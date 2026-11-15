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

func TestFindMirrorHost(t *testing.T) {
	if h, ok := FindMirrorHost(" GoFile "); !ok || h.ID != "gofile" {
		t.Fatalf("got %v %v", h, ok)
	}
	if _, ok := FindMirrorHost("nope"); ok {
		t.Fatal("unexpected host")
	}
}

func TestUploadToBuzzHeavier(t *testing.T) {
	var gotPath, gotBody, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody, gotMethod = r.URL.Path, string(b), r.Method
		_, _ = w.Write([]byte(`{"code":201,"data":{"id":"xyz789"}}`))
	}))
	defer srv.Close()
	old := BuzzHeavierUploadURL
	BuzzHeavierUploadURL = srv.URL
	defer func() { BuzzHeavierUploadURL = old }()

	p := filepath.Join(t.TempDir(), "a file.mkv")
	_ = os.WriteFile(p, []byte(strings.Repeat("y", 5000)), 0o600)
	var last int64
	link, err := UploadToHost(context.Background(), "buzzheavier", p, func(sent, total int64) { last = sent })
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://buzzheavier.com/xyz789" || gotMethod != "PUT" || gotPath != "/a file.mkv" || len(gotBody) != 5000 || last != 5000 {
		t.Fatalf("link=%q method=%q path=%q len=%d progress=%d", link, gotMethod, gotPath, len(gotBody), last)
	}
	if _, err := UploadToHost(context.Background(), "nope", p, nil); err == nil {
		t.Fatal("expected unknown host error")
	}
}
