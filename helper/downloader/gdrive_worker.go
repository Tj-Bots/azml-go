package downloader

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"azml-go/config"
)

// Public Google Drive links without token.pickle: GDRIVE_WORKER (our Cloudflare worker)
// lists files and folders and streams the files, so no Google login is needed.

type workerItem struct {
	Type   string       `json:"type"`
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Size   int64        `json:"size"`
	Mime   string       `json:"mime"`
	Direct string       `json:"direct"`
	Items  []workerItem `json:"items"`
	OK     bool         `json:"ok"`
	Error  string       `json:"error"`
}

func gdriveWorker() string {
	return strings.TrimRight(strings.TrimSpace(config.Get().GDriveWorker), "/")
}

func workerAPI(id string) (*workerItem, error) {
	w := gdriveWorker()
	if w == "" {
		return nil, fmt.Errorf("no token.pickle and GDRIVE_WORKER is off")
	}
	c := &http.Client{Timeout: 60 * time.Second}
	resp, err := c.Get(w + "/api?url=" + url.QueryEscape(id))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var it workerItem
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("gdrive worker: bad answer (HTTP %d)", resp.StatusCode)
	}
	if !it.OK {
		if it.Error == "" {
			it.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("%s", it.Error)
	}
	return &it, nil
}

func workerFileURL(id, name string) string {
	return gdriveWorker() + "/dl/" + id + "/" + url.PathEscape(name)
}

func workerInfo(id string) (*GDriveFile, error) {
	it, err := workerAPI(id)
	if err != nil {
		return nil, err
	}
	return &GDriveFile{ID: it.ID, Name: it.Name, Size: it.Size, MimeType: it.Mime, IsDir: it.Type == "folder"}, nil
}

func workerFolder(folderID, path string, depth int) ([]GDriveFile, error) {
	it, err := workerAPI(folderID)
	if err != nil {
		return nil, err
	}
	var out []GDriveFile
	for _, x := range it.Items {
		if x.Type == "folder" {
			out = append(out, GDriveFile{ID: x.ID, Name: x.Name, Path: path, IsDir: true})
			if depth < 20 {
				sub, _ := workerFolder(x.ID, filepath.Join(path, x.Name), depth+1)
				out = append(out, sub...)
			}
			continue
		}
		out = append(out, GDriveFile{ID: x.ID, Name: x.Name, Size: x.Size, Path: path, Selected: true})
	}
	return out, nil
}
