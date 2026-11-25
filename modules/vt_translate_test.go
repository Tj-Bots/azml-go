package modules

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// a fake Google Translate: every line comes back as "HE:<line>"
func fakeTranslate() func() {
	old := translateFn
	translateFn = func(text, lang string) (string, error) {
		lines := strings.Split(text, "\n")
		for i := range lines {
			lines[i] = "HE:" + lines[i]
		}
		return strings.Join(lines, "\n"), nil
	}
	return func() { translateFn = old }
}

func TestTranslateSubs(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	defer fakeTranslate()()
	d, srcDir := t.TempDir(), t.TempDir()
	mk := func(name, text string) string {
		p := filepath.Join(srcDir, name)
		_ = os.WriteFile(p, []byte("1\n00:00:00,000 --> 00:00:01,000\n"+text+"\n\n2\n00:00:01,000 --> 00:00:02,000\nsecond\n"), 0o644)
		return p
	}
	a, b := mk("a.srt", "first eng"), mk("b.srt", "first rus")
	v := filepath.Join(d, "v.mkv")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=5",
		"-i", a, "-i", b, "-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-c:s", "srt",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=rus", v).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	vtTranslateSubs(d, "he", vtTrOpts{Stream: "2", Drop: true})
	var subs []string
	for _, s := range probeStreams(v) {
		if s.CodecType == "subtitle" {
			subs = append(subs, s.Tags["language"]+"|"+s.Tags["title"])
		}
	}
	if len(subs) != 2 || !strings.HasPrefix(subs[0], "eng") || !strings.HasPrefix(subs[1], "heb|Translated rus") && !strings.HasPrefix(subs[1], "he|Translated rus") {
		t.Fatalf("subs %v", subs)
	}
	out, _ := exec.Command("ffmpeg", "-v", "error", "-i", v, "-map", "0:s:1", "-f", "srt", "-").Output()
	if !strings.Contains(string(out), "HE:first rus") || !strings.Contains(string(out), "HE:second") {
		t.Fatalf("translated: %s", out)
	}
}
