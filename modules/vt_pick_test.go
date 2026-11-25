package modules

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSubsAndLabels(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	d := t.TempDir()
	srt := filepath.Join(d, "a.srt")
	_ = os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), 0o644)
	v := filepath.Join(d, "v.mkv")
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=5",
		"-f", "lavfi", "-i", "sine=duration=2", "-f", "lavfi", "-i", "sine=duration=2", "-i", srt, "-i", srt,
		"-map", "0", "-map", "1", "-map", "2", "-map", "3", "-map", "4", "-c:v", "libx264", "-c:a", "aac", "-ac:a:1", "6", "-c:s", "srt",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=heb", "-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=heb", v).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var labels []string
	for _, s := range probeStreams(v) {
		if s.CodecType == "audio" {
			labels = append(labels, trackLabel(s))
		}
	}
	if strings.Join(labels, "|") != "English · AAC 2.0|Hebrew · AAC 5.1" && strings.Join(labels, "|") != "English · AAC 1.0|Hebrew · AAC 5.1" {
		t.Fatalf("labels %v", labels)
	}
	vtDefaultSubs(v, "2")
	pr, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "s", "-show_entries", "stream_disposition=default", "-of", "csv=p=0", v).Output()
	if got := strings.Fields(string(pr)); len(got) != 2 || got[0] != "0" || got[1] != "1" {
		t.Fatalf("dispositions %q", string(pr))
	}
}
