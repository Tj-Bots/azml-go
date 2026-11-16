package modules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoTools(t *testing.T) {
	d := t.TempDir()
	v1 := makeVideo(t, d) // movie.mkv with audio + subtitle
	_ = os.Rename(v1, filepath.Join(d, "a.mkv"))
	v2 := makeVideo(t, d)
	_ = os.Rename(v2, filepath.Join(d, "b.mkv"))

	// Video + Video
	vtMergeVideos(d)
	if vids := vtVideos(d); len(vids) != 1 || !strings.Contains(vids[0], "merged") {
		t.Fatalf("merge: %v", vids)
	}
	// Remove subtitles, Trim, Extract audio, Rename
	p, _ := runVideoTools(d, &VTSelection{Remove: "subs"})
	p, _ = runVideoTools(p, &VTSelection{TrimStart: "1", TrimEnd: "2"})
	p, _ = runVideoTools(p, &VTSelection{Extract: "audio", Rename: "Show"})
	var files []string
	for _, f := range mediaFiles(p) {
		if filepath.Base(f) != "s.srt" { // the test's own subtitle source
			files = append(files, f)
		}
	}
	if filepath.Base(p) != "Show" || len(files) != 1 || filepath.Ext(files[0]) != ".m4a" {
		t.Fatalf("extract/rename: %s %v", p, files)
	}

	// menu text and buttons
	st := &vtState{user: 5, page: "main", sel: VTSelection{Rename: "x", TrimStart: "10"}}
	text, b := st.menu()
	if !strings.Contains(text, "Rename → <code>x</code>") || !strings.Contains(text, "Trim → 10") {
		t.Fatalf("menu: %s", text)
	}
	for _, row := range b.Rows(2) {
		if len(row) > 2 {
			t.Fatal("row too long")
		}
		for _, btn := range row {
			if len(btn.Data) > 64 {
				t.Fatal("callback too long")
			}
		}
	}
}
