package modules

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"azml-go/helper/ext"
)

func TestTrackStep(t *testing.T) {
	dir := t.TempDir()
	task := ext.NewUnifiedTask("g", "n", 0, 0, 0, 0, nil)
	done := trackStep(task, dir, 4<<20)
	_ = os.WriteFile(filepath.Join(dir, "out.bin"), make([]byte, 2<<20), 0644)
	time.Sleep(1300 * time.Millisecond)
	done()
	if task.DownloadedBytes() != 2<<20 || task.Speed() <= 0 || task.Progress() < 49 {
		t.Fatalf("got %d bytes, %.0f B/s, %.1f%%", task.DownloadedBytes(), task.Speed(), task.Progress())
	}
}
