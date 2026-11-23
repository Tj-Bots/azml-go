package modules

import (
	"strings"
	"testing"

	"azml-go/helper/ext"
)

func TestPosterImageView(t *testing.T) {
	s := &posterSession{
		uid: 1, userL: "he", sel: 0, kind: "p", lang: "all",
		titles: []ext.TMDBTitle{{ID: 7, MediaType: "movie", Name: "Oppenheimer", Year: "2023"}},
		images: map[string][]ext.TMDBImage{
			"p": {{Path: "/a.jpg", Width: 2000, Height: 3000, Lang: "fr"}, {Path: "/b.jpg", Width: 1000, Height: 1500, Lang: "he"},
				{Path: "/c.jpg", Width: 1000, Height: 1500, Lang: "en"}, {Path: "/d.jpg", Width: 1000, Height: 1500}},
			"b": {{Path: "/e.jpg", Width: 3840, Height: 2160}},
		},
	}
	text, url, btn := posterImageView("1", s)
	for _, w := range []string{"<b>Oppenheimer (2023)</b>", "Poster · Portrait", "Language :</b> <i>French", "2000 x 3000", "1/4", "tmdb.org/movie/7"} {
		if !strings.Contains(text, w) {
			t.Fatalf("missing %q in\n%s", w, text)
		}
	}
	if url != "https://image.tmdb.org/t/p/original/a.jpg" {
		t.Fatal(url)
	}
	kb := btn.BuildBotAPIKeyboard(5)
	for _, w := range []string{"pstr 1 i 0", "pstr 1 i 3", "pstr 1 k b", "pstr 1 l en", "pstr 1 g", "pstr 1 b"} {
		if !strings.Contains(kb, w) {
			t.Fatalf("keyboard misses %q: %s", w, kb)
		}
	}
	if l := posterLangs(s); strings.Join(l, ",") != "en,he,xx,fr" {
		t.Fatalf("lang order %v", l)
	}
}
