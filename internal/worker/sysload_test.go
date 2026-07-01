package worker

import (
	"sync"
	"sync/atomic"
	"testing"
)

// resetGauges remet les jauges de conns actives à zéro (isolation des tests).
func resetGauges() {
	atomic.StoreInt64(&activeProxy, 0)
	atomic.StoreInt64(&activeProxyTor, 0)
}

func TestClampPct(t *testing.T) {
	cases := []struct {
		in   float64
		want int32
	}{
		{-5, 0}, {0, 0}, {12.4, 12}, {12.6, 13}, {100, 100}, {150, 100},
	}
	for _, c := range cases {
		if got := clampPct(c.in); got != c.want {
			t.Errorf("clampPct(%v)=%d, want %d", c.in, got, c.want)
		}
	}
}

func TestCPUPct(t *testing.T) {
	// 100 ticks écoulés dont 25 idle → 75% de charge.
	prev := cpuSample{total: 1000, idle: 500}
	cur := cpuSample{total: 1100, idle: 525}
	pct, ok := cpuPct(prev, cur)
	if !ok || pct != 75 {
		t.Fatalf("cpuPct = %d, %v ; want 75, true", pct, ok)
	}
	// Pas de delta → non fiable.
	if _, ok := cpuPct(cur, cur); ok {
		t.Error("cpuPct sans delta devrait renvoyer ok=false")
	}
	// Compteur qui recule (redémarrage) → non fiable.
	if _, ok := cpuPct(cur, prev); ok {
		t.Error("cpuPct avec total décroissant devrait renvoyer ok=false")
	}
}

func TestConnsByService(t *testing.T) {
	// État initial : aucune conn.
	resetGauges()
	if m := connsByService(); len(m) != 0 {
		t.Fatalf("jauge initiale non vide: %v", m)
	}
	egressBegin(false) // proxy direct
	egressBegin(true)  // proxy-tor
	egressBegin(true)  // proxy-tor
	m := connsByService()
	if m["proxy"] != 1 || m["proxy-tor"] != 2 {
		t.Fatalf("conns actives = %v ; want proxy=1 proxy-tor=2", m)
	}
	egressEnd(true)
	egressEnd(true)
	egressEnd(false)
	if m := connsByService(); len(m) != 0 {
		t.Fatalf("jauge non revenue à zéro: %v", m)
	}
}

// Concurrence : begin/end en parallèle reviennent à zéro (pas de course).
func TestConnsByServiceConcurrent(t *testing.T) {
	resetGauges()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(vt bool) {
			defer wg.Done()
			egressBegin(vt)
			egressEnd(vt)
		}(i%2 == 0)
	}
	wg.Wait()
	if m := connsByService(); len(m) != 0 {
		t.Fatalf("jauge non nulle après charge concurrente: %v", m)
	}
}

func TestMemDiskPctSane(t *testing.T) {
	// Bornes 0..100 sur la vraie machine (pas de mock : lecture réelle /proc + statfs).
	if p := memPct(); p < 0 || p > 100 {
		t.Errorf("memPct hors bornes: %d", p)
	}
	if p := diskPct("/"); p < 0 || p > 100 {
		t.Errorf("diskPct hors bornes: %d", p)
	}
}
