package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *wallabagClient {
	t.Helper()
	upstream := httptest.NewServer(h)
	t.Cleanup(upstream.Close)
	c := newWallabagClient(Config{WallabagURL: upstream.URL, WallabagClientID: "client", WallabagClientSecret: "secret", WallabagUsername: "user", WallabagPassword: "password"})
	c.token = oauthToken{AccessToken: "expired", RefreshToken: "rejected"}
	c.expires = time.Now().Add(-time.Hour)
	return c
}

func TestRecoveryAllRoutesConcurrent(t *testing.T) {
	var refresh, password, api atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/v2/token" {
			r.ParseForm()
			switch r.Form.Get("grant_type") {
			case "refresh_token":
				refresh.Add(1)
				if r.Form.Get("refresh_token") != "rejected" {
					t.Error("wrong refresh token")
				}
				w.WriteHeader(400)
			case "password":
				password.Add(1)
				if r.Form.Get("password") != "password" || r.Form.Get("client_secret") != "secret" {
					t.Error("missing credentials")
				}
				fmt.Fprint(w, `{"access_token":"new","refresh_token":"rotated","expires_in":3600}`)
			default:
				t.Error("unexpected grant")
			}
			return
		}
		api.Add(1)
		if r.Header.Get("Authorization") != "Bearer new" {
			t.Error("API used stale token")
		}
		switch {
		case r.URL.Path == "/api/tags":
			fmt.Fprint(w, `[]`)
		case r.Method == "GET" && r.URL.Path == "/api/entries.json":
			if r.URL.Query().Get("detail") != "metadata" || r.URL.Query().Get("archive") != "0" {
				t.Error("lost list filters")
			}
			fmt.Fprint(w, `{"page":1,"pages":1,"total":0,"_embedded":{"items":[]}}`)
		case r.Method == "DELETE":
			w.WriteHeader(204)
		default:
			fmt.Fprint(w, `{"id":1,"title":"Article","content":"Full content"}`)
		}
	})
	s := &Server{wallabag: c, progress: newProgressStore(t.TempDir()), notes: newNotesStore(t.TempDir())}
	routes := []struct {
		method, path, body string
		handler            http.HandlerFunc
		status             int
	}{
		{"GET", "/api/entries", "", s.handleListEntries, 200},
		{"GET", "/api/entries/1", "", s.handleGetEntry, 200},
		{"GET", "/api/tags", "", s.handleTags, 200},
		{"POST", "/api/entries", `{"url":"https://example.com"}`, s.handleCreateEntry, 201},
		{"PATCH", "/api/entries/1", `{"starred":1}`, s.handlePatchEntry, 200},
		{"DELETE", "/api/entries/1", "", s.handleDeleteEntry, 204},
		{"GET", "/api/quick-add?url=https://example.com", "", s.handleQuickAdd, 200},
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, route := range routes {
		wg.Go(func() {
			<-start
			r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			r.SetPathValue("id", "1")
			w := httptest.NewRecorder()
			route.handler(w, r)
			if w.Code != route.status {
				t.Errorf("%s %s: %d %s", route.method, route.path, w.Code, w.Body.String())
			}
		})
	}
	close(start)
	wg.Wait()
	if refresh.Load() != 1 || password.Load() != 1 || api.Load() != int32(len(routes)) {
		t.Fatalf("refresh/password/api: %d/%d/%d", refresh.Load(), password.Load(), api.Load())
	}
	if c.token.RefreshToken != "rotated" {
		t.Fatal("refresh rotation lost")
	}
}

func TestAuthenticationFailuresBoundedAndRecoverable(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var refresh, password, api atomic.Int32
			var healthy atomic.Bool
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/oauth/v2/token" {
					api.Add(1)
					fmt.Fprint(w, `{}`)
					return
				}
				r.ParseForm()
				if r.Form.Get("grant_type") == "refresh_token" {
					refresh.Add(1)
				} else {
					password.Add(1)
				}
				if healthy.Load() {
					fmt.Fprint(w, `{"access_token":"ok","expires_in":3600}`)
					return
				}
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error_description":"secret password rejected"}`)
			})
			var wg sync.WaitGroup
			for range 20 {
				wg.Go(func() {
					_, err := c.apiCall(context.Background(), "/api/entries.json", "POST", []byte(`{}`))
					if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "password rejected") {
						t.Errorf("unsafe error: %v", err)
					}
				})
			}
			wg.Wait()
			wantPassword := int32(0)
			if status == 400 || status == 401 {
				wantPassword = 1
			}
			if refresh.Load() != 1 || password.Load() != wantPassword || api.Load() != 0 {
				t.Fatalf("unbounded attempts: %d/%d/%d", refresh.Load(), password.Load(), api.Load())
			}
			healthy.Store(true)
			c.retryAfter = time.Time{} // advance past cooldown without sleeping
			if _, err := c.apiCall(context.Background(), "/api/entries.json", "POST", nil); err != nil {
				t.Fatal(err)
			}
			if api.Load() != 1 {
				t.Fatal("mutation not sent exactly once")
			}
			if wantPassword == 1 && refresh.Load() != 1 {
				t.Fatal("retried rejected refresh token")
			}
		})
	}
}

func TestAPIRequestsNeverRetried(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
		for _, status := range []int{401, 400, 500, 307} {
			t.Run(fmt.Sprintf("%s/%d", method, status), func(t *testing.T) {
				var calls atomic.Int32
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Location", "/redirect")
					w.WriteHeader(status)
				})
				c.expires = time.Now().Add(time.Hour)
				if _, err := c.apiCall(context.Background(), "/api/entries.json", method, []byte(`{}`)); err == nil {
					t.Fatal("expected error")
				}
				if calls.Load() != 1 {
					t.Fatalf("request replayed %d times", calls.Load())
				}
			})
		}
	}
}

func TestRefreshSuccessAndInvalidResponses(t *testing.T) {
	for _, body := range []string{`{"access_token":"fresh","expires_in":3600}`, `{}`, `not json`, `{"access_token":"fresh","expires_in":0}`} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, body) })
			token, err := c.auth(context.Background())
			if strings.Contains(body, "3600") {
				if err != nil || token != "fresh" || c.token.RefreshToken != "rejected" {
					t.Fatalf("refresh failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("accepted invalid token")
			}
			if calls.Load() != 1 {
				t.Fatal("unexpected password fallback")
			}
		})
	}
}

func TestClientsIsolateTokens(t *testing.T) {
	for _, value := range []string{"first", "second"} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"access_token":%q,"expires_in":3600}`, value)
		})
		c.token = oauthToken{}
		if token, err := c.auth(context.Background()); err != nil || token != value {
			t.Fatalf("token isolation failed: %v", err)
		}
	}
}

// A connection can disappear after Wallabag has applied a mutation. Do not
// interpret that ambiguous failure as permission to authenticate and replay it.
func TestMutationTransportFailureNotRetried(t *testing.T) {
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			var calls atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
			})
			c.expires = time.Now().Add(time.Hour)
			if _, err := c.apiCall(context.Background(), "/api/entries.json", method, []byte(`{}`)); err == nil {
				t.Fatal("expected transport failure")
			}
			if calls.Load() != 1 {
				t.Fatalf("mutation sent %d times", calls.Load())
			}
		})
	}
}

func TestTokenTransportFailureAndCancellation(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.auth(ctx); err == nil {
		t.Fatal("ignored cancellation")
	}
	if calls.Load() != 0 {
		t.Fatal("sent canceled request")
	}
	if _, err := c.auth(context.Background()); err == nil {
		t.Fatal("expected transport failure")
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected password fallback or cancellation cooldown")
	}
	if c.token.RefreshToken != "rejected" {
		t.Fatal("discarded token on transient error")
	}
}
