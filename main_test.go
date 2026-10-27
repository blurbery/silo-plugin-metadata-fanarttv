package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/blurbery/silo-plugin-metadata-fanarttv/fanart"
)

type recordedLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordedLog) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *recordedLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

// newTestServer serves fanart.tv's /movies and /tv routes from the fixtures
// and records the paths it was asked for.
func newTestServer(t *testing.T, status *atomic.Int32) (*metadataServer, *[]string, *recordedLog) {
	t.Helper()
	movie, err := os.ReadFile("fanart/testdata/movie.json")
	if err != nil {
		t.Fatal(err)
	}
	show, err := os.ReadFile("fanart/testdata/show.json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if status != nil && status.Load() != 0 {
			w.WriteHeader(int(status.Load()))
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/movies/"):
			_, _ = w.Write(movie)
		case strings.HasPrefix(r.URL.Path, "/tv/"):
			_, _ = w.Write(show)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := fanart.NewClient()
	client.SetBaseURL(server.URL)
	client.SetKeys("project-key", "")
	logs := &recordedLog{}
	reporter := newFailureReporter()
	reporter.logf = logs.logf
	return &metadataServer{runtime: &runtimeServer{client: client}, reporter: reporter}, &paths, logs
}

func providerIDs(t *testing.T, ids map[string]any) *structpb.Struct {
	t.Helper()
	value, err := structpb.NewStruct(ids)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestGetImagesMovieLooksUpByTMDBThenIMDb(t *testing.T) {
	server, paths, _ := newTestServer(t, nil)

	response, err := server.GetImages(context.Background(), &pluginv1.GetImagesRequest{
		ItemType:    "movie",
		ProviderIds: providerIDs(t, map[string]any{"tmdb": "550", "imdb": "tt0137523"}),
	})
	if err != nil {
		t.Fatalf("GetImages() error = %v", err)
	}
	if len(response.GetImages()) == 0 {
		t.Fatal("GetImages() returned no images for a known movie")
	}
	if _, err := server.GetImages(context.Background(), &pluginv1.GetImagesRequest{
		ItemType:    "movie",
		ProviderIds: providerIDs(t, map[string]any{"IMDB": "tt0137523"}),
	}); err != nil {
		t.Fatalf("GetImages() error = %v", err)
	}
	if got := strings.Join(*paths, ","); got != "/movies/550,/movies/tt0137523" {
		t.Fatalf("requested %q, want the TMDB ID then the IMDb ID", got)
	}

	var poster *pluginv1.ImageRecord
	for _, image := range response.GetImages() {
		if strings.HasSuffix(image.GetUrl(), "example-poster-en.jpg") {
			poster = image
		}
	}
	if poster == nil {
		t.Fatalf("no titled poster among %v", response.GetImages())
	}
	fields := poster.GetMetadata().GetFields()
	if poster.GetKind() != "poster" || poster.GetLanguage() != "en" || poster.SeasonNumber != nil {
		t.Errorf("poster = %v", poster)
	}
	if fields["rating"].GetNumberValue() <= 0 || !fields["includes_text"].GetBoolValue() {
		t.Errorf("poster metadata = %v, want a positive rating and includes_text", fields)
	}
}

func TestGetImagesSeriesUsesTVDBAndSeasonScope(t *testing.T) {
	server, paths, _ := newTestServer(t, nil)
	ids := providerIDs(t, map[string]any{"tmdb": "1399", "tvdb": "81189"})

	show, err := server.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "series", ProviderIds: ids})
	if err != nil {
		t.Fatalf("GetImages(show) error = %v", err)
	}
	for _, image := range show.GetImages() {
		if image.SeasonNumber != nil {
			t.Errorf("show-level answer carries season art %v", image)
		}
	}

	season := int32(1)
	scoped, err := server.GetImages(context.Background(), &pluginv1.GetImagesRequest{
		ItemType: "series", ProviderIds: ids, SeasonNumber: &season,
	})
	if err != nil {
		t.Fatalf("GetImages(season) error = %v", err)
	}
	if len(scoped.GetImages()) != 1 || scoped.GetImages()[0].GetSeasonNumber() != 1 || scoped.GetImages()[0].SeasonNumber == nil {
		t.Fatalf("season 1 = %v, want its one poster echoing the season", scoped.GetImages())
	}
	if got := strings.Join(*paths, ","); got != "/tv/81189" {
		t.Fatalf("requested %q, want one TVDB lookup served from the cache after", got)
	}
}

func TestGetImagesWithoutUsableIDAnswersEmpty(t *testing.T) {
	server, paths, _ := newTestServer(t, nil)
	cases := []*pluginv1.GetImagesRequest{
		{ItemType: "series", ProviderIds: providerIDs(t, map[string]any{"tmdb": "1399"})},
		{ItemType: "movie", ProviderIds: providerIDs(t, map[string]any{"tvdb": "81189"})},
		{ItemType: "audiobook", ProviderIds: providerIDs(t, map[string]any{"tmdb": "550"})},
	}
	for _, req := range cases {
		response, err := server.GetImages(context.Background(), req)
		if err != nil || len(response.GetImages()) != 0 {
			t.Errorf("GetImages(%v) = %v, %v, want an empty answer", req, response, err)
		}
	}
	if len(*paths) != 0 {
		t.Fatalf("made requests %v without a usable ID", *paths)
	}
}

func TestGetImagesFailuresAnswerEmptyAndLogOnce(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusUnauthorized)
	server, _, logs := newTestServer(t, &status)
	ids := providerIDs(t, map[string]any{"tmdb": "550"})

	for range 3 {
		response, err := server.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "movie", ProviderIds: ids})
		if err != nil || len(response.GetImages()) != 0 {
			t.Fatalf("GetImages() = %v, %v; a failure must answer empty, not fail the refresh", response, err)
		}
	}
	if logs.count() != 1 {
		t.Fatalf("logged %d lines for one ongoing failure, want 1: %v", logs.count(), logs.lines)
	}
	if strings.Contains(logs.lines[0], "project-key") {
		t.Errorf("log line %q repeats the API key", logs.lines[0])
	}

	status.Store(0)
	response, err := server.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "movie", ProviderIds: ids})
	if err != nil || len(response.GetImages()) == 0 {
		t.Fatalf("GetImages() after recovery = %v, %v", response, err)
	}
	if logs.count() != 2 || !strings.Contains(logs.lines[1], "answering again") {
		t.Fatalf("log after recovery = %v, want one recovery line", logs.lines)
	}
}

func TestFailureReporterRepeatsAfterQuietPeriod(t *testing.T) {
	logs := &recordedLog{}
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	reporter := &failureReporter{now: func() time.Time { return now }, logf: logs.logf}

	reporter.report(fanart.ErrNotConfigured)
	reporter.report(fanart.ErrNotConfigured)
	now = now.Add(repeatReportAfter)
	reporter.report(fanart.ErrNotConfigured)
	reporter.report(fmt.Errorf("%w: HTTP 503", fanart.ErrUnavailable))
	reporter.report(context.Canceled)

	if logs.count() != 3 {
		t.Fatalf("logged %v, want the first report, the repeat after the quiet period, and the new failure", logs.lines)
	}
}

func TestGetMetadataAndSearchMakeNoRequest(t *testing.T) {
	server, paths, _ := newTestServer(t, nil)
	ids := providerIDs(t, map[string]any{"tmdb": "550"})

	metadata, err := server.GetMetadata(context.Background(), &pluginv1.GetMetadataRequest{ItemType: "movie", ProviderIds: ids})
	if err != nil || metadata.GetItem() != nil {
		t.Fatalf("GetMetadata() = %v, %v, want an empty answer", metadata, err)
	}
	search, err := server.Search(context.Background(), &pluginv1.SearchMetadataRequest{Query: "Example", ItemType: "movie"})
	if err != nil || len(search.GetResults()) != 0 {
		t.Fatalf("Search() = %v, %v, want no results", search, err)
	}
	seasons, err := server.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{ProviderIds: ids})
	if err != nil || len(seasons.GetSeasons()) != 0 {
		t.Fatalf("GetSeasons() = %v, %v, want no seasons", seasons, err)
	}
	if len(*paths) != 0 {
		t.Fatalf("made requests %v; only GetImages may call fanart.tv", *paths)
	}
}

func TestKeysFromConfig(t *testing.T) {
	account, err := structpb.NewStruct(map[string]any{"api_key": " project ", "client_key": " personal "})
	if err != nil {
		t.Fatal(err)
	}
	other, err := structpb.NewStruct(map[string]any{"api_key": "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	apiKey, clientKey := keysFromConfig([]*pluginv1.ConfigEntry{
		nil,
		{Key: "other", Value: other},
		{Key: "account", Value: account},
	})
	if apiKey != "project" || clientKey != "personal" {
		t.Fatalf("keysFromConfig() = %q, %q", apiKey, clientKey)
	}
	if apiKey, clientKey := keysFromConfig(nil); apiKey != "" || clientKey != "" {
		t.Fatalf("keysFromConfig(nil) = %q, %q, want empty", apiKey, clientKey)
	}
}

func TestManifestDeclaresArtworkLookup(t *testing.T) {
	manifest, err := publicmanifest.Load(manifestJSON)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if err := publicmanifest.Validate(manifest); err != nil {
		t.Fatalf("validate manifest: %v", err)
	}
	if len(manifest.GetCapabilities()) != 1 {
		t.Fatalf("capabilities = %v, want one metadata provider", manifest.GetCapabilities())
	}
	capability := manifest.GetCapabilities()[0]
	if capability.GetType() != "metadata_provider.v1" || capability.GetId() != "fanarttv" {
		t.Fatalf("capability = %s/%s", capability.GetType(), capability.GetId())
	}
	metadata := capability.GetMetadata().AsMap()
	if metadata["lookup_images"] != true {
		t.Errorf("lookup_images = %v; without it the host never asks for artwork", metadata["lookup_images"])
	}
	lookup, _ := metadata["lookup_provider_ids"].([]any)
	if fmt.Sprint(lookup) != "[tmdb imdb tvdb]" {
		t.Errorf("lookup_provider_ids = %v", lookup)
	}
	if _, bulk := metadata["bulk_lookup_limit"]; bulk {
		t.Error("bulk_lookup_limit would put an artwork source into the ratings enrichment pass")
	}
	priorities, _ := metadata["default_priority"].(map[string]any)
	for _, level := range []string{"movie", "series", "season"} {
		if priorities[level] == nil {
			t.Errorf("default_priority has no %s level", level)
		}
	}
}
