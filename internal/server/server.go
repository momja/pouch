package server

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/momja/pouch/web"
)

// Server wires Pouch's HTTP API and the embedded frontend together.
type Server struct {
	cfg      Config
	wallabag *wallabagClient
	static   fs.FS
	progress *progressStore
	notes    *notesStore
}

// New configures the Wallabag client and returns a ready-to-run http.Server.
func New(cfg Config) *http.Server {
	staticFS, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}
	s := &Server{
		cfg:      cfg,
		wallabag: newWallabagClient(cfg),
		static:   staticFS,
		progress: newProgressStore(cfg.DataDir),
		notes:    newNotesStore(cfg.DataDir),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/entries", s.handleListEntries)
	mux.HandleFunc("POST /api/entries", s.handleCreateEntry)
	mux.HandleFunc("GET /api/entries/{id}", s.handleGetEntry)
	mux.HandleFunc("PATCH /api/entries/{id}", s.handlePatchEntry)
	mux.HandleFunc("DELETE /api/entries/{id}", s.handleDeleteEntry)
	// POST (not PUT) so navigator.sendBeacon can report on page unload.
	mux.HandleFunc("POST /api/entries/{id}/progress", s.handleSetProgress)
	mux.HandleFunc("GET /api/entries/{id}/notes", s.handleListNotes)
	mux.HandleFunc("POST /api/entries/{id}/notes", s.handleCreateNote)
	mux.HandleFunc("PATCH /api/entries/{id}/notes/{noteID}", s.handleUpdateNote)
	mux.HandleFunc("DELETE /api/entries/{id}/notes/{noteID}", s.handleDeleteNote)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("DELETE /api/state/last", s.handleClearLast)
	mux.HandleFunc("GET /api/tags", s.handleTags)
	mux.HandleFunc("GET /api/quick-add", s.handleQuickAdd)
	mux.HandleFunc("/", s.handleStatic)

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok"))
}

// handleStatic serves the embedded frontend. Unknown paths fall back to
// index.html so client-side routes (e.g. /add from the share target) work.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path[1:]
	if p != "" {
		if f, err := s.static.Open(p); err == nil {
			f.Close()
			http.ServeFileFS(w, r, s.static, p)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; img-src * data: blob:; media-src *; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
	http.ServeFileFS(w, r, s.static, "index.html")
}
