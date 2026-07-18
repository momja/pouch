package server

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// progressStore remembers how far into each article the reader got, plus
// which article was open most recently. It backs Pouch's scroll memory:
// positions survive restarts via a small JSON file under the data dir.
// Positions are fractions of the article's scroll range (0..1) so they
// transfer between devices with different fonts and viewport sizes.
//
// Conflict rule (per project owner): when several browsers report different
// positions for the same entry, the furthest one wins — Set keeps the max.
type progressStore struct {
	mu   sync.Mutex
	path string // "" = persistence disabled (data dir unwritable)
	data progressData
}

type progressData struct {
	Last    *lastRead       `json:"last,omitempty"`
	Entries map[int]float64 `json:"entries"`
}

type lastRead struct {
	ID int       `json:"id"`
	At time.Time `json:"at"`
}

func newProgressStore(dataDir string) *progressStore {
	s := &progressStore{data: progressData{Entries: map[int]float64{}}}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Printf("progress: cannot create data dir %s (%v); scroll memory will not survive restarts", dataDir, err)
		return s
	}
	s.path = filepath.Join(dataDir, "progress.json")
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return s // first run
	}
	if err := json.Unmarshal(raw, &s.data); err != nil || s.data.Entries == nil {
		log.Printf("progress: ignoring unreadable %s", s.path)
		s.data = progressData{Entries: map[int]float64{}}
	}
	return s
}

// save persists the store; call with the lock held.
func (s *progressStore) save() {
	if s.path == "" {
		return
	}
	raw, _ := json.Marshal(s.data)
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err == nil {
		if err := os.Rename(tmp, s.path); err != nil {
			log.Printf("progress: save failed: %v", err)
		}
	} else {
		log.Printf("progress: save failed: %v", err)
	}
}

// Set records a position for an entry and marks it as the last-open article.
// The stored position only ever moves forward (furthest-wins); the returned
// value is what's stored.
func (s *progressStore) Set(id int, p float64) float64 {
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.data.Entries[id]; ok && prev > p {
		p = prev
	}
	s.data.Entries[id] = p
	s.data.Last = &lastRead{ID: id, At: time.Now().UTC()}
	s.save()
	return p
}

func (s *progressStore) Get(id int) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Entries[id]
}

// Forget drops an entry's position (entry deleted).
func (s *progressStore) Forget(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Entries, id)
	if s.data.Last != nil && s.data.Last.ID == id {
		s.data.Last = nil
	}
	s.save()
}

// ClearLast forgets which article was open (reader closed deliberately)
// without touching stored positions.
func (s *progressStore) ClearLast() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Last == nil {
		return
	}
	s.data.Last = nil
	s.save()
}

// LastOpen returns the most recently open entry and its position, or ok=false.
func (s *progressStore) LastOpen() (id int, p float64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Last == nil {
		return 0, 0, false
	}
	return s.data.Last.ID, s.data.Entries[s.data.Last.ID], true
}
