package modules

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"azml-go/helper/ext"
)

// a fake Gemini: every line comes back as "HE:<line>"
func fakeGemini(t *testing.T) func() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		p := body.Contents[0].Parts[0].Text
		var lines []string
		_ = json.Unmarshal([]byte(p[strings.LastIndex(p, "\n\n")+2:]), &lines)
		for i := range lines {
			lines[i] = "HE:" + lines[i]
		}
		out, _ := json.Marshal(lines)
		res, _ := json.Marshal(map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{map[string]string{"text": string(out)}}}}}})
		fmt.Fprint(w, string(res))
	}))
	old := ext.GeminiBaseURL
	ext.GeminiBaseURL = srv.URL
	return func() { ext.GeminiBaseURL = old; srv.Close() }
}

func TestTranslateSubsGemini(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	defer fakeGemini(t)()
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
	vtTranslateSubs(d, "he", vtTrOpts{Key: "KEY", Stream: "2", Drop: true})
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
