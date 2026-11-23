package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"azml-go/config"
)

// /poster: the titles of a search and all the images of one title (posters, backdrops, logos)
// with their size and language, to browse them like the poster command of TJ-Rename-Bot.

// TMDBTitle is one search result.
type TMDBTitle struct {
	ID        int
	MediaType string // movie / tv
	Name      string
	Year      string
}

// TMDBImage is one image of a title.
type TMDBImage struct {
	Path   string
	Width  int
	Height int
	Lang   string // "" = no text
}

var tmdbBrowseHTTP = &http.Client{Timeout: 15 * time.Second}

// TMDBEnabled: TMDB_ACCESS_TOKEN (or an API key) is set.
func TMDBEnabled() bool { return strings.TrimSpace(config.Get().TMDBAccessToken) != "" }

func tmdbGet(ctx context.Context, path string, q url.Values, out interface{}) error {
	token := strings.TrimSpace(config.Get().TMDBAccessToken)
	if token == "" {
		return fmt.Errorf("TMDB is not set")
	}
	if q == nil {
		q = url.Values{}
	}
	if len(token) < 50 { // an API key, not a read access token
		q.Set("api_key", token)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.themoviedb.org/3"+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if len(token) >= 50 {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := tmdbBrowseHTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TMDB answered %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// SearchTMDBTitles: movies and shows for a name in any language, titles in English, at most max.
func SearchTMDBTitles(ctx context.Context, name, year, userLang string, max int) ([]TMDBTitle, error) {
	var langs []string
	add := func(l string) {
		for _, x := range langs {
			if x == l {
				return
			}
		}
		langs = append(langs, l)
	}
	// English first: the bot is in English, so the titles are too (the user's Telegram
	// language gave Hebrew names). A name in other letters (ספיידרמן) is found in its language.
	_ = userLang
	add("en-US")
	if l := scriptLang(name); l != "" {
		add(l)
	}
	var res struct {
		Results []struct {
			ID           int    `json:"id"`
			MediaType    string `json:"media_type"`
			Title        string `json:"title"`
			Name         string `json:"name"`
			ReleaseDate  string `json:"release_date"`
			FirstAirDate string `json:"first_air_date"`
		} `json:"results"`
	}
	var out []TMDBTitle
	for _, l := range langs {
		q := url.Values{"query": {name}, "include_adult": {"false"}, "language": {l}, "page": {"1"}}
		if year != "" {
			q.Set("year", year)
		}
		if err := tmdbGet(ctx, "/search/multi", q, &res); err != nil {
			return nil, err
		}
		for _, r := range res.Results {
			if r.MediaType != "movie" && r.MediaType != "tv" {
				continue
			}
			t := TMDBTitle{ID: r.ID, MediaType: r.MediaType, Name: r.Title, Year: r.ReleaseDate}
			if r.MediaType == "tv" {
				t.Name, t.Year = r.Name, r.FirstAirDate
			}
			if len(t.Year) >= 4 {
				t.Year = t.Year[:4]
			}
			out = append(out, t)
			if len(out) >= max {
				break
			}
		}
		if len(out) > 0 {
			break
		}
	}
	return out, nil
}

// TMDBTitleImages: the posters, backdrops and logos of a title (best rated first, like TMDB).
func TMDBTitleImages(ctx context.Context, mediaType string, id int) (posters, backdrops, logos []TMDBImage, err error) {
	var res struct {
		Posters   []tmdbBrowseImage `json:"posters"`
		Backdrops []tmdbBrowseImage `json:"backdrops"`
		Logos     []tmdbBrowseImage `json:"logos"`
	}
	if err = tmdbGet(ctx, fmt.Sprintf("/%s/%d/images", mediaType, id), nil, &res); err != nil {
		return
	}
	conv := func(in []tmdbBrowseImage) []TMDBImage {
		out := make([]TMDBImage, 0, len(in))
		for _, i := range in {
			if i.FilePath == "" {
				continue
			}
			im := TMDBImage{Path: i.FilePath, Width: i.Width, Height: i.Height}
			if i.ISO6391 != nil {
				im.Lang = *i.ISO6391
			}
			out = append(out, im)
		}
		return out
	}
	return conv(res.Posters), conv(res.Backdrops), conv(res.Logos), nil
}

type tmdbBrowseImage struct {
	FilePath string  `json:"file_path"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	ISO6391  *string `json:"iso_639_1"`
}

// TMDBImageURL: the image in a size (w500, w780, w1280, original).
func TMDBImageURL(path, size string) string {
	return "https://image.tmdb.org/t/p/" + size + path
}

var tmdbLangNames = map[string]string{
	"en": "English", "he": "Hebrew", "ar": "Arabic", "ru": "Russian", "hi": "Hindi", "ta": "Tamil",
	"te": "Telugu", "ml": "Malayalam", "kn": "Kannada", "bn": "Bengali", "mr": "Marathi", "fr": "French",
	"de": "German", "es": "Spanish", "it": "Italian", "pt": "Portuguese", "nl": "Dutch", "pl": "Polish",
	"tr": "Turkish", "uk": "Ukrainian", "cs": "Czech", "sk": "Slovak", "hu": "Hungarian", "ro": "Romanian",
	"bg": "Bulgarian", "el": "Greek", "sv": "Swedish", "no": "Norwegian", "da": "Danish", "fi": "Finnish",
	"ja": "Japanese", "ko": "Korean", "zh": "Chinese", "th": "Thai", "vi": "Vietnamese", "id": "Indonesian",
	"ms": "Malay", "fa": "Persian", "ur": "Urdu", "lt": "Lithuanian", "lv": "Latvian", "et": "Estonian",
	"hr": "Croatian", "sr": "Serbian", "sl": "Slovenian", "ka": "Georgian", "ca": "Catalan",
}

// TMDBLangName: "English" for "en", "No Text" for an image without text.
func TMDBLangName(code string) string {
	if code == "" {
		return "No Text"
	}
	if n, ok := tmdbLangNames[code]; ok {
		return n
	}
	return strings.ToUpper(code)
}
