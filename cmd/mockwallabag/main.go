// Command mockwallabag is a tiny in-memory fake of the Wallabag API, used
// for local development and browser testing of Pouch without a real
// Wallabag instance. It implements just the endpoints Pouch exercises:
// oauth token, entries CRUD and tags.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const timeLayout = "2006-01-02T15:04:05-0700"

type tag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
	Slug  string `json:"slug"`
}

type entryRec struct {
	ID             int    `json:"id"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	DomainName     string `json:"domain_name"`
	Content        string `json:"content"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	ReadingTime    int    `json:"reading_time"`
	IsArchived     int    `json:"is_archived"`
	IsStarred      int    `json:"is_starred"`
	PreviewPicture string `json:"preview_picture"`
	Tags           []tag  `json:"tags"`
}

type store struct {
	mu     sync.Mutex
	nextID int
	items  []*entryRec
}

func (s *store) add(e *entryRec) *entryRec {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	e.ID = s.nextID
	if e.UpdatedAt == "" {
		e.UpdatedAt = e.CreatedAt
	}
	s.items = append(s.items, e)
	return e
}

func (s *store) get(id int) *entryRec {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.items {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (s *store) delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.items {
		if e.ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return true
		}
	}
	return false
}

func mkTags(labels ...string) []tag {
	tags := make([]tag, 0, len(labels))
	for i, l := range labels {
		tags = append(tags, tag{ID: i + 1, Label: l, Slug: strings.ToLower(strings.ReplaceAll(l, " ", "-"))})
	}
	return tags
}

func para(lines ...string) string { return "<p>" + strings.Join(lines, " ") + "</p>\n" }

func seed(s *store) {
	now := time.Now()
	at := func(daysAgo int) string { return now.Add(-time.Duration(daysAgo) * 24 * time.Hour).Format(timeLayout) }

	s.add(&entryRec{
		Title: "Why We Sleep on Problems: The Science of Incubation", URL: "https://nautil.us/why-we-sleep-on-problems", DomainName: "nautil.us",
		CreatedAt: at(0), ReadingTime: 7, PreviewPicture: "https://picsum.photos/seed/pouch1/640/400",
		Tags: mkTags("science", "sleep"),
		Content: para("There is a reason the advice to “sleep on it” has survived centuries of folk wisdom: the sleeping brain keeps working on the problems the waking brain abandons.",
			"Researchers call this incubation, and its mechanics are stranger than the metaphor suggests.") +
			"<h2>The replay loop</h2>" +
			para("During slow-wave sleep, the hippocampus replays fragments of the day at high speed, shuttling them to the cortex where they are woven into existing knowledge.",
				"The result is not memory in the photographic sense, but something more useful: abstraction.") +
			"<blockquote>Sleep is the price we pay for plasticity — and the discount we get on insight.</blockquote>" +
			para("In one elegant study, participants who slept between sessions of a number puzzle were nearly three times as likely to discover its hidden shortcut than those who stayed awake the same number of hours.") +
			"<h2>What this means for your desk</h2>" +
			para("The practical upshot is humble: when you are stuck, stop. The brain that returns to the problem tomorrow will not be the one that left it tonight."),
	})
	s.add(&entryRec{
		Title: "A Field Guide to the Small Web", URL: "https://example.blog/small-web-field-guide", DomainName: "example.blog",
		CreatedAt: at(1), ReadingTime: 12, PreviewPicture: "https://picsum.photos/seed/pouch2/640/400",
		Tags: mkTags("web", "essays"),
		Content: para("Beneath the platforms, the old web is still there: hand-built HTML pages, webrings, guestbooks, sites that load in a heartbeat because they carry nothing they do not need.") +
			"<h2>What counts as small</h2>" +
			"<ul><li>Personal sites maintained by one person, for no audience in particular</li><li>Pages that work without JavaScript</li><li>Sites whose design says something about their author</li></ul>" +
			para("The small web is not nostalgia. It is a working demonstration that publishing can be a craft rather than a growth strategy.") +
			"<figure><img src=\"https://picsum.photos/seed/smallweb/800/450\" alt=\"\"><figcaption>Every small site is a room someone built by hand.</figcaption></figure>" +
			para("Start with one page. Write it yourself. Host it anywhere. Link to five sites you love, and email their authors to say so. That is the entire protocol."),
	})
	s.add(&entryRec{
		Title: "Understanding Go's Embed Directive", URL: "https://go.dev/blog/embed-directive-deep-dive", DomainName: "go.dev",
		CreatedAt: at(2), ReadingTime: 9,
		Tags: mkTags("go", "programming"),
		Content: para("Since Go 1.16, the <code>//go:embed</code> directive lets you compile static files directly into your binary — no external assets, no runtime path guessing, a single artifact to ship.") +
			"<h2>The basics</h2>" +
			"<pre><code>import \"embed\"\n\n//go:embed all:static\nvar Static embed.FS</code></pre>" +
			para("The <code>all:</code> prefix includes files that begin with dots or underscores, which the plain pattern would skip.",
				"At build time the toolchain records the file contents in the binary's data section; at runtime <code>embed.FS</code> serves them through the standard <code>fs.FS</code> interface.") +
			"<h2>Serving over HTTP</h2>" +
			"<pre><code>staticFS, _ := fs.Sub(Static, \"static\")\nhttp.Handle(\"/\", http.FileServerFS(staticFS))</code></pre>" +
			para("Because the embedded tree is read-only and hashed into the build, cache invalidation becomes a deploy concern rather than a runtime one — which is exactly where you want it."),
	})
	s.add(&entryRec{
		Title: "The Quiet Genius of the Paper Notebook", URL: "https://longform.example.com/paper-notebooks", DomainName: "longform.example.com",
		CreatedAt: at(4), ReadingTime: 6, IsStarred: 1, PreviewPicture: "https://picsum.photos/seed/pouch4/640/400",
		Tags: mkTags("essays"),
		Content: para("A notebook does not sync, does not notify, and cannot be searched. These are features.") +
			para("The friction of paper is a filter: only the thoughts worth the ink survive the trip from head to page. What remains is a record not of everything, but of what mattered.") +
			"<blockquote>The notebook is the only writing technology whose battery is always full.</blockquote>" +
			para("Digital tools remember perfectly and understand nothing. The notebook understands one thing deeply — that thinking is physical — and remembers just enough."),
	})
	s.add(&entryRec{
		Title: "How Lighthouses Are Built on Waves", URL: "https://engineering.example.org/wave-swept-lighthouses", DomainName: "engineering.example.org",
		CreatedAt: at(6), ReadingTime: 14, IsStarred: 1, PreviewPicture: "https://picsum.photos/seed/pouch5/640/400",
		Tags: mkTags("engineering", "history"),
		Content: para("A wave-swept lighthouse is a building that should not exist: a masonry tower standing in open ocean, on a reef that is submerged most of the day, assembled in the few summer hours when the sea permits.") +
			"<h2>Dovetailed stone</h2>" +
			para("The breakthrough, perfected at Eddystone and Bell Rock, was to cut each granite block with dovetail joints so the tower interlocks like a three-dimensional puzzle.",
				"Waves cannot pull apart what they can only press together — the impact of a breaker actually tightens the joints.") +
			para("Work proceeded at a pace modern schedules would not tolerate: two hours of masonry per low tide, weather permitting, for seven years.") +
			"<h2>The keeper's view</h2>" +
			para("The finished towers sway in heavy weather — a few inches at the lantern — and the keepers learned to read the sea by the pitch of the vibration underfoot."),
	})
	s.add(&entryRec{
		Title: "Recipes as Version Control", URL: "https://food.example.dev/recipes-as-version-control", DomainName: "food.example.dev",
		CreatedAt: at(9), ReadingTime: 5,
		Tags: mkTags("cooking", "essays"),
		Content: para("Every family recipe is a repository with an unreliable history. The card says one thing; your grandmother's hands did another; and somewhere between the two lies the actual dish.") +
			para("Treating recipes like code — forking, annotating, committing changes after each cook — turns dinner into a lineage you can trace.") +
			"<ul><li><strong>Fork</strong> the original before you touch it</li><li><strong>Commit</strong> one change per cook, with notes</li><li><strong>Tag</strong> the versions that made someone ask for seconds</li></ul>" +
			para("The diff between the card and the table is where the family lives."),
	})
	s.add(&entryRec{
		Title: "What Deep Time Does to the Mind", URL: "https://nautil.us/deep-time-and-the-mind", DomainName: "nautil.us",
		CreatedAt: at(12), ReadingTime: 11, IsArchived: 1,
		Tags: mkTags("science", "essays"),
		Content: para("Stand at the rim of the Grand Canyon and the rock face in front of you spans more time than the human mind evolved to imagine.",
			"Geologists call the feeling deep time vertigo, and it has a curious side effect: it makes people kinder.") +
			para("Studies of awe consistently find that vastness — temporal or spatial — shrinks the self, and a smaller self is more generous, more patient, more honest about uncertainty.") +
			"<blockquote>Deep time is not a fact you learn. It is a place you visit, briefly, and return from changed.</blockquote>",
	})
	s.add(&entryRec{
		Title: "The Art of the Commit Message", URL: "https://dev.example.io/art-of-the-commit-message", DomainName: "dev.example.io",
		CreatedAt: at(15), ReadingTime: 8, IsArchived: 1,
		Tags: mkTags("programming"),
		Content: para("A commit message is a letter to a stranger who has your job in two years. Most letters say “fix”.") +
			"<h2>The shape of a good message</h2>" +
			"<pre><code>Summarize the change in 50 characters\n\nExplain what was wrong, why this approach,\nand what a reviewer should look at first.</code></pre>" +
			para("The summary line is a headline, not a description: imperative mood, no period, specific enough to be searchable.",
				"The body answers the only question that matters later — <em>why</em> — because the diff already answers <em>what</em>.") +
			para("Write the message before you write the code, and it becomes a plan. Write it after, and it becomes an apology."),
	})
	s.add(&entryRec{
		Title: "Migration Patterns of the Arctic Tern", URL: "https://birds.example.net/arctic-tern-migration", DomainName: "birds.example.net",
		CreatedAt: at(20), ReadingTime: 10, IsArchived: 1, IsStarred: 1, PreviewPicture: "https://picsum.photos/seed/pouch9/640/400",
		Tags: mkTags("science"),
		Content: para("The Arctic tern commutes between the poles: roughly seventy thousand kilometres a year, chasing an endless summer it never gets to keep.") +
			para("Over a thirty-year life, a single tern flies the equivalent of three round trips to the Moon, navigating by sun, stars, and a magnetic sense we still cannot fully explain.") +
			para("Tracking studies revealed the route is not a straight line but a vast S-curve across the Atlantic, drawn by wind systems the birds read like a map of moving sidewalks.") +
			"<blockquote>No animal on Earth sees more daylight than the Arctic tern.</blockquote>",
	})
}

type server struct{ st *store }

func (sv *server) token(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"access_token": "mock-access-token", "expires_in": 3600,
		"refresh_token": "mock-refresh-token", "token_type": "bearer", "scope": nil,
	})
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func (sv *server) listEntries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	archive := atoiDefault(q.Get("archive"), -1)
	starred := atoiDefault(q.Get("starred"), -1)
	page := atoiDefault(q.Get("page"), 1)
	perPage := atoiDefault(q.Get("perPage"), 30)
	tagFilter := q.Get("tags")

	sv.st.mu.Lock()
	var filtered []*entryRec
	for _, e := range sv.st.items {
		if archive >= 0 && e.IsArchived != archive {
			continue
		}
		if starred >= 0 && e.IsStarred != starred {
			continue
		}
		if tagFilter != "" {
			found := false
			for _, want := range strings.Split(tagFilter, ",") {
				for _, t := range e.Tags {
					if strings.EqualFold(t.Label, want) || strings.EqualFold(t.Slug, want) {
						found = true
					}
				}
			}
			if !found {
				continue
			}
		}
		filtered = append(filtered, e)
	}
	sv.st.mu.Unlock()

	sort.Slice(filtered, func(i, j int) bool { return filtered[i].CreatedAt > filtered[j].CreatedAt })
	total := len(filtered)
	pages := (total + perPage - 1) / perPage
	if pages == 0 {
		pages = 1
	}
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	writeJSON(w, 200, map[string]any{
		"page": page, "limit": perPage, "pages": pages, "total": total,
		"_links":    map[string]any{},
		"_embedded": map[string]any{"items": filtered[start:end]},
	})
}

func (sv *server) createEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Tags string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		http.Error(w, "bad request", 400)
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Host == "" {
		http.Error(w, "bad url", 400)
		return
	}
	time.Sleep(350 * time.Millisecond) // simulate wallabag fetching the page
	title := strings.Trim(strings.ReplaceAll(strings.Trim(u.Path, "/"), "-", " "), " ")
	if title == "" {
		title = u.Host
	} else if len(title) > 1 {
		title = strings.ToUpper(title[:1]) + title[1:]
	}
	var tags []tag
	for i, l := range strings.Split(req.Tags, ",") {
		if l = strings.TrimSpace(l); l != "" {
			tags = append(tags, tag{ID: 100 + i, Label: l, Slug: strings.ToLower(l)})
		}
	}
	e := sv.st.add(&entryRec{
		Title: title, URL: req.URL, DomainName: u.Host,
		CreatedAt: time.Now().Format(timeLayout), ReadingTime: 4, Tags: tags,
		Content: "<p>This entry was saved through the mock Wallabag server, which pretends it fetched and cleaned <a href=\"" +
			req.URL + "\">" + req.URL + "</a>. A real Wallabag would show the article's readable content here.</p>",
	})
	writeJSON(w, 200, e)
}

func (sv *server) entryByID(w http.ResponseWriter, r *http.Request) {
	id := atoiDefault(strings.TrimSuffix(r.PathValue("id"), ".json"), 0)
	e := sv.st.get(id)
	if e == nil {
		http.Error(w, "not found", 404)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, e)
	case http.MethodPatch:
		var req struct {
			Archive *int    `json:"archive"`
			Starred *int    `json:"starred"`
			Title   *string `json:"title"`
			Tags    *string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		sv.st.mu.Lock()
		if req.Archive != nil {
			e.IsArchived = *req.Archive
		}
		if req.Starred != nil {
			e.IsStarred = *req.Starred
		}
		if req.Title != nil {
			e.Title = *req.Title
		}
		e.UpdatedAt = time.Now().Format(timeLayout)
		sv.st.mu.Unlock()
		writeJSON(w, 200, e)
	case http.MethodDelete:
		sv.st.delete(id)
		writeJSON(w, 200, e)
	}
}

func (sv *server) tags(w http.ResponseWriter, r *http.Request) {
	sv.st.mu.Lock()
	defer sv.st.mu.Unlock()
	seen := map[string]tag{}
	for _, e := range sv.st.items {
		for _, t := range e.Tags {
			seen[t.Slug] = t
		}
	}
	list := make([]tag, 0, len(seen))
	for _, t := range seen {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Label < list[j].Label })
	writeJSON(w, 200, list)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	flag.Parse()

	st := &store{}
	seed(st)
	sv := &server{st: st}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/v2/token", sv.token)
	mux.HandleFunc("GET /api/entries.json", sv.listEntries)
	mux.HandleFunc("POST /api/entries.json", sv.createEntry)
	mux.HandleFunc("/api/entries/{id}", sv.entryByID)
	mux.HandleFunc("GET /api/tags", sv.tags)
	mux.HandleFunc("GET /api/tags.json", sv.tags)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("mockwallabag: unhandled %s %s", r.Method, r.URL)
		http.Error(w, fmt.Sprintf("mockwallabag: no route for %s %s", r.Method, r.URL.Path), 404)
	})

	log.Printf("mock wallabag listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
