package ext

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azml-go/config"
)

func TestAllDebridFlow(t *testing.T) {
	polls, deleted := 0, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Header.Get("Authorization") != "Bearer KEY" {
			fmt.Fprint(w, `{"status":"error","error":{"code":"AUTH_BAD_APIKEY","message":"bad key"}}`)
			return
		}
		switch r.URL.Path {
		case "/v4.1/user/hosts":
			fmt.Fprint(w, `{"status":"success","data":{"hosts":{"1fichier":{"domains":["1fichier.com"]}}}}`)
		case "/v4/link/unlock":
			fmt.Fprintf(w, `{"status":"success","data":{"link":"https://dl.example/%s"}}`, strings.TrimPrefix(r.Form.Get("link"), "https://alldebrid.com/f/"))
		case "/v4/magnet/upload":
			fmt.Fprint(w, `{"status":"success","data":{"magnets":[{"id":7,"name":"Show","ready":false}]}}`)
		case "/v4.1/magnet/status":
			polls++
			code := 1
			if polls > 1 {
				code = 4
			}
			fmt.Fprintf(w, `{"status":"success","data":{"magnets":{"id":7,"filename":"Show S01","size":300,"statusCode":%d,"downloaded":100}}}`, code)
		case "/v4/magnet/files":
			fmt.Fprint(w, `{"status":"success","data":{"magnets":[{"id":"7","files":[{"n":"Show S01","e":[{"n":"e1.mkv","s":100,"l":"https://alldebrid.com/f/a"},{"n":"e2.mkv","s":200,"l":"https://alldebrid.com/f/b"}]}]}]}}`)
		case "/v4/magnet/delete":
			deleted = true
			fmt.Fprint(w, `{"status":"success","data":{"message":"ok"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := adBase
	adBase = srv.URL
	defer func() { adBase = old }()
	cfg := config.Get()
	oldKey, oldMode := cfg.AllDebridAPI, cfg.AllDebridMode
	cfg.AllDebridAPI, cfg.AllDebridMode = "KEY", "torrents"
	defer func() { cfg.AllDebridAPI, cfg.AllDebridMode = oldKey, oldMode }()

	if AllDebridFor("links") || !AllDebridFor("torrents") {
		t.Fatal("mode torrents")
	}
	if d, err := AllDebridUnlock(context.Background(), "https://1fichier.com/?abc"); err != nil || !strings.HasPrefix(d, "https://dl.example/") {
		t.Fatalf("unlock %q %v", d, err)
	}
	if d, _ := AllDebridUnlock(context.Background(), "https://archive.org/x.zip"); d != "" {
		t.Fatalf("not a supported host, got %q", d)
	}
	var seen int64
	files, id, name, err := AllDebridTorrent(context.Background(), "magnet:?xt=urn:btih:abc", func(done, total int64, _ float64, _ string) { seen = done })
	if err != nil || id != 7 || name != "Show S01" || len(files) != 2 {
		t.Fatalf("torrent %v %d %q %+v", err, id, name, files)
	}
	if files[0].Path != "Show S01" || files[1].Link != "https://dl.example/b" || seen != 300 {
		t.Fatalf("files %+v seen %d", files, seen)
	}
	AllDebridDelete(id)
	if !deleted {
		t.Fatal("not deleted")
	}
}
