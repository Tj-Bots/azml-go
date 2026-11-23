package modules

import (
	"strings"
	"testing"

	"azml-go/helper/ext"
)

func TestPosterView(t *testing.T) {
	s := &posterSession{
		uid:    1,
		titles: []ext.TMDBTitle{{ID: 1, MediaType: "movie", Name: "Oppenheimer", Year: "2023"}},
		title:  ext.TMDBTitle{ID: 1, MediaType: "movie", Name: "Oppenheimer", Year: "2023"},
		images: map[string][]ext.TMDBImage{
			"poster":   {{Path: "/a.jpg", Width: 2000, Height: 3000, Lang: "en"}, {Path: "/b.jpg", Width: 1000, Height: 1500, Lang: "he"}},
			"backdrop": {{Path: "/c.jpg", Width: 3840, Height: 2160}},
		},
		kind: "poster", lang: "*",
	}
	caption, url, _ := posterView("1", s)
	for _, w := range []string{"Oppenheimer (2023)", "Type:</b> Poster", "2000 × 3000", "Language:</b> English", "1 / 2"} {
		if !strings.Contains(caption, w) {
			t.Fatalf("caption misses %q:\n%s", w, caption)
		}
	}
	if url != "https://image.tmdb.org/t/p/w780/a.jpg" {
		t.Fatalf("url %s", url)
	}
	s.lang = "he"
	if c, _, _ := posterView("1", s); !strings.Contains(c, "Hebrew") || !strings.Contains(c, "1 / 1") {
		t.Fatalf("lang filter: %s", c)
	}
	s.kind, s.lang = "backdrop", "*"
	if c, u, _ := posterView("1", s); !strings.Contains(c, "No Text") || !strings.Contains(u, "w1280") {
		t.Fatalf("backdrop: %s %s", c, u)
	}
}
