package ext

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMakeScreenshots(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	v := filepath.Join(t.TempDir(), "v.mp4")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=12:size=320x240:rate=10", "-pix_fmt", "yuv420p", v).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg: %v %s", err, out)
	}
	files, err := MakeScreenshots(v, 3)
	if err != nil || len(files) != 3 {
		t.Fatalf("got %v %v", files, err)
	}
	defer os.RemoveAll(filepath.Dir(files[0]))
	for _, f := range files {
		if fi, err := os.Stat(f); err != nil || fi.Size() == 0 {
			t.Fatalf("bad file %s", f)
		}
	}
}
