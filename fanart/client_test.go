package fanart

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client := NewClient()
	client.SetBaseURL(server.URL)
	client.SetKeys("project-key", "")
	return client, &requests
}

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
}

func TestClientSendsKeysAsHeaders(t *testing.T) {
	var got *http.Request
	fixture := serveFixture(t, "movie.json")
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		fixture(w, r)
	})
	client.SetKeys(" project-key ", " personal-key ")
	client.SetUserAgent("silo-plugin-metadata-fanarttv/test")

	artwork, err := client.Movie(context.Background(), "550")
	if err != nil {
		t.Fatalf("Movie() error = %v", err)
	}
	if artwork == nil || len(artwork.MoviePoster) == 0 {
		t.Fatalf("Movie() = %+v, want the fixture's artwork", artwork)
	}
	if got.URL.Path != "/movies/550" {
		t.Errorf("path = %q, want /movies/550", got.URL.Path)
	}
	if got.URL.RawQuery != "" {
		t.Errorf("query = %q; keys must not travel in the URL", got.URL.RawQuery)
	}
	if got.Header.Get("api-key") != "project-key" || got.Header.Get("client-key") != "personal-key" {
		t.Errorf("key headers = %q / %q", got.Header.Get("api-key"), got.Header.Get("client-key"))
	}
	if got.Header.Get("User-Agent") != "silo-plugin-metadata-fanarttv/test" {
		t.Errorf("User-Agent = %q", got.Header.Get("User-Agent"))
	}
}

func TestClientShowsUseTVSection(t *testing.T) {
	var path string
	fixture := serveFixture(t, "show.json")
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		fixture(w, r)
	})
	if _, err := client.Show(context.Background(), "81189"); err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if path != "/tv/81189" {
		t.Errorf("path = %q, want /tv/81189", path)
	}
}

func TestClientWithoutKeyMakesNoRequest(t *testing.T) {
	client, requests := newTestClient(t, serveFixture(t, "movie.json"))
	client.SetKeys("", "personal-key")

	if _, err := client.Movie(context.Background(), "550"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Movie() error = %v, want ErrNotConfigured", err)
	}
	if requests.Load() != 0 {
		t.Errorf("made %d requests without an API key", requests.Load())
	}
}

func TestClientEmptyIDMakesNoRequest(t *testing.T) {
	client, requests := newTestClient(t, serveFixture(t, "movie.json"))
	artwork, err := client.Show(context.Background(), "  ")
	if artwork != nil || err != nil || requests.Load() != 0 {
		t.Fatalf("Show(blank) = %v, %v after %d requests, want nothing", artwork, err, requests.Load())
	}
}

func TestClientStatusMapping(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{"unknown title", http.StatusNotFound, `{"status":"error","error message":"Not found"}`, nil},
		{"rejected key", http.StatusUnauthorized, `{}`, ErrKeyRejected},
		{"forbidden", http.StatusForbidden, `{}`, ErrKeyRejected},
		{"rate limited", http.StatusTooManyRequests, `{}`, ErrUnavailable},
		{"outage", http.StatusBadGateway, `{}`, ErrUnavailable},
		{"bad request", http.StatusBadRequest, `{}`, ErrUnusableAnswer},
		{"garbled", http.StatusOK, `{"movieposter": "not a list"`, ErrUnusableAnswer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			artwork, err := client.Movie(context.Background(), "550")
			if artwork != nil {
				t.Errorf("Movie() = %+v, want nil", artwork)
			}
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Movie() error = %v, want none for an unknown title", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Movie() error = %v, want %v", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "project-key") {
				t.Errorf("error %q repeats the API key", err)
			}
		})
	}
}

func TestClientCachesAnswers(t *testing.T) {
	client, requests := newTestClient(t, serveFixture(t, "movie.json"))
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return now }

	for range 3 {
		if _, err := client.Movie(context.Background(), "550"); err != nil {
			t.Fatalf("Movie() error = %v", err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("made %d requests for one title, want 1", requests.Load())
	}

	now = now.Add(cacheTTL)
	if _, err := client.Movie(context.Background(), "550"); err != nil {
		t.Fatalf("Movie() error = %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("made %d requests after the cache expired, want 2", requests.Load())
	}

	client.SetKeys("project-key", "personal-key")
	if _, err := client.Movie(context.Background(), "550"); err != nil {
		t.Fatalf("Movie() error = %v", err)
	}
	if requests.Load() != 3 {
		t.Fatalf("made %d requests after the keys changed, want 3", requests.Load())
	}
}

func TestClientCachesUnknownTitles(t *testing.T) {
	client, requests := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	for range 2 {
		if artwork, err := client.Show(context.Background(), "1"); artwork != nil || err != nil {
			t.Fatalf("Show() = %v, %v, want nothing", artwork, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("made %d requests for one unknown title, want 1", requests.Load())
	}
}

func TestClientDoesNotCacheFailures(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	fixture := serveFixture(t, "movie.json")
	client, requests := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fixture(w, r)
	})
	if _, err := client.Movie(context.Background(), "550"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Movie() error = %v, want ErrUnavailable", err)
	}
	fail.Store(false)
	artwork, err := client.Movie(context.Background(), "550")
	if err != nil || artwork == nil {
		t.Fatalf("Movie() after the outage = %v, %v, want artwork", artwork, err)
	}
	if requests.Load() != 2 {
		t.Fatalf("made %d requests, want 2", requests.Load())
	}
}

func TestClientCacheStaysBounded(t *testing.T) {
	client := NewClient()
	for i := range maxCacheEntries + 10 {
		client.store("movies/"+strconv.Itoa(i), nil)
	}
	if len(client.cache) > maxCacheEntries {
		t.Fatalf("cache holds %d entries, cap is %d", len(client.cache), maxCacheEntries)
	}
}
