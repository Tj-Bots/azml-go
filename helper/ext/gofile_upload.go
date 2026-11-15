package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// GoFileUploadURL is the guest upload endpoint (overridable in tests).
var GoFileUploadURL = "https://upload.gofile.io/uploadfile"

// UploadToGoFile uploads one file to GoFile as a guest and returns the download page link.
// onProgress is called with the bytes sent so far and the total size.
func UploadToGoFile(ctx context.Context, path string, onProgress func(sent, total int64)) (string, error) {
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

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, perr := mw.CreateFormFile("file", filepath.Base(path))
		if perr == nil {
			var sent int64
			_, perr = io.Copy(part, &progressReader{r: f, onRead: func(n int) {
				sent += int64(n)
				if onProgress != nil {
					onProgress(sent, total)
				}
			}})
		}
		if perr == nil {
			perr = mw.Close()
		}
		_ = pw.CloseWithError(perr)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GoFileUploadURL, pr)
	if err != nil {
		_ = pr.Close()
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 6 * time.Hour}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var out struct {
		Status string `json:"status"`
		Data   struct {
			DownloadPage string `json:"downloadPage"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("gofile: bad response (HTTP %d): %w", resp.StatusCode, err)
	}
	if out.Status != "ok" || out.Data.DownloadPage == "" {
		return "", fmt.Errorf("gofile: upload failed (status %q, HTTP %d)", out.Status, resp.StatusCode)
	}
	return out.Data.DownloadPage, nil
}

type progressReader struct {
	r      io.Reader
	onRead func(n int)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.onRead(n)
	}
	return n, err
}
