package kb

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record is one knowledge-base entry. Data holds the user-defined fields.
type Record struct {
	ID      string                 `json:"id"`
	Created int64                  `json:"created"`
	Updated int64                  `json:"updated"`
	Data    map[string]interface{} `json:"data"`
}

// Store persists collections as JSON files under a directory.
type Store struct {
	mu  sync.Mutex
	dir string
}

// New creates a store backed by dir (created if missing).
func New(dir string) *Store {
	_ = os.MkdirAll(dir, 0o755)
	return &Store{dir: dir}
}

func (s *Store) file(c string) string { return filepath.Join(s.dir, c+".json") }

func (s *Store) load(c string) (map[string]Record, error) {
	b, err := os.ReadFile(s.file(c))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Record{}, nil
		}
		return nil, err
	}
	m := map[string]Record{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) save(c string, m map[string]Record) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(s.file(c), b, 0o644)
}

// List returns all records in a collection.
func (s *Store) List(c string) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load(c)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	return out, nil
}

// Add inserts a new record and returns it.
func (s *Store) Add(c string, data map[string]interface{}) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load(c)
	if err != nil {
		return Record{}, err
	}
	id := newID()
	now := time.Now().Unix()
	r := Record{ID: id, Created: now, Updated: now, Data: data}
	m[id] = r
	return r, s.save(c, m)
}

// Update replaces a record's data.
func (s *Store) Update(c, id string, data map[string]interface{}) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load(c)
	if err != nil {
		return Record{}, err
	}
	r, ok := m[id]
	if !ok {
		return Record{}, fmt.Errorf("record not found")
	}
	if data != nil {
		r.Data = data
	}
	r.Updated = time.Now().Unix()
	m[id] = r
	return r, s.save(c, m)
}

// Delete removes a record.
func (s *Store) Delete(c, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load(c)
	if err != nil {
		return err
	}
	delete(m, id)
	return s.save(c, m)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
