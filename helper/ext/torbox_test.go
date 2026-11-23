package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azml-go/config"
)

func TestTorBoxFlow(t *testing.T) {
	polls, deleted := 0, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer KEY" {
			fmt.Fprint(w, `{"success":false,"detail":"bad token"}`)
			return
		}
		switch r.URL.Path {
		case "/torrents/createtorrent":
			_ = r.ParseMultipartForm(1 << 20)
			if !strings.HasPrefix(r.FormValue("magnet"), "magnet:") || r.FormValue("seed") != "3" {
				fmt.Fprint(w, `{"success":false,"detail":"bad form"}`)
				return
			}
			fmt.Fprint(w, `{"success":true,"data":{"torrent_id":42,"hash":"abc"}}`)
		case "/torrents/mylist":
			polls++
			fin := polls > 1
			fmt.Fprintf(w, `{"success":true,"data":{"id":42,"name":"Show S01","size":300,"progress":0.5,"download_speed":10,"download_state":"downloading","download_finished":%v,"download_present":%v,
				"files":[{"id":0,"name":"Show S01/e1.mkv","short_name":"e1.mkv","size":100},{"id":1,"name":"Show S01/Subs/e1.srt","short_name":"e1.srt","size":200}]}}`, fin, fin)
		case "/torrents/requestdl":
			if r.URL.Query().Get("token") != "KEY" {
				fmt.Fprint(w, `{"success":false,"detail":"no token"}`)
				return
			}
			fmt.Fprintf(w, `{"success":true,"data":"https://cdn.example/%s"}`, r.URL.Query().Get("file_id"))
		case "/torrents/controltorrent":
			var b map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&b)
			deleted = fmt.Sprint(b["operation"], " ", b["torrent_id"])
			fmt.Fprint(w, `{"success":true,"data":null}`)
		case "/webdl/hosters":
			fmt.Fprint(w, `{"success":true,"data":[{"name":"1fichier","domains":["1fichier.com"],"status":true}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := tbBase
	tbBase = srv.URL
	defer func() { tbBase = old }()
	cfg := config.Get()
	ok, om, oa := cfg.TorBoxAPI, cfg.TorBoxMode, cfg.TorBoxAccess
	cfg.TorBoxAPI, cfg.TorBoxMode, cfg.TorBoxAccess = "KEY", "torrents", "sudo"
	defer func() { cfg.TorBoxAPI, cfg.TorBoxMode, cfg.TorBoxAccess = ok, om, oa }()

	if TorBoxFor("links", 1, true, false) || !TorBoxFor("torrents", 1, true, false) || TorBoxFor("torrents", 2, false, true) {
		t.Fatal("mode / access")
	}
	var seen int64
	files, cleanup, name, err := TorBoxTorrent(context.Background(), "magnet:?xt=urn:btih:abc", func(d, _ int64, _ float64, _ string) { seen = d })
	if err != nil || name != "Show S01" || len(files) != 2 {
		t.Fatalf("%v %q %+v", err, name, files)
	}
	if files[0].Path != "" || files[1].Path != "Subs" || files[1].Link != "https://cdn.example/1" || seen != 300 {
		t.Fatalf("files %+v seen %d", files, seen)
	}
	cleanup()
	if deleted != "delete 42" {
		t.Fatalf("deleted %q", deleted)
	}
	if !IsDebridHost("https://1fichier.com/?x") || IsDebridHost("https://archive.org/a.zip") {
		t.Fatal("hosters")
	}
}
