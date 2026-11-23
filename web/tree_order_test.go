package web

import (
	"os"
	"strings"
	"testing"

	"azml-go/helper/downloader"
)

func TestTreeNaturalOrder(t *testing.T) {
	files := []downloader.MegaFileInfo{
		{ID: "d1", Name: "Season 1", IsDir: true, Path: ""},
		{ID: "a", Name: "Show.S01E10.mkv", Size: 900 << 20, Path: "Season 1"},
		{ID: "b", Name: "Show.S01E2.mkv", Size: 800 << 20, Path: "Season 1"},
		{ID: "c", Name: "Show.S01E1.mkv", Size: 700 << 20, Path: "Season 1"},
		{ID: "s", Name: "Show.S01E1.srt", Size: 50 << 10, Path: "Season 1"},
		{ID: "z", Name: "readme.txt", Size: 1 << 10, Path: ""},
	}
	h := BuildMegaHTMLTree(files)
	i1, i2, i10 := strings.Index(h, "S01E1.mkv"), strings.Index(h, "S01E2.mkv"), strings.Index(h, "S01E10.mkv")
	if !(i1 < i2 && i2 < i10) {
		t.Fatalf("not in natural order: %d %d %d", i1, i2, i10)
	}
	if strings.Index(h, "Season 1") > strings.Index(h, "readme.txt") {
		t.Fatal("folders must come first")
	}
	if out := os.Getenv("TREE_PAGE_OUT"); out != "" {
		page := strings.ReplaceAll(pageHTML, "{My_content}", h)
		_ = os.WriteFile(out, []byte(strings.ReplaceAll(page, "{form_url}", "#")), 0o644)
	}
}
