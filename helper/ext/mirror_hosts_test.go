package ext

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azml-go/config"
)

func testFile(t *testing.T, name string, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(strings.Repeat("z", size)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindMirrorHost(t *testing.T) {
	if h, ok := FindMirrorHost(" GoFile "); !ok || h.ID != "gofile" {
		t.Fatalf("got %v %v", h, ok)
	}
	if _, ok := FindMirrorHost("nope"); ok {
		t.Fatal("unexpected host")
	}
}

func TestKeyRequired(t *testing.T) {
	cfg := config.Get()
	old := cfg.BuzzHeavierToken
	cfg.BuzzHeavierToken = ""
	defer func() { cfg.BuzzHeavierToken = old }()
	if _, err := UploadToHost(context.Background(), "buzzheavier", testFile(t, "a", 1), nil, nil); err == nil || !strings.Contains(err.Error(), "BUZZHEAVIER_TOKEN") {
		t.Fatalf("expected missing key error, got %v", err)
	}
	for _, h := range AvailableMirrorHosts() {
		if h.ID == "buzzheavier" {
			t.Fatal("buzzheavier listed without a token")
		}
	}
}

func TestGoFileSameFolder(t *testing.T) {
	calls := 0
	var gotToken, gotFolder string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseMultipartForm(1 << 20)
		gotToken, gotFolder = r.FormValue("token"), r.FormValue("folderId")
		_, _ = w.Write([]byte(`{"status":"ok","data":{"downloadPage":"https://gofile.io/d/page` + string(rune('0'+calls)) + `","guestToken":"gt","parentFolder":"pf"}}`))
	}))
	defer srv.Close()
	old := GoFileUploadURL
	GoFileUploadURL = srv.URL
	defer func() { GoFileUploadURL = old }()

	sess := &MirrorSession{}
	l1, err := UploadToHost(context.Background(), "gofile", testFile(t, "a.mkv", 10), sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := UploadToHost(context.Background(), "gofile", testFile(t, "b.mkv", 10), sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l1 != "https://gofile.io/d/page1" || l2 != l1 || gotToken != "gt" || gotFolder != "pf" {
		t.Fatalf("l1=%q l2=%q token=%q folder=%q", l1, l2, gotToken, gotFolder)
	}
}

func TestPixelDrain(t *testing.T) {
	var user, pass, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ = r.BasicAuth()
		path = r.URL.Path
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"pd1"}`))
	}))
	defer srv.Close()
	old := PixelDrainAPI
	PixelDrainAPI = srv.URL
	defer func() { PixelDrainAPI = old }()
	cfg := config.Get()
	oldKey := cfg.PixelDrainKey
	cfg.PixelDrainKey = "k1"
	defer func() { cfg.PixelDrainKey = oldKey }()

	link, err := UploadToHost(context.Background(), "pixeldrain", testFile(t, "my file.mkv", 100), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://pixeldrain.com/u/pd1" || user != "" || pass != "k1" || path != "/file/my.file.mkv" {
		t.Fatalf("link=%q user=%q pass=%q path=%q", link, user, pass, path)
	}
}

func TestBuzzHeavier(t *testing.T) {
	var auth, method, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, method = r.Header.Get("Authorization"), r.Method
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"code":201,"data":{"id":"bz9"}}`))
	}))
	defer srv.Close()
	old := BuzzHeavierUploadURL
	BuzzHeavierUploadURL = srv.URL
	defer func() { BuzzHeavierUploadURL = old }()
	cfg := config.Get()
	oldKey := cfg.BuzzHeavierToken
	cfg.BuzzHeavierToken = "tok"
	defer func() { cfg.BuzzHeavierToken = oldKey }()

	var last int64
	link, err := UploadToHost(context.Background(), "buzzheavier", testFile(t, "a.mkv", 5000), nil, func(s, _ int64) { last = s })
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://buzzheavier.com/bz9" || auth != "Bearer tok" || method != "PUT" || len(body) != 5000 || last != 5000 {
		t.Fatalf("link=%q auth=%q method=%q len=%d progress=%d", link, auth, method, len(body), last)
	}
}

func TestDevUploads(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/upload/server", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":200,"sess_id":"s1","result":"` + srv.URL + `/up"}`))
	})
	var sess string
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		sess = r.FormValue("sess_id")
		_, _ = w.Write([]byte(`[{"file_code":"dv7"}]`))
	})
	old := DevUploadsAPI
	DevUploadsAPI = srv.URL
	defer func() { DevUploadsAPI = old }()
	cfg := config.Get()
	oldKey := cfg.DevUploadsKey
	cfg.DevUploadsKey = "dk"
	defer func() { cfg.DevUploadsKey = oldKey }()

	link, err := UploadToHost(context.Background(), "devuploads", testFile(t, "a.mkv", 10), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://devuploads.com/dv7" || sess != "s1" {
		t.Fatalf("link=%q sess=%q", link, sess)
	}
}

func TestVikingFile(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/get-server", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"server":"` + srv.URL + `/up"}`))
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		_, _ = w.Write([]byte(`{"url":"https://vikingfile.com/f/vk1"}`))
	})
	old := VikingFileAPI
	VikingFileAPI = srv.URL
	defer func() { VikingFileAPI = old }()

	link, err := UploadToHost(context.Background(), "vikingfile", testFile(t, "a.mkv", 10), nil, nil)
	if err != nil || link != "https://vikingfile.com/f/vk1" {
		t.Fatalf("link=%q err=%v", link, err)
	}
}

func TestStreamTape(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/file/ul", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("login") != "me" || r.URL.Query().Get("key") != "sk" {
			_, _ = w.Write([]byte(`{"status":403}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":200,"result":{"url":"` + srv.URL + `/up"}}`))
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		if _, _, err := r.FormFile("file1"); err != nil {
			http.Error(w, "no file", 400)
			return
		}
		_, _ = w.Write([]byte(`{"status":200,"result":{"url":"https://streamtape.com/v/st1/a.mkv","id":"st1"}}`))
	})
	old := StreamTapeAPIURL
	StreamTapeAPIURL = srv.URL
	defer func() { StreamTapeAPIURL = old }()
	cfg := config.Get()
	oldKey := cfg.StreamTapeAPI
	cfg.StreamTapeAPI = "me:sk"
	defer func() { cfg.StreamTapeAPI = oldKey }()

	link, err := UploadToHost(context.Background(), "streamtape", testFile(t, "a.mkv", 10), nil, nil)
	if err != nil || link != "https://streamtape.com/v/st1/a.mkv" {
		t.Fatalf("link=%q err=%v", link, err)
	}
}
