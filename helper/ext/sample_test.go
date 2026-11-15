package ext

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMakeSampleVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	src := filepath.Join(t.TempDir(), "movie.mkv")
	if err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=30:size=320x240:rate=10", "-c:v", "libx264", "-g", "10", src, "-y").Run(); err != nil {
		t.Skip("could not make a test video")
	}
	out := MakeSampleVideo(src, 10)
	if out == "" {
		t.Fatal("no sample made")
	}
	defer os.Remove(out)
	d, err := ProbeMedia(out)
	if err != nil || d.Duration < 8 || d.Duration > 12 {
		t.Fatalf("sample duration wrong: %+v %v", d, err)
	}
	if MakeSampleVideo(src, 60) != "" {
		t.Fatal("sample longer than the video")
	}
}
