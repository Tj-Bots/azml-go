package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
)

var (
	tmdbUrlRegex      = regexp.MustCompile(`(?i)https?://\S+`)
	tmdbTgLinkRegex   = regexp.MustCompile(`(?i)\b(?:t|telegram)\.me/\S+`)
	tmdbBracketRegex  = regexp.MustCompile(`[\[\](){}]`)
	tmdbExtRegex      = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|mov|flv|webm|ts|m4v|wmv|iso|rar|zip|7z)$`)
	tmdbDomainRegex   = regexp.MustCompile(`(?i)\b(?:www\.)?\w+\.(?:com|net|org|xyz|me|in|to|co|cc|info|tv|link|app|online|site|club|work|icu|top|vip|pro)\b`)
	tmdbWwwRegex      = regexp.MustCompile(`(?i)\bwww\S*`)
	tmdbCleanDelim    = regexp.MustCompile(`[-_.]`)
	tmdbSpacesRegex   = regexp.MustCompile(`\s+`)
	tmdbYearRegex     = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	tmdbSeasonSxxExx  = regexp.MustCompile(`(?i)(?:^|[^\w])s0*(\d{1,2})e\d{1,2}(?:[^\w]|$)`)
	tmdbSeasonWord    = regexp.MustCompile(`(?i)\bseason\s*(\d{1,2})\b`)
	tmdbSeasonSimple  = regexp.MustCompile(`(?i)(?:^|[^\w])s0*(\d{1,2})(?:[^\w]|$)`)
	tmdbEpisodeRegex  = regexp.MustCompile(`(?i)(?:^|[^\w])(?:e\d{1,4}|ep\s*\d{1,4}|episode\s*\d{1,4})(?:[^\w]|$)`)
	tmdbTagStripRegex = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|480p|4k|uhd|fhd|hdrip|web-dl|webrip|bluray|brrip|dvdrip|hdtv|remux|x264|x265|h264|h265|hevc|10bit|8bit|hdr|hdr10|dovi|dv|avc|aac|ac3|eac3|dts|ddp5\.1|atmos|mp3|flac|dual audio|multi audio|hindi|english|tamil|telugu)\b`)
)

// FormatCleanPosterTitle extracts clean movie/show title, season, and year for TMDB search
func FormatCleanPosterTitle(rawTitle, renameRegex string) (title, season, year string) {
	if unquoted, err := url.QueryUnescape(rawTitle); err == nil {
		rawTitle = unquoted
	}

	if renameRegex != "" {
		rawTitle = applyRegexRemname(rawTitle, renameRegex)
	}

	t := tmdbUrlRegex.ReplaceAllString(rawTitle, " ")
	t = tmdbTgLinkRegex.ReplaceAllString(t, " ")
	t = tmdbBracketRegex.ReplaceAllString(t, " ")
	t = tmdbExtRegex.ReplaceAllString(t, "")
	t = tmdbDomainRegex.ReplaceAllString(t, " ")
	t = tmdbWwwRegex.ReplaceAllString(t, " ")
	t = tmdbCleanDelim.ReplaceAllString(t, " ")
	t = tmdbTagStripRegex.ReplaceAllString(t, " ")
	t = tmdbSpacesRegex.ReplaceAllString(t, " ")
	t = strings.TrimSpace(t)

	// Extract season
	if match := tmdbSeasonSxxExx.FindStringSubmatchIndex(t); len(match) >= 4 {
		sNum := t[match[2]:match[3]]
		if n, err := strconv.Atoi(sNum); err == nil {
			season = fmt.Sprintf("Season %d", n)
		}
		if match[0] <= 1 {
			t = strings.TrimSpace(t[match[1]:])
		} else {
			t = strings.TrimSpace(t[:match[0]])
		}
		return finalCleanTitle(t), season, ""
	}

	if match := tmdbSeasonWord.FindStringSubmatchIndex(t); len(match) >= 4 {
		sNum := t[match[2]:match[3]]
		if n, err := strconv.Atoi(sNum); err == nil {
			season = fmt.Sprintf("Season %d", n)
		}
		if match[0] <= 1 {
			t = strings.TrimSpace(t[match[1]:])
		} else {
			t = strings.TrimSpace(t[:match[0]])
		}
		return finalCleanTitle(t), season, ""
	}

	if match := tmdbSeasonSimple.FindStringSubmatchIndex(t); len(match) >= 4 {
		sNum := t[match[2]:match[3]]
		if n, err := strconv.Atoi(sNum); err == nil {
			season = fmt.Sprintf("Season %d", n)
		}
		if match[0] <= 1 {
			t = strings.TrimSpace(t[match[1]:])
		} else {
			t = strings.TrimSpace(t[:match[0]])
		}
		return finalCleanTitle(t), season, ""
	}

	// Remove episode numbers
	if match := tmdbEpisodeRegex.FindStringIndex(t); len(match) == 2 {
		if match[0] <= 1 {
			t = strings.TrimSpace(t[match[1]:])
		} else {
			t = strings.TrimSpace(t[:match[0]])
		}
	}

	// Extract year
	allYears := tmdbYearRegex.FindAllStringIndex(t, -1)
	if len(allYears) > 0 {
		lastYearMatch := allYears[len(allYears)-1]
		year = t[lastYearMatch[0]:lastYearMatch[1]]
		if lastYearMatch[0] <= 1 {
			t = strings.TrimSpace(t[lastYearMatch[1]:])
		} else {
			t = strings.TrimSpace(t[:lastYearMatch[0]])
		}
	}

	return finalCleanTitle(t), season, year
}

func finalCleanTitle(t string) string {
	t = tmdbBracketRegex.ReplaceAllString(t, "")
	t = tmdbSpacesRegex.ReplaceAllString(t, " ")
	return strings.TrimSpace(t)
}

type tmdbSearchResult struct {
	Results []struct {
		ID           int    `json:"id"`
		MediaType    string `json:"media_type"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		BackdropPath string `json:"backdrop_path"`
		PosterPath   string `json:"poster_path"`
	} `json:"results"`
}

type tmdbImageItem struct {
	FilePath string  `json:"file_path"`
	ISO6391  *string `json:"iso_639_1"`
}

type tmdbImagesResult struct {
	Backdrops []tmdbImageItem `json:"backdrops"`
	Posters   []tmdbImageItem `json:"posters"`
}

// GetTMDBPosterLink queries TMDB API to find poster or landscape backdrop URL
func GetTMDBPosterLink(ctx context.Context, title, year string, asDoc bool, lang string) (string, error) {
	return tmdbImageLink(ctx, title, year, asDoc, lang, []string{"en-US"})
}

// GetTMDBCoverByName: /cover <name>. The name in any language (ספיידרמן, Spiderman, سبايدرمان):
// it is searched in the user's language, in the language of its letters and in English. The
// image is a backdrop (landscape), with text in the user's language, else English, else none.
func GetTMDBCoverByName(ctx context.Context, name, lang string) (string, error) {
	name = strings.TrimSpace(name)
	year := ""
	if m := tmdbYearRegex.FindStringIndex(name); m != nil && m[0] > 0 {
		year = name[m[0]:m[1]]
		name = strings.TrimSpace(name[:m[0]] + name[m[1]:])
	}
	var langs []string
	add := func(l string) {
		for _, x := range langs {
			if x == l {
				return
			}
		}
		langs = append(langs, l)
	}
	if l := scriptLang(name); l != "" {
		add(l)
	}
	if l := shortLang(lang); l != "" && l != "en" {
		add(l)
	}
	add("en-US")
	return tmdbImageLink(ctx, name, year, false, lang, langs)
}

// scriptLang: the language of the letters of a text (Hebrew, Arabic, Russian ...), "" for Latin.
func scriptLang(s string) string {
	for _, r := range s {
		switch {
		case r >= 0x0590 && r <= 0x05FF:
			return "he"
		case r >= 0x0600 && r <= 0x06FF:
			return "ar"
		case r >= 0x0400 && r <= 0x04FF:
			return "ru"
		case r >= 0x0900 && r <= 0x097F:
			return "hi"
		case r >= 0x0E00 && r <= 0x0E7F:
			return "th"
		case r >= 0x3040 && r <= 0x30FF:
			return "ja"
		case r >= 0xAC00 && r <= 0xD7AF:
			return "ko"
		case r >= 0x4E00 && r <= 0x9FFF:
			return "zh"
		}
	}
	return ""
}

// tmdbImageLink: the search is tried in each of searchLangs until something is found.
func tmdbImageLink(ctx context.Context, title, year string, asDoc bool, lang string, searchLangs []string) (string, error) {
	cfg := config.Get()
	token := strings.TrimSpace(cfg.TMDBAccessToken)
	if token == "" {
		return "", nil
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}

	searchURL := "https://api.themoviedb.org/3/search/multi"
	var searchRes tmdbSearchResult
	for _, sl := range searchLangs {
		q := url.Values{}
		q.Set("query", title)
		q.Set("include_adult", "false")
		q.Set("language", sl)
		q.Set("page", "1")
		if year != "" {
			q.Set("year", year)
		}
		if len(token) < 50 {
			q.Set("api_key", token)
		}

		req, err := http.NewRequestWithContext(ctx, "GET", searchURL+"?"+q.Encode(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		if len(token) >= 50 {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return "", fmt.Errorf("tmdb search returned status %d", resp.StatusCode)
		}
		searchRes = tmdbSearchResult{}
		dErr := json.NewDecoder(resp.Body).Decode(&searchRes)
		resp.Body.Close()
		if dErr != nil {
			return "", dErr
		}
		if len(searchRes.Results) > 0 {
			break
		}
	}

	if len(searchRes.Results) == 0 {
		return "", nil
	}

	var bestItem *struct {
		ID           int    `json:"id"`
		MediaType    string `json:"media_type"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		BackdropPath string `json:"backdrop_path"`
		PosterPath   string `json:"poster_path"`
	}

	for i := range searchRes.Results {
		if searchRes.Results[i].MediaType != "person" {
			bestItem = &searchRes.Results[i]
			break
		}
	}

	if bestItem == nil {
		return "", nil
	}

	// Fetch image sets for media
	imgURL := fmt.Sprintf("https://api.themoviedb.org/3/%s/%d/images", bestItem.MediaType, bestItem.ID)
	iq := url.Values{}
	iq.Set("include_image_languages", imageLanguages(lang))
	if len(token) < 50 {
		iq.Set("api_key", token)
	}

	imgReq, err := http.NewRequestWithContext(ctx, "GET", imgURL+"?"+iq.Encode(), nil)
	if err == nil {
		imgReq.Header.Set("Accept", "application/json")
		if len(token) >= 50 {
			imgReq.Header.Set("Authorization", "Bearer "+token)
		}
		if imgResp, err := httpClient.Do(imgReq); err == nil {
			defer imgResp.Body.Close()
			if imgResp.StatusCode == http.StatusOK {
				var imagesRes tmdbImagesResult
				if err := json.NewDecoder(imgResp.Body).Decode(&imagesRes); err == nil {
					imgPath := selectBestImagePath(imagesRes, asDoc, lang)
					if imgPath != "" {
						return "https://image.tmdb.org/t/p/original" + imgPath, nil
					}
				}
			}
		}
	}

	// Fallback to basic search paths
	fallback := bestItem.BackdropPath
	if asDoc && bestItem.PosterPath != "" {
		fallback = bestItem.PosterPath
	} else if fallback == "" {
		fallback = bestItem.PosterPath
	}

	if fallback != "" {
		return "https://image.tmdb.org/t/p/original" + fallback, nil
	}

	return "", nil
}

// imageLanguages: the user's language first, then English and images without text.
func imageLanguages(lang string) string {
	lang = shortLang(lang)
	if lang == "" || lang == "en" {
		return "en,null"
	}
	return lang + ",en,null"
}

func shortLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	return lang
}

// selectBestImagePath: posters for documents, backdrops (16:9) for videos;
// in each, the user's language first, then English, then no text, then any.
func selectBestImagePath(res tmdbImagesResult, asDoc bool, lang string) string {
	langs := []string{"en"}
	if l := shortLang(lang); l != "" && l != "en" {
		langs = []string{l, "en"}
	}
	pick := func(items []tmdbImageItem, code string, noText bool) string {
		for _, it := range items {
			if noText && it.ISO6391 == nil {
				return it.FilePath
			}
			if !noText && it.ISO6391 != nil && *it.ISO6391 == code {
				return it.FilePath
			}
		}
		return ""
	}
	order := func(first, second []tmdbImageItem) string {
		for _, l := range langs {
			if p := pick(first, l, false); p != "" {
				return p
			}
		}
		if p := pick(first, "", true); p != "" {
			return p
		}
		if len(first) > 0 {
			return first[0].FilePath
		}
		for _, l := range langs {
			if p := pick(second, l, false); p != "" {
				return p
			}
		}
		if p := pick(second, "", true); p != "" {
			return p
		}
		if len(second) > 0 {
			return second[0].FilePath
		}
		return ""
	}
	if asDoc {
		return order(res.Posters, res.Backdrops)
	}
	return order(res.Backdrops, res.Posters)
}

// DownloadImage downloads an image URL directly to destPath
func DownloadImage(ctx context.Context, imageURL, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download image failed with status %d", resp.StatusCode)
	}

	_ = os.MkdirAll(filepath.Dir(destPath), 0755)
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// GetAutoThumbnail searches TMDB for title and returns a local high-res poster file path
func GetAutoThumbnail(ctx context.Context, rawFilename string, asDoc bool, renameRegex string, lang string) (string, error) {
	title, _, year := FormatCleanPosterTitle(rawFilename, renameRegex)
	if len(title) < 2 {
		return "", nil
	}

	posterURL, err := GetTMDBPosterLink(ctx, title, year, asDoc, lang)
	if err != nil || posterURL == "" {
		return "", err
	}

	destDir := "Thumbnails"
	_ = os.MkdirAll(destDir, 0755)
	destPath := filepath.Join(destDir, fmt.Sprintf("tmdb_%s.jpg", ShortHash(rawFilename)))

	if err := DownloadImage(ctx, posterURL, destPath); err != nil {
		return "", err
	}

	return destPath, nil
}
