package modules

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"azml-go/helper/telegram"
	"azml-go/types"
)

func TestParseNewFlags(t *testing.T) {
	opt, link := parseLeechArgs("/l https://a.com/f.mkv -t https://t.me/c/123/45 -sp 1.5GB -ea m4a -cv mp4 -ca mp3 -j -u bob -p secret -h Referer: https://x.com Cookie: a=b -z")
	if link != "https://a.com/f.mkv" || opt.ThumbURL != "https://t.me/c/123/45" || opt.SplitSize != int64(1.5*(1<<30)) ||
		opt.ExtractAud != "m4a" || opt.ConvVideo != "mp4" || opt.ConvAudio != "mp3" || !opt.Join || opt.Username != "bob" ||
		opt.Password != "secret" || opt.IsExtract || !opt.IsZip {
		t.Fatalf("bad opts: %+v link=%s", opt, link)
	}
	h := taskHeaders(opt.Headers, opt.Username, opt.Password)
	if len(h) != 3 || h[0] != "Referer: https://x.com" || h[1] != "Cookie: a=b" || !strings.HasPrefix(h[2], "Authorization: Basic ") {
		t.Fatalf("headers %q (raw %q)", h, opt.Headers)
	}
	if o, _ := parseLeechArgs("/l link -ea"); o.ExtractAud != "mp3" {
		t.Fatal("-ea default")
	}
	if got := multiFollowText("/l https://a.b -i 5 -z -n x"); got != "/l -z -n x" {
		t.Fatalf("follow text %q", got)
	}
	if parseSizeArg("500MB") != 500<<20 || parseSizeArg("2G") != 2<<30 {
		t.Fatal("sizes")
	}
	if strings.Contains(leechHelpForTest(), "%!") {
		t.Fatal("help fmt")
	}
	if plain, _ := telegram.ParseFormatting(leechHelpForTest()); plain == leechHelpForTest() {
		t.Fatal("help html")
	}
}

func makeVideo(t *testing.T, dir string) string {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	srt := filepath.Join(dir, "s.srt")
	_ = os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:02,000\nhi\n"), 0644)
	v := filepath.Join(dir, "movie.mkv")
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=3:size=160x120:rate=10",
		"-f", "lavfi", "-i", "sine=duration=3", "-i", srt,
		"-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-c:a", "aac", "-c:s", "srt", v).CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg: %v %s", err, out)
	}
	return v
}

func TestMediaTools(t *testing.T) {
	dir := t.TempDir()
	v := makeVideo(t, dir)
	st, err := extractAllStreams(v, filepath.Join(dir, "st"))
	if err != nil {
		t.Fatal(err)
	}
	files := mediaFiles(st)
	if len(files) != 3 {
		t.Fatalf("-st files: %v", files)
	}

	d2 := t.TempDir()
	v2 := makeVideo(t, d2)
	if p := extractAudio(v2, "m4a"); filepath.Ext(p) != ".m4a" {
		t.Fatalf("-ea gave %s", p)
	}

	d3 := t.TempDir()
	v3 := makeVideo(t, d3)
	if p := convertMedia(v3, "mp4", ""); filepath.Ext(p) != ".mp4" {
		t.Fatalf("-cv gave %s", p)
	}

	d4 := t.TempDir()
	data := []byte(strings.Repeat("abc", 1000))
	_ = os.WriteFile(filepath.Join(d4, "f.bin.001"), data[:1000], 0644)
	_ = os.WriteFile(filepath.Join(d4, "f.bin.002"), data[1000:], 0644)
	joinParts(d4)
	if b, err := os.ReadFile(filepath.Join(d4, "f.bin")); err != nil || string(b) != string(data) {
		t.Fatalf("-j: %v", err)
	}
}

func TestIsTorrentInput(t *testing.T) {
	for l, want := range map[string]bool{
		"magnet:?xt=urn:btih:abc":      true,
		"https://x.com/a.torrent?dl=1": true,
		"https://x.com/movie.mkv":      false,
		"https://mega.nz/file/abc#def": false,
	} {
		if got := isTorrentInput(l, nil); got != want {
			t.Errorf("%s: %v", l, got)
		}
	}
	if !isTorrentInput("", &types.MessageContext{FileName: "Ubuntu.TORRENT"}) {
		t.Error("torrent file")
	}
}

func TestLocalTorrentArg(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.torrent")
	_ = os.WriteFile(p, []byte("d4:infod4:name1:aee"), 0644)
	if _, link := parseLeechArgs("/leech " + p); link != p {
		t.Fatalf("local torrent not taken: %q", link)
	}
	if !isTorrentInput(p, nil) {
		t.Fatal("must be blocked when torrents are disabled")
	}
}

func TestSameDirFlag(t *testing.T) {
	opt, link := parseLeechArgs("/l -i 3 -m Season 1 -z")
	if opt.SameDir != "Season 1" || !opt.IsZip || opt.AsMedia || link != "" || opt.Multi != 3 {
		t.Fatalf("%+v", opt)
	}
	if o, _ := parseLeechArgs("/l link -med"); !o.AsMedia {
		t.Fatal("-med")
	}
}

func TestSameDirGroup(t *testing.T) {
	dir := t.TempDir()
	key := newSameDir("Show", 2)
	a := filepath.Join(dir, "a.mkv")
	_ = os.WriteFile(a, []byte("x"), 0644)
	if f, err := sameDirAdd(key, a); err != nil || f != "" {
		t.Fatalf("first: %q %v", f, err)
	}
	b := filepath.Join(dir, "a.mkv")
	_ = os.WriteFile(b, []byte("y"), 0644)
	f, err := sameDirAdd(key, b)
	if err != nil || f == "" {
		t.Fatalf("last: %q %v", f, err)
	}
	if n := len(mediaFiles(f)); n != 2 {
		t.Fatalf("files in folder: %d", n)
	}
	_ = os.RemoveAll(filepath.Dir(f))
}

func leechHelpForTest() string {
	var sb strings.Builder
	for _, k := range []string{"leech", "mirror", "ytdl"} {
		t, _ := cmdHelpMenu(k, 1, 0)
		sb.WriteString(t)
		_, _, args := helpKind(k)
		for i := range args {
			it, _ := cmdHelpItem(k, 1, i, 0)
			sb.WriteString(it)
		}
	}
	return sb.String()
}
