package server

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// note is one inline comment on an article: a span of the reader's text
// (character offsets into the concatenated text of the rendered article) plus
// the reader's remark. The quote is stored too so a note can still be placed
// when the offsets no longer line up — wallabag can re-fetch an article and
// shift everything by a few characters.
type note struct {
	ID        int       `json:"id"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
	Quote     string    `json:"quote"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// notesStore keeps every note, grouped by entry, in a small JSON file next to
// the scroll-memory file. Same trade-offs as progressStore: single user, no
// auth, in-memory map written out on every change.
type notesStore struct {
	mu   sync.Mutex
	path string // "" = persistence disabled (data dir unwritable)
	data notesData
}

type notesData struct {
	NextID int            `json:"nextId"`
	Notes  map[int][]note `json:"notes"` // entry id -> notes, sorted by Start
}

func newNotesStore(dataDir string) *notesStore {
	s := &notesStore{data: notesData{NextID: 1, Notes: map[int][]note{}}}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Printf("notes: cannot create data dir %s (%v); notes will not survive restarts", dataDir, err)
		return s
	}
	s.path = filepath.Join(dataDir, "notes.json")
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return s // first run
	}
	if err := json.Unmarshal(raw, &s.data); err != nil || s.data.Notes == nil {
		log.Printf("notes: ignoring unreadable %s", s.path)
		s.data = notesData{NextID: 1, Notes: map[int][]note{}}
	}
	if s.data.NextID < 1 {
		s.data.NextID = 1
	}
	return s
}

// save persists the store; call with the lock held.
func (s *notesStore) save() {
	writeJSONFile(s.path, s.data, "notes")
}

// List returns an entry's notes, earliest position first.
func (s *notesStore) List(entry int) []note {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]note, len(s.data.Notes[entry]))
	copy(out, s.data.Notes[entry])
	return out
}

func (s *notesStore) Count(entry int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data.Notes[entry])
}

// Add stores a new note and returns it with its assigned id.
func (s *notesStore) Add(entry int, n note) note {
	s.mu.Lock()
	defer s.mu.Unlock()
	n.ID = s.data.NextID
	s.data.NextID++
	n.CreatedAt = time.Now().UTC()
	list := append(s.data.Notes[entry], n)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Start < list[j].Start })
	s.data.Notes[entry] = list
	s.save()
	return n
}

// Update rewrites a note's body.
func (s *notesStore) Update(entry, id int, body string) (note, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, n := range s.data.Notes[entry] {
		if n.ID == id {
			s.data.Notes[entry][i].Body = body
			s.save()
			return s.data.Notes[entry][i], true
		}
	}
	return note{}, false
}

func (s *notesStore) Delete(entry, id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.data.Notes[entry]
	for i, n := range list {
		if n.ID == id {
			list = append(list[:i], list[i+1:]...)
			if len(list) == 0 {
				delete(s.data.Notes, entry)
			} else {
				s.data.Notes[entry] = list
			}
			s.save()
			return true
		}
	}
	return false
}

// Forget drops every note on an entry (entry deleted).
func (s *notesStore) Forget(entry int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Notes[entry]; !ok {
		return
	}
	delete(s.data.Notes, entry)
	s.save()
}

// writeJSONFile atomically replaces path with v encoded as JSON. path == ""
// means persistence is disabled, in which case this is a no-op.
func writeJSONFile(path string, v any, what string) {
	if path == "" {
		return
	}
	raw, _ := json.Marshal(v)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		log.Printf("%s: save failed: %v", what, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("%s: save failed: %v", what, err)
	}
}
