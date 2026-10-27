package fanart

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func loadArtwork(t *testing.T, name string) *Artwork {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var artwork Artwork
	if err := json.Unmarshal(data, &artwork); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return &artwork
}

func byURLSuffix(t *testing.T, records []Record, suffix string) Record {
	t.Helper()
	for _, record := range records {
		if strings.HasSuffix(record.URL, suffix) {
			return record
		}
	}
	t.Fatalf("no record ending in %q among %+v", suffix, records)
	return Record{}
}

func TestMovieRecords(t *testing.T) {
	records := loadArtwork(t, "movie.json").MovieRecords()

	// Ten images in the fixture: one repeats a URL and one is not a web URL.
	if len(records) != 8 {
		t.Fatalf("got %d records, want 8: %+v", len(records), records)
	}
	for _, record := range records {
		if !strings.HasPrefix(record.URL, "https://assets.fanart.tv/") {
			t.Errorf("URL %q is not rewritten to HTTPS", record.URL)
		}
		if record.Rating <= 0 {
			t.Errorf("%s rates %v; the host only replaces art with a rating above zero", record.URL, record.Rating)
		}
		if record.Season != nil {
			t.Errorf("movie image %s carries a season", record.URL)
		}
	}

	poster := byURLSuffix(t, records, "example-poster-en.jpg")
	if poster.Kind != "poster" || poster.Language != "en" || poster.IncludesText == nil || !*poster.IncludesText {
		t.Errorf("titled poster = %+v, want an English poster with text", poster)
	}
	textless := byURLSuffix(t, records, "example-poster-textless.jpg")
	if textless.Language != "" || textless.IncludesText == nil || *textless.IncludesText {
		t.Errorf("textless poster = %+v, want no language and no text", textless)
	}

	background := byURLSuffix(t, records, "example-background.jpg")
	if background.Kind != "backdrop" || background.Language != "" || background.IncludesText != nil {
		t.Errorf("background = %+v, want a language-neutral backdrop", background)
	}

	hdLogo := byURLSuffix(t, records, "example-hdlogo.png")
	sdLogo := byURLSuffix(t, records, "example-logo.png")
	if hdLogo.Kind != "logo" || sdLogo.Kind != "logo" {
		t.Fatalf("logos = %+v and %+v, want both logos", hdLogo, sdLogo)
	}
	if hdLogo.Rating <= sdLogo.Rating {
		t.Errorf("HD logo with no likes rates %v, SD logo with 40 likes %v; HD must still win", hdLogo.Rating, sdLogo.Rating)
	}

	if got := byURLSuffix(t, records, "example-hdclearart.png"); got.Kind != "clearart" || got.Language != "de" {
		t.Errorf("clear art = %+v, want German clear art", got)
	}
	if got := byURLSuffix(t, records, "example-disc.png"); got.Kind != "disc" {
		t.Errorf("disc = %+v", got)
	}
	if got := byURLSuffix(t, records, "example-thumb.jpg"); got.Kind != "thumb" {
		t.Errorf("thumb = %+v", got)
	}
}

func TestShowRecordsLeaveOutSeasonArt(t *testing.T) {
	records := loadArtwork(t, "show.json").ShowRecords()

	kinds := map[string]int{}
	for _, record := range records {
		kinds[record.Kind]++
		if record.Season != nil {
			t.Errorf("show-level answer carries season art %s", record.URL)
		}
		if strings.Contains(record.URL, "/seasonposter/") || strings.Contains(record.URL, "/characterart/") {
			t.Errorf("show-level answer carries %s", record.URL)
		}
	}
	want := map[string]int{"poster": 1, "backdrop": 1, "logo": 2, "clearart": 1, "banner": 1, "thumb": 1}
	for kind, count := range want {
		if kinds[kind] != count {
			t.Errorf("%d %s records, want %d (all: %v)", kinds[kind], kind, count, kinds)
		}
	}
}

func TestSeasonRecords(t *testing.T) {
	artwork := loadArtwork(t, "show.json")

	seasonTwo := artwork.SeasonRecords(2)
	if len(seasonTwo) != 1 || !strings.HasSuffix(seasonTwo[0].URL, "example-season2.jpg") {
		t.Fatalf("season 2 = %+v, want its one poster", seasonTwo)
	}
	if seasonTwo[0].Season == nil || *seasonTwo[0].Season != 2 || seasonTwo[0].Kind != "poster" {
		t.Errorf("season 2 poster = %+v, want a poster filed under season 2", seasonTwo[0])
	}

	specials := artwork.SeasonRecords(0)
	if len(specials) != 1 || specials[0].Season == nil || *specials[0].Season != 0 {
		t.Fatalf("specials = %+v, want the season 0 poster", specials)
	}
	if specials[0].IncludesText == nil || *specials[0].IncludesText {
		t.Errorf("textless specials poster = %+v, want includes_text false", specials[0])
	}

	if got := artwork.SeasonRecords(9); len(got) != 0 {
		t.Errorf("season 9 = %+v; a poster filed under all seasons belongs to none", got)
	}
}

func TestNilArtworkHasNoRecords(t *testing.T) {
	var artwork *Artwork
	if artwork.MovieRecords() != nil || artwork.ShowRecords() != nil || artwork.SeasonRecords(1) != nil {
		t.Fatal("a title fanart.tv does not know must have no records")
	}
}

func TestForLanguage(t *testing.T) {
	records := []Record{
		{URL: "neutral", Language: ""},
		{URL: "english", Language: "en"},
		{URL: "german", Language: "de"},
		{URL: "french", Language: "fr"},
	}
	cases := []struct {
		requested string
		want      string
	}{
		{"en", "neutral english"},
		{"en-AU", "neutral english"},
		{"DE", "neutral english german"},
		{"de_DE", "neutral english german"},
		{"fr-CA", "neutral english french"},
		{"", "neutral english"},
	}
	for _, tc := range cases {
		var got []string
		for _, record := range ForLanguage(records, tc.requested) {
			got = append(got, record.URL)
		}
		if strings.Join(got, " ") != tc.want {
			t.Errorf("ForLanguage(%q) kept %v, want %s", tc.requested, got, tc.want)
		}
	}
	if len(records) != 4 || records[2].URL != "german" {
		t.Fatalf("ForLanguage changed its input: %v", records)
	}
}

func TestRatingBands(t *testing.T) {
	cases := []struct {
		likes   flexString
		variant variant
		min     float64
		max     float64
	}{
		{"0", hd, 5, 5},
		{"5", hd, 7.5, 7.5},
		{"1000", hd, 9.9, 10},
		{"0", sd, 1, 1},
		{"1000", sd, 4.9, 5},
		{"", hd, 5, 5},
		{"-3", sd, 1, 1},
		{"lots", sd, 1, 1},
	}
	for _, tc := range cases {
		got := rating(tc.likes, tc.variant)
		if got < tc.min || got > tc.max {
			t.Errorf("rating(%q, %v) = %v, want between %v and %v", tc.likes, tc.variant, got, tc.min, tc.max)
		}
	}
}
