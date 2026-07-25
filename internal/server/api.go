package server

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Strubbl/wallabago/v9"
)

// apiCall wraps wallabago.APICall, which returns (nil, nil) when wallabag
// answers with a non-200 status; normalize that into a real error.
func apiCall(apiURL, method string, postData []byte) ([]byte, error) {
	body, err := wallabago.APICall(apiURL, method, postData)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("wallabag returned a non-OK status for %s %s", method, apiURL)
	}
	return body, nil
}

// entry is the trimmed shape Pouch's frontend consumes.
type entry struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Domain      string   `json:"domain"`
	ReadingTime int      `json:"readingTime"`
	CreatedAt   string   `json:"createdAt"`
	Preview     string   `json:"preview"`
	Starred     bool     `json:"starred"`
	Archived    bool     `json:"archived"`
	Tags        []string `json:"tags"`
	Content     string   `json:"content,omitempty"`
	Progress    float64  `json:"progress,omitempty"` // scroll memory, 0..1
	NoteCount   int      `json:"noteCount"`          // inline comments on this entry
	Notes       []note   `json:"notes,omitempty"`    // full entry only
}

func fromItem(it wallabago.Item, withContent bool) entry {
	e := entry{
		ID:          it.ID,
		Title:       it.Title,
		URL:         it.URL,
		Domain:      it.DomainName,
		ReadingTime: it.ReadingTime,
		Preview:     it.PreviewPicture,
		Starred:     it.IsStarred == 1,
		Archived:    it.IsArchived == 1,
		Tags:        []string{},
	}
	if it.CreatedAt != nil && !it.CreatedAt.IsZero() {
		e.CreatedAt = it.CreatedAt.Format(time.RFC3339)
	}
	for _, t := range it.Tags {
		e.Tags = append(e.Tags, t.Label)
	}
	if e.Title == "" {
		e.Title = it.URL
	}
	if withContent {
		e.Content = it.Content
	}
	return e
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func apiError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// GET /api/entries?filter=unread|archive|starred|all&page=N&perPage=N&tag=slug
func (s *Server) handleListEntries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	archive, starred := -1, -1
	switch q.Get("filter") {
	case "", "unread":
		archive = 0
	case "archive":
		archive = 1
	case "starred":
		starred = 1
	case "all":
	default:
		apiError(w, http.StatusBadRequest, fmt.Errorf("unknown filter %q", q.Get("filter")))
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(q.Get("perPage"))
	if perPage < 1 || perPage > 100 {
		perPage = 30
	}
	res, err := wallabago.GetEntries(wallabago.APICall, archive, starred,
		"created", "desc", page, perPage, q.Get("tag"), -1, -1, "metadata", "")
	if err != nil {
		apiError(w, http.StatusBadGateway, err)
		return
	}
	items := make([]entry, 0, len(res.Embedded.Items))
	for _, it := range res.Embedded.Items {
		e := fromItem(it, false)
		e.NoteCount = s.notes.Count(it.ID)
		items = append(items, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page":  res.Page,
		"pages": res.Pages,
		"total": res.Total,
		"items": items,
	})
}

// createEntry POSTs a new URL to wallabag and returns the created item.
func createEntry(rawURL, tags string) (wallabago.Item, error) {
	var it wallabago.Item
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return it, fmt.Errorf("not a valid http(s) URL: %q", rawURL)
	}
	payload, _ := json.Marshal(map[string]string{"url": rawURL, "tags": tags})
	body, err := apiCall(wallabago.LibConfig.WallabagURL+"/api/entries.json", "POST", payload)
	if err != nil {
		return it, err
	}
	err = json.Unmarshal(body, &it)
	return it, err
}

// POST /api/entries {"url": "...", "tags": "a,b"}
func (s *Server) handleCreateEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Tags string `json:"tags"`
	}
	if err := readJSON(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	it, err := createEntry(req.URL, req.Tags)
	if err != nil {
		apiError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusCreated, fromItem(it, false))
}

func entryID(r *http.Request) (int, error) {
	return strconv.Atoi(r.PathValue("id"))
}

// GET /api/entries/{id} — full entry including content.
func (s *Server) handleGetEntry(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	it, err := wallabago.GetEntry(wallabago.APICall, id)
	if err != nil {
		apiError(w, http.StatusBadGateway, err)
		return
	}
	e := fromItem(it, true)
	e.Progress = s.progress.Get(id)
	e.Notes = s.notes.List(id)
	e.NoteCount = len(e.Notes)
	writeJSON(w, http.StatusOK, e)
}

// PATCH /api/entries/{id} {"archive": 0|1, "starred": 0|1}
func (s *Server) handlePatchEntry(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Archive *int    `json:"archive"`
		Starred *int    `json:"starred"`
		Title   *string `json:"title"`
		Tags    *string `json:"tags"`
	}
	if err := readJSON(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	patch := map[string]any{}
	if req.Archive != nil {
		patch["archive"] = *req.Archive
	}
	if req.Starred != nil {
		patch["starred"] = *req.Starred
	}
	if req.Title != nil {
		patch["title"] = *req.Title
	}
	if req.Tags != nil {
		patch["tags"] = *req.Tags
	}
	if len(patch) == 0 {
		apiError(w, http.StatusBadRequest, fmt.Errorf("empty patch"))
		return
	}
	payload, _ := json.Marshal(patch)
	body, err := apiCall(wallabago.LibConfig.WallabagURL+"/api/entries/"+strconv.Itoa(id)+".json", "PATCH", payload)
	if err != nil {
		apiError(w, http.StatusBadGateway, err)
		return
	}
	var it wallabago.Item
	if err := json.Unmarshal(body, &it); err != nil {
		apiError(w, http.StatusBadGateway, err)
		return
	}
	e := fromItem(it, false)
	e.NoteCount = s.notes.Count(id)
	writeJSON(w, http.StatusOK, e)
}

// DELETE /api/entries/{id}
func (s *Server) handleDeleteEntry(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := apiCall(wallabago.LibConfig.WallabagURL+"/api/entries/"+strconv.Itoa(id)+".json", "DELETE", nil); err != nil {
		apiError(w, http.StatusBadGateway, err)
		return
	}
	s.progress.Forget(id)
	s.notes.Forget(id)
	w.WriteHeader(http.StatusNoContent)
}

/* ---------- Inline notes ---------- */

func noteID(r *http.Request) (int, error) {
	return strconv.Atoi(r.PathValue("noteID"))
}

// GET /api/entries/{id}/notes
func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.notes.List(id))
}

// POST /api/entries/{id}/notes {"start":12,"end":48,"quote":"…","body":"…"}
// Offsets are character positions into the article text as the reader renders
// it; the quote lets the client re-find the span if they ever drift.
func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Start int    `json:"start"`
		End   int    `json:"end"`
		Quote string `json:"quote"`
		Body  string `json:"body"`
	}
	if err := readJSON(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	if req.End <= req.Start || strings.TrimSpace(req.Body) == "" {
		apiError(w, http.StatusBadRequest, fmt.Errorf("a note needs a non-empty range and body"))
		return
	}
	writeJSON(w, http.StatusCreated, s.notes.Add(id, note{
		Start: req.Start,
		End:   req.End,
		Quote: req.Quote,
		Body:  strings.TrimSpace(req.Body),
	}))
}

// PATCH /api/entries/{id}/notes/{noteID} {"body": "…"}
func (s *Server) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	nid, nerr := noteID(r)
	if err != nil || nerr != nil {
		apiError(w, http.StatusBadRequest, fmt.Errorf("bad entry or note id"))
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Body) == "" {
		apiError(w, http.StatusBadRequest, fmt.Errorf("body must be {\"body\": \"…\"}"))
		return
	}
	n, ok := s.notes.Update(id, nid, strings.TrimSpace(req.Body))
	if !ok {
		apiError(w, http.StatusNotFound, fmt.Errorf("no such note"))
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// DELETE /api/entries/{id}/notes/{noteID}
func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	nid, nerr := noteID(r)
	if err != nil || nerr != nil {
		apiError(w, http.StatusBadRequest, fmt.Errorf("bad entry or note id"))
		return
	}
	if !s.notes.Delete(id, nid) {
		apiError(w, http.StatusNotFound, fmt.Errorf("no such note"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

/* ---------- Scroll memory ---------- */

// POST /api/entries/{id}/progress {"progress": 0.42}
// Stores the reading position (furthest-wins) and marks the entry as the
// last-open article. Trusts the client-reported id; there is no auth anyway.
func (s *Server) handleSetProgress(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Progress *float64 `json:"progress"`
	}
	if err := readJSON(r, &req); err != nil || req.Progress == nil {
		apiError(w, http.StatusBadRequest, fmt.Errorf("body must be {\"progress\": 0..1}"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]float64{"progress": s.progress.Set(id, *req.Progress)})
}

// GET /api/state — what the frontend needs on boot: the last-open article
// (if any) so a fresh visit can resume it at the stored position.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	var last any
	if id, p, ok := s.progress.LastOpen(); ok {
		last = map[string]any{"id": id, "progress": p}
	}
	writeJSON(w, http.StatusOK, map[string]any{"last": last})
}

// DELETE /api/state/last — the reader was closed deliberately; a fresh visit
// should show the list again instead of resuming.
func (s *Server) handleClearLast(w http.ResponseWriter, r *http.Request) {
	s.progress.ClearLast()
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/tags
func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	tags, err := wallabago.GetTags(wallabago.APICall)
	if err != nil {
		apiError(w, http.StatusBadGateway, err)
		return
	}
	if tags == nil {
		tags = []wallabago.Tag{}
	}
	writeJSON(w, http.StatusOK, tags)
}

// GET /api/quick-add?url=... — the endpoint iOS Shortcuts and the
// bookmarklet hit. Responds with a small self-contained HTML page.
func (s *Server) handleQuickAdd(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		quickAddPage(w, http.StatusBadRequest, "Nothing to save", "No url parameter was given.", "")
		return
	}
	it, err := createEntry(rawURL, r.URL.Query().Get("tags"))
	if err != nil {
		quickAddPage(w, http.StatusBadGateway, "Could not save", err.Error(), "")
		return
	}
	title := it.Title
	if title == "" {
		title = rawURL
	}
	quickAddPage(w, http.StatusOK, "Saved to Pouch", title, it.DomainName)
}

func quickAddPage(w http.ResponseWriter, status int, heading, detail, domain string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	icon := "✓"
	if status != http.StatusOK {
		icon = "!"
	}
	if domain != "" {
		domain = `<p class="domain">` + html.EscapeString(domain) + `</p>`
	}
	fmt.Fprintf(w, quickAddHTML, icon, html.EscapeString(heading), html.EscapeString(detail), domain)
}

const quickAddHTML = `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Pouch</title>
<style>
  body { margin: 0; min-height: 100vh; display: grid; place-items: center;
         background: #f7f4ee; color: #1f1d1a;
         font: 16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif; }
  .card { max-width: 26rem; margin: 1rem; padding: 2rem 2.2rem; text-align: center;
          background: #fff; border: 1px solid #e4dfd4; border-radius: 14px; }
  .badge { width: 3rem; height: 3rem; margin: 0 auto 1rem; display: grid; place-items: center;
           background: #b4533a; color: #fff; font-size: 1.4rem; border-radius: 50%%; }
  h1 { margin: 0 0 .5rem; font-size: 1.2rem; font-family: "Iowan Old Style", Palatino, Georgia, serif; }
  p { margin: .25rem 0; overflow-wrap: anywhere; }
  .domain { color: #6e675c; font-size: .85rem; }
  a { color: #b4533a; }
</style>
<div class="card">
  <div class="badge">%s</div>
  <h1>%s</h1>
  <p>%s</p>
  %s
  <p style="margin-top:1.2rem"><a href="/">Open Pouch</a></p>
</div>
`
