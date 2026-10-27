package fanart

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Artwork is fanart.tv's answer for one movie or show. Only the image types
// Silo has a slot for are read; character art, season thumbs and season
// banners have none, so they are left out.
type Artwork struct {
	MoviePoster     []Image `json:"movieposter"`
	MovieBackground []Image `json:"moviebackground"`
	HDMovieLogo     []Image `json:"hdmovielogo"`
	MovieLogo       []Image `json:"movielogo"`
	HDMovieClearArt []Image `json:"hdmovieclearart"`
	MovieArt        []Image `json:"movieart"`
	MovieDisc       []Image `json:"moviedisc"`
	MovieBanner     []Image `json:"moviebanner"`
	MovieThumb      []Image `json:"moviethumb"`

	TVPoster       []Image `json:"tvposter"`
	SeasonPoster   []Image `json:"seasonposter"`
	ShowBackground []Image `json:"showbackground"`
	HDTVLogo       []Image `json:"hdtvlogo"`
	ClearLogo      []Image `json:"clearlogo"`
	HDClearArt     []Image `json:"hdclearart"`
	ClearArt       []Image `json:"clearart"`
	TVBanner       []Image `json:"tvbanner"`
	TVThumb        []Image `json:"tvthumb"`
}

// Image is one fanart.tv image. The API sends likes and season as strings;
// flexString also takes a number, so a change of type upstream does not lose
// a whole title.
type Image struct {
	URL    string     `json:"url"`
	Lang   string     `json:"lang"`
	Likes  flexString `json:"likes"`
	Season flexString `json:"season"`
}

type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = flexString(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		*s = flexString(number.String())
		return nil
	}
	*s = ""
	return nil
}

// Record is one image in Silo's terms. Kind is the plugin contract's image
// kind: poster, backdrop, logo, clearart, disc, banner or thumb.
type Record struct {
	Kind     string
	URL      string
	Language string
	// Season is set only on a season poster. Zero is Specials.
	Season *int
	Rating float64
	// IncludesText is set on posters: a poster tagged with a language carries
	// the title, and fanart.tv files a textless poster under no language.
	IncludesText *bool
}

// variant ranks the two resolutions fanart.tv keeps for logos and clear art.
type variant int

const (
	// hd is the current format, or the only one an image type has.
	hd variant = iota
	// sd is the older, smaller format, offered only next to the HD one.
	sd
)

type source struct {
	images  []Image
	kind    string
	variant variant
	// textless marks an image type fanart.tv's rules keep free of text, so its
	// language tag says nothing about what the image shows.
	textless bool
}

// MovieRecords returns a movie's artwork.
func (a *Artwork) MovieRecords() []Record {
	if a == nil {
		return nil
	}
	return records([]source{
		{images: a.MoviePoster, kind: "poster"},
		{images: a.MovieBackground, kind: "backdrop", textless: true},
		{images: a.HDMovieLogo, kind: "logo"},
		{images: a.MovieLogo, kind: "logo", variant: sd},
		{images: a.HDMovieClearArt, kind: "clearart"},
		{images: a.MovieArt, kind: "clearart", variant: sd},
		{images: a.MovieDisc, kind: "disc"},
		{images: a.MovieBanner, kind: "banner"},
		{images: a.MovieThumb, kind: "thumb"},
	}, nil)
}

// ShowRecords returns a series' own artwork. Season posters are left out:
// Silo reads every image in a show-level answer as show artwork.
func (a *Artwork) ShowRecords() []Record {
	if a == nil {
		return nil
	}
	return records([]source{
		{images: a.TVPoster, kind: "poster"},
		{images: a.ShowBackground, kind: "backdrop", textless: true},
		{images: a.HDTVLogo, kind: "logo"},
		{images: a.ClearLogo, kind: "logo", variant: sd},
		{images: a.HDClearArt, kind: "clearart"},
		{images: a.ClearArt, kind: "clearart", variant: sd},
		{images: a.TVBanner, kind: "banner"},
		{images: a.TVThumb, kind: "thumb"},
	}, nil)
}

// SeasonRecords returns the posters fanart.tv files under one season. Season
// zero is Specials, as in Silo; a poster filed under "all" seasons belongs to
// none of them.
func (a *Artwork) SeasonRecords(season int) []Record {
	if a == nil {
		return nil
	}
	return records([]source{{images: a.SeasonPoster, kind: "poster"}}, &season)
}

func records(sources []source, season *int) []Record {
	var out []Record
	seen := make(map[string]struct{})
	for _, src := range sources {
		for _, image := range src.images {
			if season != nil {
				number, err := strconv.Atoi(strings.TrimSpace(string(image.Season)))
				if err != nil || number != *season {
					continue
				}
			}
			url := secureURL(image.URL)
			if url == "" {
				continue
			}
			if _, dup := seen[url]; dup {
				continue
			}
			seen[url] = struct{}{}

			record := Record{
				Kind:     src.kind,
				URL:      url,
				Language: language(image.Lang),
				Rating:   rating(image.Likes, src.variant),
			}
			if src.textless {
				record.Language = ""
			}
			if season != nil {
				number := *season
				record.Season = &number
			}
			if src.kind == "poster" {
				hasText := record.Language != ""
				record.IncludesText = &hasText
			}
			out = append(out, record)
		}
	}
	return out
}

// secureURL returns the image URL over HTTPS, or "" when it is not a web URL.
// fanart.tv's asset links have been plain http:// in older answers; the asset
// host serves the same paths over HTTPS.
func secureURL(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "https://"):
		return raw
	case strings.HasPrefix(raw, "http://"):
		return "https://" + strings.TrimPrefix(raw, "http://")
	default:
		return ""
	}
}

// language returns the image's language tag, or "" for none. fanart.tv tags
// an image with no language "00".
func language(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "00" {
		return ""
	}
	return lang
}

// rating turns fanart.tv's likes into Silo's image rating. Silo replaces
// stored artwork on a refresh only with an image rated above zero, and picks
// one provider's best image in each language by the highest rating, so:
//
//   - every image rates above zero, even with no likes;
//   - more likes rate higher, flattening out the way vote averages do;
//   - an HD image (5 to 10) always outranks an SD one (1 to 5) of the same
//     type and language.
func rating(likes flexString, v variant) float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(string(likes)), 64)
	if err != nil || n < 0 {
		n = 0
	}
	share := n / (n + 5)
	if v == sd {
		return 1 + 4*share
	}
	return 5 + 5*share
}
