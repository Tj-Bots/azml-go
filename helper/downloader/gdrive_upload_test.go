package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// a fake Drive: resumable upload in parts, folders, permissions, search
func TestUploadToGDriveFake(t *testing.T) {
	var mu sync.Mutex
	got := map[string]int64{}
	var perms, folders []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/upload"):
			w.Header().Set("Location", "http://"+r.Host+"/session/f1")
			w.WriteHeader(200)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/session/"):
			n, _ := io.Copy(io.Discard, r.Body)
			got["bytes"] += n
			var a, b, total int64
			fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &a, &b, &total)
			if b+1 < total {
				w.WriteHeader(308)
				return
			}
			fmt.Fprint(w, `{"id":"FILE1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/permissions"):
			perms = append(perms, r.URL.Path)
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodPost && r.URL.Path == "/files":
			folders = append(folders, "x")
			fmt.Fprint(w, `{"id":"FOLDER1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/files":
			fmt.Fprint(w, `{"files":[]}`)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 400)
		}
	}))
	defer srv.Close()
	gdAPI, gdUploadAPI, gdChunk = srv.URL+"/files", srv.URL+"/upload", 256<<10
	c := GetGDriveClient()
	c.lock.Lock()
	c.accessToken, c.expiryTime = "tok", time.Now().Add(time.Hour)
	c.lock.Unlock()

	dir := t.TempDir()
	p := filepath.Join(dir, "a b.mkv")
	_ = os.WriteFile(p, make([]byte, 700<<10), 0o644) // 3 parts
	o := GDUploadOptions{ParentID: "ROOT", IndexURL: "https://idx.example/0:", StopDuplicate: true}

	res, err := UploadToGDrive(context.Background(), p, o, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["bytes"] != 700<<10 || res.DriveLink != "https://drive.google.com/file/d/FILE1/view?usp=drivesdk" ||
		res.IndexLink != "https://idx.example/0:/a%20b.mkv" || len(perms) != 1 {
		t.Fatalf("file: %+v bytes=%d perms=%v", res, got["bytes"], perms)
	}

	sess := &GDSession{FolderName: "Season 1"}
	r1, err1 := UploadToGDrive(context.Background(), p, o, sess, nil)
	r2, err2 := UploadToGDrive(context.Background(), p, o, sess, nil)
	if err1 != nil || err2 != nil || len(folders) != 1 || r1 != r2 ||
		r1.DriveLink != "https://drive.google.com/drive/folders/FOLDER1" || r1.IndexLink != "https://idx.example/0:/Season%201/" {
		t.Fatalf("folder: %+v %+v %v %v folders=%d", r1, r2, err1, err2, len(folders))
	}
	if _, err := UploadToGDrive(context.Background(), p, GDUploadOptions{}, nil, nil); err == nil {
		t.Fatal("no GDRIVE_ID must fail")
	}
}
