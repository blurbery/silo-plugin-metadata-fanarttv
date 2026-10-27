// Command plugin is the fanart.tv artwork provider for Silo.
//
// It supplies artwork only. It never identifies an item and never fills
// metadata: Search and GetMetadata answer empty, and GetImages looks titles up
// by the TMDB, IMDb or TVDB ID a primary provider such as TMDB resolved. The
// host asks it for images by those IDs because the manifest declares
// lookup_images.
package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/blurbery/silo-plugin-metadata-fanarttv/fanart"
)

// version is set at build time via -ldflags "-X main.version=...".
var version string

// builtinAPIKey is the fanart.tv project key, set at release time via
// -ldflags "-X main.builtinAPIKey=..." from the FANARTTV_API_KEY Actions
// secret, so the plugin works with no setup. A key in the plugin settings
// replaces it. Local and test builds have none.
var builtinAPIKey string

//go:embed manifest.json
var manifestJSON []byte

// repeatReportAfter is how long a provider-wide failure stays quiet in the log
// before it is reported again.
const repeatReportAfter = 15 * time.Minute

type runtimeServer struct {
	pluginv1.UnimplementedRuntimeServer

	manifest *pluginv1.PluginManifest
	client   *fanart.Client
}

type metadataServer struct {
	pluginv1.UnimplementedMetadataProviderServer
	runtime  *runtimeServer
	reporter *failureReporter
}

func (s *runtimeServer) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.manifest}, nil
}

func (s *runtimeServer) Configure(_ context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	apiKey, clientKey := keysFromConfig(req.GetConfig())
	if apiKey == "" {
		apiKey = builtinAPIKey
	}
	s.client.SetKeys(apiKey, clientKey)
	return &pluginv1.ConfigureResponse{}, nil
}

// GetImages returns fanart.tv's artwork for a movie, a series, or one season
// of a series, in the requested language, English or no language (see
// fanart.ForLanguage).
//
// Every failure answers empty rather than with an error. The host logs each
// image provider error once per item, so a missing key or an outage would
// otherwise flood a library refresh; the plugin logs it itself, once.
func (s *metadataServer) GetImages(ctx context.Context, req *pluginv1.GetImagesRequest) (*pluginv1.GetImagesResponse, error) {
	ids := stringMapFromStruct(req.GetProviderIds())
	client := s.runtime.client

	var records []fanart.Record
	switch mediaKind(req.GetItemType()) {
	case "movie":
		id := ids["tmdb"]
		if id == "" {
			id = ids["imdb"]
		}
		artwork, err := client.Movie(ctx, id)
		if err != nil {
			s.reporter.report(err)
			return &pluginv1.GetImagesResponse{}, nil
		}
		records = artwork.MovieRecords()
	case "show":
		artwork, err := client.Show(ctx, ids["tvdb"])
		if err != nil {
			s.reporter.report(err)
			return &pluginv1.GetImagesResponse{}, nil
		}
		if req.SeasonNumber != nil {
			records = artwork.SeasonRecords(int(req.GetSeasonNumber()))
		} else {
			records = artwork.ShowRecords()
		}
	default:
		return &pluginv1.GetImagesResponse{}, nil
	}
	s.reporter.recovered()
	records = fanart.ForLanguage(records, req.GetLanguage())

	response := &pluginv1.GetImagesResponse{Images: make([]*pluginv1.ImageRecord, 0, len(records))}
	for _, record := range records {
		response.Images = append(response.Images, imageRecord(record))
	}
	return response, nil
}

// Search always returns nothing. fanart.tv can only be asked by another
// provider's ID, and an artwork source must never compete for identity.
func (s *metadataServer) Search(context.Context, *pluginv1.SearchMetadataRequest) (*pluginv1.SearchMetadataResponse, error) {
	return &pluginv1.SearchMetadataResponse{}, nil
}

// GetMetadata answers empty without a request. The host calls it on every
// match and refresh because the manifest declares lookup_provider_ids, and
// fanart.tv has no metadata to give.
func (s *metadataServer) GetMetadata(context.Context, *pluginv1.GetMetadataRequest) (*pluginv1.GetMetadataResponse, error) {
	return &pluginv1.GetMetadataResponse{}, nil
}

// The remaining MetadataProvider RPCs exist because the host calls every
// method on every provider in a library's chain. Each answers empty and
// successfully, so the plugin adds no failure to a match or refresh. Season
// posters reach the host through season-scoped GetImages, not GetSeasons:
// season records from an artwork source would feed the host's season merge.

func (s *metadataServer) GetPersonDetail(context.Context, *pluginv1.GetPersonDetailRequest) (*pluginv1.GetPersonDetailResponse, error) {
	return &pluginv1.GetPersonDetailResponse{}, nil
}

func (s *metadataServer) GetSeasons(context.Context, *pluginv1.GetSeasonsRequest) (*pluginv1.GetSeasonsResponse, error) {
	return &pluginv1.GetSeasonsResponse{}, nil
}

func (s *metadataServer) GetEpisodes(context.Context, *pluginv1.GetEpisodesRequest) (*pluginv1.GetEpisodesResponse, error) {
	return &pluginv1.GetEpisodesResponse{}, nil
}

func (s *metadataServer) ResolveImageURL(context.Context, *pluginv1.ResolveImageURLRequest) (*pluginv1.ResolveImageURLResponse, error) {
	return &pluginv1.ResolveImageURLResponse{}, nil
}

func (s *metadataServer) ResolveImageURLs(context.Context, *pluginv1.ResolveImageURLsRequest) (*pluginv1.ResolveImageURLsResponse, error) {
	return &pluginv1.ResolveImageURLsResponse{}, nil
}

func main() {
	manifest, err := loadManifest()
	if err != nil {
		panic(err)
	}

	rs := &runtimeServer{
		manifest: manifest,
		client:   newClient(),
	}

	runtime.Serve(runtime.ServeConfig{
		Servers: runtime.CapabilityServers{
			Runtime:          rs,
			MetadataProvider: &metadataServer{runtime: rs, reporter: newFailureReporter()},
		},
	})
}

// newClient builds the fanart.tv client with a User-Agent naming this build.
func newClient() *fanart.Client {
	client := fanart.NewClient()
	userAgent := "silo-plugin-metadata-fanarttv"
	if version != "" {
		userAgent += "/" + version
	}
	client.SetUserAgent(userAgent)
	client.SetKeys(builtinAPIKey, "")
	return client
}

func loadManifest() (*pluginv1.PluginManifest, error) {
	manifest, err := publicmanifest.Load(manifestJSON)
	if err != nil {
		return nil, fmt.Errorf("load embedded manifest: %w", err)
	}

	if version != "" {
		manifest.Version = version
	}

	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	binaryData, err := os.ReadFile(executablePath)
	if err != nil {
		return nil, fmt.Errorf("read executable %q: %w", executablePath, err)
	}
	checksum := sha256.Sum256(binaryData)
	manifest.Checksum = hex.EncodeToString(checksum[:])

	return manifest, nil
}

// keysFromConfig reads the "account" config entry declared in manifest.json's
// global_config_schema.
func keysFromConfig(entries []*pluginv1.ConfigEntry) (apiKey, clientKey string) {
	for _, entry := range entries {
		if entry == nil || entry.GetKey() != "account" || entry.GetValue() == nil {
			continue
		}
		values := entry.GetValue().AsMap()
		apiKey, _ = values["api_key"].(string)
		clientKey, _ = values["client_key"].(string)
		return strings.TrimSpace(apiKey), strings.TrimSpace(clientKey)
	}
	return "", ""
}

// mediaKind maps the host's item type to the fanart.tv section it lives in.
func mediaKind(itemType string) string {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case "movie", "movies":
		return "movie"
	case "series", "show", "tv":
		return "show"
	default:
		return ""
	}
}

// imageRecord builds the wire record. The host reads "rating" and
// "includes_text" from the metadata struct.
func imageRecord(record fanart.Record) *pluginv1.ImageRecord {
	values := map[string]any{"rating": record.Rating}
	if record.IncludesText != nil {
		values["includes_text"] = *record.IncludesText
	}
	out := &pluginv1.ImageRecord{
		Kind:     record.Kind,
		Url:      record.URL,
		Language: record.Language,
		Metadata: structFromMap(values),
	}
	if record.Season != nil {
		season := int32(*record.Season)
		out.SeasonNumber = &season
	}
	return out
}

// stringMapFromStruct reads the external IDs the primary provider resolved.
// Keys are normalised so a host or client that sent "TMDB" still works.
func stringMapFromStruct(value *structpb.Struct) map[string]string {
	result := make(map[string]string)
	if value == nil {
		return result
	}
	for key, raw := range value.AsMap() {
		text, ok := raw.(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		result[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(text)
	}
	return result
}

func structFromMap(value map[string]any) *structpb.Struct {
	if len(value) == 0 {
		return nil
	}
	result, err := structpb.NewStruct(value)
	if err != nil {
		return nil
	}
	return result
}

// failureReporter logs a provider-wide failure (no key, a rejected key, an
// outage) when it starts, again if it is still happening after
// repeatReportAfter, and once more when lookups work again. A library refresh
// asks for thousands of titles, and one line per title would bury the cause.
type failureReporter struct {
	mu       sync.Mutex
	now      func() time.Time
	last     error
	lastKind error
	loggedAt time.Time
	logf     func(format string, args ...any)
}

func newFailureReporter() *failureReporter {
	return &failureReporter{now: time.Now, logf: log.Printf}
}

func (r *failureReporter) report(err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	kind := failureKind(err)
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.last != nil && kind == r.lastKind && now.Sub(r.loggedAt) < repeatReportAfter {
		return
	}
	r.last, r.lastKind, r.loggedAt = err, kind, now
	if kind == fanart.ErrNotConfigured {
		r.logf("fanart.tv: this build has no built-in API key and none is set in the plugin settings. Artwork lookups answer empty until then.")
		return
	}
	r.logf("fanart.tv: artwork lookups are failing and answer empty: %v", err)
}

func (r *failureReporter) recovered() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		return
	}
	r.last, r.lastKind = nil, nil
	r.logf("fanart.tv: artwork lookups are answering again")
}

// failureKind groups errors so one outage logs once however its detail varies.
func failureKind(err error) error {
	for _, kind := range []error{fanart.ErrNotConfigured, fanart.ErrKeyRejected, fanart.ErrUnavailable, fanart.ErrUnusableAnswer} {
		if errors.Is(err, kind) {
			return kind
		}
	}
	return nil
}
