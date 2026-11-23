package ext

import (
	"testing"

	"azml-go/types"
)

func TestUnifiedTaskBatch(t *testing.T) {
	u := NewUnifiedTask("g", "Season 1", 0, 1, 1, 0, nil)
	u.BeginBatch("Season 1", 300)
	u.SetStatus(types.StatusUploading, "tg")
	u.SetName("ep1.mkv")
	u.SetSize(100)
	u.SetProgress(0, 100, 0, "-")
	u.SetProgress(100, 100, 10, "0s")
	u.SetStatus(types.StatusProcessing, "Screenshots") // a step between the files: its own numbers
	u.SetProgress(3, 5, 0, "-")
	u.SetStatus(types.StatusUploading, "tg")
	u.SetName("ep2.mkv")
	u.SetProgress(0, 200, 0, "-")
	u.SetProgress(50, 200, 10, "")
	if u.Name() != "Season 1" || u.Size() != 300 || u.DownloadedBytes() != 150 {
		t.Fatalf("name %q size %d done %d", u.Name(), u.Size(), u.DownloadedBytes())
	}
	if p := u.Progress(); p < 49.9 || p > 50.1 {
		t.Fatalf("progress %.1f", p)
	}
}
