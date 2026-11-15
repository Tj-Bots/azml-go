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
)

func TestUploadToGoFile(t *testing.T) {
	var gotName, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		b, _ := io.ReadAll(file)
		gotName, gotBody = hdr.Filename, string(b)
		_, _ = w.Write([]byte(`{"status":"ok","data":{"downloadPage":"https://gofile.io/d/abc123"}}`))
	}))
	defer srv.Close()
	old := GoFileUploadURL
	GoFileUploadURL = srv.URL
	defer func() { GoFileUploadURL = old }()

	p := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(p, []byte(strings.Repeat("x", 100000)), 0o600); err != nil {
		t.Fatal(err)
	}
	var last int64
	link, err := UploadToGoFile(context.Background(), p, func(sent, total int64) { last = sent })
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://gofile.io/d/abc123" || gotName != "movie.mkv" || len(gotBody) != 100000 || last != 100000 {
		t.Fatalf("link=%q name=%q bodyLen=%d progress=%d", link, gotName, len(gotBody), last)
	}
}

func TestUploadToGoFileError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error-rateLimit","data":{}}`))
	}))
	defer srv.Close()
	old := GoFileUploadURL
	GoFileUploadURL = srv.URL
	defer func() { GoFileUploadURL = old }()
	p := filepath.Join(t.TempDir(), "a.bin")
	_ = os.WriteFile(p, []byte("hi"), 0o600)
	if _, err := UploadToGoFile(context.Background(), p, nil); err == nil {
		t.Fatal("expected an error")
	}
}
