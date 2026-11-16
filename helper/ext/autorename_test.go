package ext

import "testing"

func TestAutoRenameTitles(t *testing.T) {
	for in, want := range map[string]string{
		"Breaking.Bad.S02E05.1080p.WEB.mkv":           "Breaking Bad S02E05 [1080p].mkv",
		"[SubsPlease] Frieren - 12 (1080p) [ABC].mkv": "Frieren S01E12 [1080p].mkv",
		"Show Name Season 3 Episode 7 720p.mp4":       "Show Name S03E07 [720p].mp4",
		"One Piece 1100.mkv":                          "One Piece S01E1100 [1080p].mkv",
	} {
		if got := ApplyTemplateRename(in, "{title} S{season}E{episode} [{quality}]"); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}
