package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// V002 P5 — file durable générique (bbolt), réutilisable. Sémantique
// at-least-once : enqueue → lease (invisibilité temporaire) → ack (suppression)
// ou nack (retry après backoff). Survit au restart : un message non-acké est
// rejoué. Ordre FIFO par file nommée. Le temps (nowMs) est injecté → testable.

// racine des files (sous-bucket par nom de file).
var bktQueues = []byte("queues")

// QueueItem = un élément de file.
type QueueItem struct {
	ID           uint64 `json:"id"`
	Payload      []byte `json:"payload"`
	Attempts     int    `json:"attempts"`
	EnqueuedMs   int64  `json:"enqueued_ms"`
	NotBeforeMs  int64  `json:"not_before_ms"`  // pas avant (backoff)
	LeaseUntilMs int64  `json:"lease_until_ms"` // invisible jusqu'à (0 = disponible)
}

func queueBucket(tx *bolt.Tx, queue string) (*bolt.Bucket, error) {
	root, err := tx.CreateBucketIfNotExists(bktQueues)
	if err != nil {
		return nil, err
	}
	return root.CreateBucketIfNotExists([]byte(queue))
}

// QueueEnqueue ajoute un payload en fin de file ; renvoie l'id (seq monotone).
func (s *Store) QueueEnqueue(queue string, payload []byte, nowMs int64) (uint64, error) {
	var id uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := queueBucket(tx, queue)
		if err != nil {
			return err
		}
		id, _ = b.NextSequence()
		it := QueueItem{ID: id, Payload: payload, EnqueuedMs: nowMs, NotBeforeMs: nowMs}
		val, err := json.Marshal(it)
		if err != nil {
			return fmt.Errorf("marshal queue item: %w", err)
		}
		return b.Put(seqKey(id), val)
	})
	return id, err
}

// QueueLease réserve jusqu'à max éléments disponibles (notBefore<=now, non leasés),
// pose un lease de leaseMs, et les renvoie. Ordre FIFO.
func (s *Store) QueueLease(queue string, max int, leaseMs, nowMs int64) ([]QueueItem, error) {
	var out []QueueItem
	err := s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(bktQueues)
		if root == nil {
			return nil
		}
		b := root.Bucket([]byte(queue))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil && len(out) < max; k, v = c.Next() {
			var it QueueItem
			if json.Unmarshal(v, &it) != nil {
				continue
			}
			if it.NotBeforeMs > nowMs || it.LeaseUntilMs > nowMs {
				continue // pas encore dû, ou déjà leasé par un autre consommateur
			}
			it.LeaseUntilMs = nowMs + leaseMs
			nv, err := json.Marshal(it)
			if err != nil {
				return err
			}
			if err := b.Put(k, nv); err != nil {
				return err
			}
			out = append(out, it)
		}
		return nil
	})
	return out, err
}

// QueueAck supprime définitivement un élément (traité avec succès).
func (s *Store) QueueAck(queue string, id uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(bktQueues)
		if root == nil {
			return nil
		}
		b := root.Bucket([]byte(queue))
		if b == nil {
			return nil
		}
		return b.Delete(seqKey(id))
	})
}

// QueueNack replace un élément en attente après backoff (Attempts++). Renvoie le
// nombre de tentatives ; si maxAttempts>0 est atteint, l'élément est supprimé
// (rejeté définitivement) et ok=false.
func (s *Store) QueueNack(queue string, id uint64, backoffMs, nowMs int64, maxAttempts int) (attempts int, kept bool, err error) {
	err = s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(bktQueues)
		if root == nil {
			return nil
		}
		b := root.Bucket([]byte(queue))
		if b == nil {
			return nil
		}
		key := seqKey(id)
		raw := b.Get(key)
		if raw == nil {
			return nil
		}
		var it QueueItem
		if json.Unmarshal(raw, &it) != nil {
			return nil
		}
		it.Attempts++
		attempts = it.Attempts
		if maxAttempts > 0 && it.Attempts >= maxAttempts {
			return b.Delete(key) // épuisé → drop (kept reste false)
		}
		it.NotBeforeMs = nowMs + backoffMs
		it.LeaseUntilMs = 0
		nv, err := json.Marshal(it)
		if err != nil {
			return err
		}
		kept = true
		return b.Put(key, nv)
	})
	return attempts, kept, err
}

// QueueDepth renvoie le nombre d'éléments restants dans une file.
func (s *Store) QueueDepth(queue string) (int, error) {
	var n int
	err := s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket(bktQueues)
		if root == nil {
			return nil
		}
		b := root.Bucket([]byte(queue))
		if b == nil {
			return nil
		}
		n = b.Stats().KeyN
		return nil
	})
	return n, err
}

func seqKey(id uint64) []byte {
	k := make([]byte, 8)
	binary.BigEndian.PutUint64(k, id)
	return k
}
