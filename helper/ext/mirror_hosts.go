package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MirrorHost is a place /mirror can upload to.
type MirrorHost struct {
	ID   string
	Name string
}

// MirrorHosts is the list shown to the user (add a host here and in UploadToHost).
var MirrorHosts = []MirrorHost{
	{ID: "gofile", Name: "GoFile"},
	{ID: "buzzheavier", Name: "BuzzHeavier"},
}

// BuzzHeavierUploadURL is the upload base (overridable in tests).
var BuzzHeavierUploadURL = "https://w.buzzheavier.com"

// FindMirrorHost returns the host for an id or name (case-insensitive).
func FindMirrorHost(s string) (MirrorHost, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, h := range MirrorHosts {
		if s == h.ID || s == strings.ToLower(h.Name) {
			return h, true
		}
	}
	return MirrorHost{}, false
}

// UploadToHost uploads a file to the chosen host and returns the link.
func UploadToHost(ctx context.Context, hostID, path string, onProgress func(sent, total int64)) (string, error) {
	switch hostID {
	case "gofile":
		return UploadToGoFile(ctx, path, onProgress)
	case "buzzheavier":
		return uploadToBuzzHeavier(ctx, path, onProgress)
	}
	return "", fmt.Errorf("unknown upload host %q", hostID)
}

func uploadToBuzzHeavier(ctx context.Context, path string, onProgress func(sent, total int64)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	total := info.Size()

	var sent int64
	body := &progressReader{r: f, onRead: func(n int) {
		sent += int64(n)
		if onProgress != nil {
			onProgress(sent, total)
		}
	}}
	endpoint := strings.TrimRight(BuzzHeavierUploadURL, "/") + "/" + url.PathEscape(filepath.Base(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, body)
	if err != nil {
		return "", err
	}
	req.ContentLength = total
	client := &http.Client{Timeout: 6 * time.Hour}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var out struct {
		Code int `json:"code"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("buzzheavier: bad response (HTTP %d): %w", resp.StatusCode, err)
	}
	if out.Data.ID == "" {
		return "", fmt.Errorf("buzzheavier: upload failed (code %d, HTTP %d)", out.Code, resp.StatusCode)
	}
	return "https://buzzheavier.com/" + out.Data.ID, nil
}
