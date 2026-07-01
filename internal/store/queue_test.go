package store

import (
	"path/filepath"
	"testing"
)

func tempStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestQueueFifoAndAck(t *testing.T) {
	s, _ := tempStore(t)
	for _, p := range []string{"a", "b", "c"} {
		if _, err := s.QueueEnqueue("reports", []byte(p), 1000); err != nil {
			t.Fatal(err)
		}
	}
	if d, _ := s.QueueDepth("reports"); d != 3 {
		t.Fatalf("depth=%d want 3", d)
	}
	items, _ := s.QueueLease("reports", 10, 5000, 2000)
	if len(items) != 3 || string(items[0].Payload) != "a" || string(items[2].Payload) != "c" {
		t.Fatalf("FIFO cassé: %+v", items)
	}
	// ack le premier → depth 2
	if err := s.QueueAck("reports", items[0].ID); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.QueueDepth("reports"); d != 2 {
		t.Fatalf("depth après ack=%d want 2", d)
	}
}

func TestQueueLeaseInvisibility(t *testing.T) {
	s, _ := tempStore(t)
	s.QueueEnqueue("q", []byte("x"), 1000)
	// 1er lease à t=2000, lease 5000ms → invisible jusqu'à 7000
	if got, _ := s.QueueLease("q", 10, 5000, 2000); len(got) != 1 {
		t.Fatalf("1er lease devrait renvoyer 1, got %d", len(got))
	}
	// re-lease pendant la fenêtre → rien
	if got, _ := s.QueueLease("q", 10, 5000, 3000); len(got) != 0 {
		t.Fatalf("lease pendant invisibilité devrait renvoyer 0, got %d", len(got))
	}
	// après expiration du lease → re-disponible
	if got, _ := s.QueueLease("q", 10, 5000, 8000); len(got) != 1 {
		t.Fatalf("après expiration lease devrait renvoyer 1, got %d", len(got))
	}
}

func TestQueueNackBackoffAndRetry(t *testing.T) {
	s, _ := tempStore(t)
	s.QueueEnqueue("q", []byte("x"), 1000)
	items, _ := s.QueueLease("q", 1, 1000, 2000)
	if len(items) != 1 {
		t.Fatal("lease initial")
	}
	// nack avec backoff 10s → notBefore=12000, attempts=1
	att, kept, err := s.QueueNack("q", items[0].ID, 10_000, 2000, 5)
	if err != nil || !kept || att != 1 {
		t.Fatalf("nack: att=%d kept=%v err=%v", att, kept, err)
	}
	// avant le backoff → invisible
	if got, _ := s.QueueLease("q", 10, 1000, 5000); len(got) != 0 {
		t.Fatalf("avant backoff devrait renvoyer 0, got %d", len(got))
	}
	// après le backoff → re-disponible, attempts préservé
	got, _ := s.QueueLease("q", 10, 1000, 13000)
	if len(got) != 1 || got[0].Attempts != 1 {
		t.Fatalf("après backoff: len=%d attempts=%d", len(got), got[0].Attempts)
	}
}

func TestQueueNackMaxAttemptsDrops(t *testing.T) {
	s, _ := tempStore(t)
	s.QueueEnqueue("q", []byte("x"), 0)
	id := uint64(1)
	// 3 nacks avec maxAttempts=3 → au 3e, drop
	_, kept1, _ := s.QueueNack("q", id, 0, 0, 3)
	_, kept2, _ := s.QueueNack("q", id, 0, 0, 3)
	_, kept3, _ := s.QueueNack("q", id, 0, 0, 3)
	if !kept1 || !kept2 || kept3 {
		t.Fatalf("kept: %v %v %v (le 3e doit dropper)", kept1, kept2, kept3)
	}
	if d, _ := s.QueueDepth("q"); d != 0 {
		t.Fatalf("depth après épuisement=%d want 0", d)
	}
}

// CDC-B1 : durabilité — un message non-acké survit à un restart (réouverture).
func TestQueueDurableAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.QueueEnqueue("reports", []byte("rapport-important"), 1000)
	s.QueueLease("reports", 1, 1000, 2000) // leasé mais PAS acké
	_ = s.Close()

	// réouverture (= restart de l'agent)
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if d, _ := s2.QueueDepth("reports"); d != 1 {
		t.Fatalf("message perdu au restart: depth=%d want 1", d)
	}
	// le lease n'ayant pas d'autorité après restart (temps avancé), il est rejoué
	got, _ := s2.QueueLease("reports", 10, 1000, 100000)
	if len(got) != 1 || string(got[0].Payload) != "rapport-important" {
		t.Fatalf("message non rejoué après restart: %+v", got)
	}
}
