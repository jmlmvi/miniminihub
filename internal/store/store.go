// Package store est le store local embarqué (bbolt, D-19) : logs/audit/buffer.
// Pur-Go, 1 fichier, zéro CGO → préserve le binaire statique. PAS la source de
// vérité (le Hub l'est) : buffer + journal local à rétention courte.
package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bktEvents   = []byte("events")
	bktCounters = []byte("counters")
)

// Store encapsule la base bbolt locale.
type Store struct {
	db *bolt.DB
}

// Event = une entrée du journal local.
type Event struct {
	TsMs   int64  `json:"ts_ms"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Open ouvre (ou crée) la base et garantit les buckets.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("mkdir store dir: %w", err)
		}
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bbolt %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bktEvents, bktCounters} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init buckets: %w", err)
	}
	return &Store{db: db}, nil
}

// Close ferme la base.
func (s *Store) Close() error { return s.db.Close() }

// Incr incrémente un compteur persistant et retourne la nouvelle valeur.
func (s *Store) Incr(name string) (uint64, error) {
	var out uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bktCounters)
		cur := b.Get([]byte(name))
		var v uint64
		if len(cur) == 8 {
			v = binary.BigEndian.Uint64(cur)
		}
		v++
		out = v
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, v)
		return b.Put([]byte(name), buf)
	})
	return out, err
}

// Counter lit la valeur courante d'un compteur (0 si absent).
func (s *Store) Counter(name string) (uint64, error) {
	var v uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		cur := tx.Bucket(bktCounters).Get([]byte(name))
		if len(cur) == 8 {
			v = binary.BigEndian.Uint64(cur)
		}
		return nil
	})
	return v, err
}

// AppendEvent journalise un événement (clé = timestamp ns, ordonné).
func (s *Store) AppendEvent(tsMs int64, kind, detail string) error {
	e := Event{TsMs: tsMs, Kind: kind, Detail: detail}
	val, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bktEvents)
		key := make([]byte, 8)
		seq, _ := b.NextSequence()
		binary.BigEndian.PutUint64(key, seq)
		return b.Put(key, val)
	})
}

// CountEvents retourne le nombre d'événements stockés.
func (s *Store) CountEvents() (int, error) {
	var n int
	err := s.db.View(func(tx *bolt.Tx) error {
		n = tx.Bucket(bktEvents).Stats().KeyN
		return nil
	})
	return n, err
}
