package modules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	info := &downloader.YtFormatInfo{Title: "A <b> & c", Heights: []int{2160, 1080, 720, 360, 144}, Sizes: map[int]int64{1080: 50 << 20}, AudioSize: 3 << 20, Duration: 125}
	text, btn := buildYtdlMenu(info, 42, "abcd")
	if !strings.Contains(text, "A &lt;b&gt; &amp; c") || !strings.Contains(text, "Duration") {
		t.Fatalf("bad text: %s", text)
	}
	var datas []string
	for _, row := range btn.Rows(2) {
		for _, b := range row {
			datas = append(datas, b.Data)
		}
	}
	all := strings.Join(datas, "|")
	for _, want := range []string{"ytq 42 abcd 2160", "ytq 42 abcd 1080", "ytq 42 abcd best", "ytq 42 abcd audio", "ytq 42 abcd mp3", "ytq 42 abcd cancel"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in %s", want, all)
		}
	}
	for _, d := range datas {
		if len(d) > 64 {
			t.Errorf("callback data too long: %s", d)
		}
	}
	text, _ = buildYtdlMenu(&downloader.YtFormatInfo{Title: "PL", IsPlaylist: true, Count: 12, Sizes: map[int]int64{}}, 1, "k")
	if !strings.Contains(text, "12 items") {
		t.Fatal("playlist count missing")
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
