// Package fanart talks to the fanart.tv v3 API and turns its answers into
// artwork records in Silo's terms.
package fanart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// defaultBaseURL is the fanart.tv API root. Tests override it with SetBaseURL.
const defaultBaseURL = "https://webservice.fanart.tv/v3"

// defaultUserAgent identifies the plugin to fanart.tv; main.go appends the
// build version.
const defaultUserAgent = "silo-plugin-metadata-fanarttv"

const (
	defaultTimeout = 15 * time.Second
	// maxResponseBody bounds one title's answer. A long-running show with
	// hundreds of season posters is still well under 1 MB.
	maxResponseBody = 4 << 20
	// cacheTTL keeps an answer long enough that one refresh, and an
	// administrator paging through a show's seasons in the image picker, cost
	// one request per title. fanart.tv asks API users to cache.
	cacheTTL = 30 * time.Minute
	// maxCacheEntries bounds the cache during a full library refresh.
	maxCacheEntries = 2000
)

// Lookup failures. Each wraps one of these, so match them with errors.Is. A
// title fanart.tv does not know is not an error: it resolves to (nil, nil).
var (
	// ErrNotConfigured means no API key is set.
	ErrNotConfigured = errors.New("fanart.tv: no API key configured")
	// ErrKeyRejected means fanart.tv refused the API key or client key.
	ErrKeyRejected = errors.New("fanart.tv: API key rejected")
	// ErrUnavailable means fanart.tv could not be reached, was rate limiting,
	// or failed to answer.
	ErrUnavailable = errors.New("fanart.tv: service unavailable")
	// ErrUnusableAnswer means fanart.tv answered this title with something
	// that could not be read.
	ErrUnusableAnswer = errors.New("fanart.tv: unusable answer")
)

// Client fetches artwork from fanart.tv.
//
// The keys travel as the api-key and client-key headers rather than in the
// query string, so they never appear in a URL that ends up in an error or a
// log line.
type Client struct {
	httpClient *http.Client
	now        func() time.Time

	mu        sync.RWMutex
	apiKey    string
	clientKey string
	baseURL   string
	userAgent string

	cacheMu sync.Mutex
	cache   map[string]cacheEntry
}

type cacheEntry struct {
	// artwork is nil for a title fanart.tv does not know.
	artwork *Artwork
	expires time.Time
}

// NewClient builds a client with no keys; the plugin's Configure supplies them.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		now:        time.Now,
		baseURL:    defaultBaseURL,
		userAgent:  defaultUserAgent,
		cache:      make(map[string]cacheEntry),
	}
}

// SetKeys replaces the project API key and the optional personal client key.
// Safe to call while requests are in flight. A change empties the cache: a
// personal key can see artwork a project key is still waiting on.
func (c *Client) SetKeys(apiKey, clientKey string) {
	apiKey = strings.TrimSpace(apiKey)
	clientKey = strings.TrimSpace(clientKey)
	c.mu.Lock()
	changed := apiKey != c.apiKey || clientKey != c.clientKey
	c.apiKey = apiKey
	c.clientKey = clientKey
	c.mu.Unlock()
	if changed {
		c.cacheMu.Lock()
		c.cache = make(map[string]cacheEntry)
		c.cacheMu.Unlock()
	}
}

// SetBaseURL overrides the API root. Used by tests.
func (c *Client) SetBaseURL(baseURL string) {
	c.mu.Lock()
	c.baseURL = strings.TrimRight(baseURL, "/")
	c.mu.Unlock()
}

// SetUserAgent sets the User-Agent sent with every request.
func (c *Client) SetUserAgent(userAgent string) {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	c.mu.Lock()
	c.userAgent = userAgent
	c.mu.Unlock()
}

// Movie fetches a movie's artwork by its TMDB ID or IMDb ID.
func (c *Client) Movie(ctx context.Context, id string) (*Artwork, error) {
	return c.fetch(ctx, "movies", id)
}

// Show fetches a series' artwork by its TVDB ID, the only ID fanart.tv files
// shows under.
func (c *Client) Show(ctx context.Context, tvdbID string) (*Artwork, error) {
	return c.fetch(ctx, "tv", tvdbID)
}

func (c *Client) fetch(ctx context.Context, section, id string) (*Artwork, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	c.mu.RLock()
	apiKey, clientKey, baseURL, userAgent := c.apiKey, c.clientKey, c.baseURL, c.userAgent
	c.mu.RUnlock()
	if apiKey == "" {
		return nil, ErrNotConfigured
	}

	key := section + "/" + id
	if artwork, ok := c.cached(key); ok {
		return artwork, nil
	}

	endpoint := baseURL + "/" + section + "/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request for %s: %v", ErrUnusableAnswer, key, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("api-key", apiKey)
	if clientKey != "" {
		req.Header.Set("client-key", clientKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrUnavailable, key, transportError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrUnavailable, key, err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		c.store(key, nil)
		return nil, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrKeyRejected, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: %s: HTTP %d", ErrUnavailable, key, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: %s: HTTP %d", ErrUnusableAnswer, key, resp.StatusCode)
	}
	if len(body) > maxResponseBody {
		return nil, fmt.Errorf("%w: %s: answer larger than %d bytes", ErrUnusableAnswer, key, maxResponseBody)
	}

	var artwork Artwork
	if err := json.Unmarshal(body, &artwork); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnusableAnswer, key, err)
	}
	c.store(key, &artwork)
	return &artwork, nil
}

// transportError drops the request URL from a transport error, which repeats
// it as "Get \"https://...\": ...". Only the cause is useful in a log line.
func transportError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

func (c *Client) cached(key string) (*Artwork, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry, ok := c.cache[key]
	if !ok || !c.now().Before(entry.expires) {
		return nil, false
	}
	return entry.artwork, true
}

func (c *Client) store(key string, artwork *Artwork) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	now := c.now()
	if len(c.cache) >= maxCacheEntries {
		for k, entry := range c.cache {
			if !now.Before(entry.expires) {
				delete(c.cache, k)
			}
		}
		if len(c.cache) >= maxCacheEntries {
			// Every entry is still fresh, so this is one long refresh; starting
			// over costs a request per title, not correctness.
			c.cache = make(map[string]cacheEntry)
		}
	}
	c.cache[key] = cacheEntry{artwork: artwork, expires: now.Add(cacheTTL)}
}
