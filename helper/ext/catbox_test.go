package ext

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatboxAndLitterbox(t *testing.T) {
	var gotType, gotTime string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		if _, _, err := r.FormFile("fileToUpload"); err != nil {
			http.Error(w, "no file", 400)
			return
		}
		gotType, gotTime = r.FormValue("reqtype"), r.FormValue("time")
		_, _ = w.Write([]byte("https://files.catbox.moe/abc123.mkv"))
	}))
	defer srv.Close()
	oldC, oldL := CatboxAPI, LitterboxAPI
	CatboxAPI, LitterboxAPI = srv.URL, srv.URL
	defer func() { CatboxAPI, LitterboxAPI = oldC, oldL }()

	link, err := UploadToHost(context.Background(), "catbox", testFile(t, "a.mkv", 100), nil, nil)
	if err != nil || link != "https://files.catbox.moe/abc123.mkv" || gotType != "fileupload" {
		t.Fatalf("catbox link=%q err=%v type=%q", link, err, gotType)
	}
	if _, err := UploadToHost(context.Background(), "litterbox", testFile(t, "b.mkv", 100), nil, nil); err != nil || gotTime != "72h" {
		t.Fatalf("litterbox err=%v time=%q", err, gotTime)
	}
}
