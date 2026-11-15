package modules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azml-go/helper/downloader"
)

func TestParseYtdlArgs(t *testing.T) {
	cases := []struct{ text, link, q string }{
		{"/ytdl https://youtu.be/abc", "https://youtu.be/abc", ""},
		{"/ytdl https://youtu.be/abc 720p", "https://youtu.be/abc", "720p"},
		{"/ytdl 1080 https://youtu.be/abc -n my name", "https://youtu.be/abc", "1080"},
		{"/ytdl https://youtu.be/abc mp3", "https://youtu.be/abc", "mp3"},
		{"/ytdl https://youtu.be/abc -q bv*[height<=480]+ba", "https://youtu.be/abc", "bv*[height<=480]+ba"},
		{"/ytdl", "", ""},
	}
	for _, c := range cases {
		l, q := parseYtdlArgs(c.text)
		if l != c.link || q != c.q {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", c.text, l, q, c.link, c.q)
		}
	}
}

func TestYtQuality(t *testing.T) {
	f, _ := downloader.YtQuality("720p")
	if f != "bv*[height<=720]+ba/b[height<=720]/b" {
		t.Fatalf("720p -> %q", f)
	}
	if f, _ := downloader.YtQuality("best"); f != "bv*+ba/b" {
		t.Fatalf("best -> %q", f)
	}
	if _, a := downloader.YtQuality("mp3"); strings.Join(a, " ") != "-x --audio-format mp3 --audio-quality 320K" {
		t.Fatalf("mp3 args %v", a)
	}
	if f, _ := downloader.YtQuality("bestvideo"); f != "bestvideo" {
		t.Fatalf("raw format changed: %q", f)
	}
}

func TestYtdlMenu(t *testing.T) {
	info := &downloader.YtFormatInfo{Title: "A <b> & c", Duration: 125, Formats: []downloader.YtFormat{
		{ID: "140", Ext: "m4a", VideoExt: "none", AudioExt: "m4a", Acodec: "mp4a", Tbr: 129, Size: 3 << 20},
		{ID: "137", Ext: "mp4", VideoExt: "mp4", Vcodec: "avc1", Acodec: "none", Height: 1080, Fps: 30, Tbr: 4000, Size: 50 << 20},
		{ID: "248", Ext: "webm", VideoExt: "webm", Vcodec: "vp9", Acodec: "none", Height: 1080, Fps: 30, Tbr: 2500},
		{ID: "399", Ext: "mp4", VideoExt: "mp4", Vcodec: "av01", Acodec: "none", Height: 1080, Fps: 30, Tbr: 2000},
		{ID: "sb0", Ext: "mhtml", VideoExt: "none", Acodec: "none"},
	}}
	p := &ytdlPending{info: info, groups: ytdlGroups(info.Formats), uid: 42, key: "abcd", created: time.Now()}
	text, btn := p.mainMenu()
	if !strings.Contains(text, "A &lt;b&gt; &amp; c") || !strings.Contains(text, "Duration") {
		t.Fatalf("bad text: %s", text)
	}
	rows := btn.Rows(2)
	var all []string
	for _, row := range rows {
		if len(row) > 2 {
			t.Fatalf("row with %d buttons: %+v", len(row), row)
		}
		for _, b := range row {
			all = append(all, b.Data)
			if len(b.Data) > 64 {
				t.Errorf("callback too long: %s", b.Data)
			}
		}
	}
	joined := strings.Join(all, "|")
	for _, want := range []string{"ytq 42 abcd dict", "ytq 42 abcd sel", "ytq 42 abcd mp3", "ytq 42 abcd audio", "ytq 42 abcd fmt bv", "ytq 42 abcd fmt ba", "ytq 42 abcd cancel"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	// 1080p30-mp4 has two bit rates -> submenu
	for i, g := range p.groups {
		if g.name == "1080p30-mp4" {
			if len(g.tbrs) != 2 {
				t.Fatalf("1080p30-mp4 rates: %d", len(g.tbrs))
			}
			if !strings.HasPrefix(g.tbrs[0].format, "137+ba[ext=m4a]") {
				t.Fatalf("format: %s", g.tbrs[0].format)
			}
			_, sb := p.bitrateMenu(i)
			if len(sb.Rows(2)) < 2 {
				t.Fatal("bitrate menu rows")
			}
		}
	}
	_, ab := p.audioQualityMenu("flac")
	for _, row := range ab.Rows(5) {
		if len(row) > 5 {
			t.Fatal("audio quality row too long")
		}
	}
	pl := &ytdlPending{info: &downloader.YtFormatInfo{Title: "PL", IsPlaylist: true, Count: 12}, uid: 1, key: "k", created: time.Now()}
	text, pb := pl.mainMenu()
	if !strings.Contains(text, "12 items") {
		t.Fatal("playlist count missing")
	}
	for _, row := range pb.Rows(3) {
		if len(row) > 3 {
			t.Fatal("playlist row too long")
		}
	}
	if f, a := downloader.YtQuality("ba/b-mp3-320"); f != "ba/b" || strings.Join(a, " ") != "-x --audio-format mp3 --audio-quality 320K" {
		t.Fatalf("mp3: %s %v", f, a)
	}
	if _, a := downloader.YtQuality("ba/b-flac-0"); strings.Join(a, " ") != "-x --audio-format flac --audio-quality 0" {
		t.Fatalf("flac: %v", a)
	}
}

func TestYtdlResult(t *testing.T) {
	dir := t.TempDir()
	if ytdlResult(dir, "") != "" {
		t.Fatal("empty dir should give no file")
	}
	one := filepath.Join(dir, "a.mkv")
	_ = os.WriteFile(one, []byte("x"), 0644)
	if ytdlResult(dir, "") != one {
		t.Fatal("single file")
	}
	_ = os.WriteFile(filepath.Join(dir, "b.mkv"), []byte("x"), 0644)
	if ytdlResult(dir, "") != dir {
		t.Fatal("playlist should return folder")
	}
}

func TestSafeYtOpts(t *testing.T) {
	ok, bad := safeYtOpts(`--embed-subs --sub-langs en,he --exec "rm -rf /" -o /etc/x -S res:720 --config-locations /tmp/c --write-thumbnail -x`)
	got := strings.Join(ok, " ")
	if got != "--embed-subs --sub-langs en,he -S res:720 -x" {
		t.Fatalf("allowed: %q", got)
	}
	if !strings.Contains(strings.Join(bad, " "), "--exec") || !strings.Contains(strings.Join(bad, " "), "rm -rf /") {
		t.Fatalf("dropped: %v", bad)
	}
}
