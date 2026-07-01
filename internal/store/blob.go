package store

import bolt "go.etcd.io/bbolt"

// V002 P5 — petit KV binaire durable (config batch persistée : survit au restart
// et à la déconnexion du mh).
var bktBlobs = []byte("blobs")

// PutBlob écrit une valeur binaire sous une clé.
func (s *Store) PutBlob(key string, val []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bktBlobs)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), val)
	})
}

// GetBlob lit une valeur binaire (nil si absente).
func (s *Store) GetBlob(key string) ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bktBlobs)
		if b == nil {
			return nil
		}
		if v := b.Get([]byte(key)); v != nil {
			out = append([]byte(nil), v...) // copie (la vue bbolt est éphémère)
		}
		return nil
	})
	return out, err
}
