package session

import (
	"crypto/tls"
	"encoding/gob"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	path string

	mu       sync.Mutex
	entries  map[string]*tls.ClientSessionState
	dirty    bool
	lastSave time.Time
}

func NewStore(path string) *Store {
	s := &Store{path: path, entries: make(map[string]*tls.ClientSessionState)}
	s.load()
	return s
}

func (s *Store) Get(sessionKey string) (*tls.ClientSessionState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.entries[sessionKey]
	return v, ok
}

func (s *Store) Put(sessionKey string, cs *tls.ClientSessionState) {
	s.mu.Lock()
	if cs == nil {
		delete(s.entries, sessionKey)
	} else {
		s.entries[sessionKey] = cs
	}
	s.dirty = true
	shouldFlush := time.Since(s.lastSave) > 5*time.Second
	s.mu.Unlock()
	if shouldFlush {
		_ = s.Flush()
	}
}

type diskEntry struct {
	Key    string
	Ticket []byte
}

func (s *Store) Flush() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".sess-*")
	if err != nil {
		return err
	}
	enc := gob.NewEncoder(f)
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	if err := enc.Encode(keys); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	f.Close()
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	s.dirty = false
	s.lastSave = time.Now()
	return nil
}

func (s *Store) load() {
	if s.path == "" {
		return
	}
	f, err := os.Open(s.path)
	if err != nil {
		return
	}
	defer f.Close()
	var keys []string
	_ = gob.NewDecoder(f).Decode(&keys)
}
