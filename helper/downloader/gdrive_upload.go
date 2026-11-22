package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TJ GDrive (tjgd): the upload of /mirror -up gd, like gdriveTools.py of the Python bot:
// token.pickle, GDRIVE_ID (a folder or a Team Drive), anyone-with-link permission unless
// IS_TEAM_DRIVE, STOP_DUPLICATE, and the INDEX_URL link.

// GDUploadOptions: what the bot settings say.
type GDUploadOptions struct {
	ParentID      string        // GDRIVE_ID
	IsTeamDrive   bool          // IS_TEAM_DRIVE: no public permission
	Private       bool          // DISABLE_DRIVE_LINK: the files stay private (only the index reads them)
	StopDuplicate bool          // STOP_DUPLICATE
	IndexURL      string        // INDEX_URL
	Client        *GDriveClient // a user's own token.pickle; nil = the bot's token.pickle
}

func (o GDUploadOptions) client() *GDriveClient {
	if o.Client != nil {
		return o.Client
	}
	return GetGDriveClient()
}

// GDSession: one task. With a folder name, all its files go into one new Drive folder.
type GDSession struct {
	FolderName string
	folderID   string
}

// GDResult: the links of an upload. For a folder they are the folder's links.
type GDResult struct {
	DriveLink string
	IndexLink string
}

// the Drive API (variables for the tests)
var (
	gdAPI       = "https://www.googleapis.com/drive/v3/files"
	gdUploadAPI = "https://www.googleapis.com/upload/drive/v3/files"
	gdChunk     = int64(32 << 20) // a multiple of 256 KiB
)

type gdFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
}

func gdDo(ctx context.Context, gc *GDriveClient, method, endpoint string, body interface{}, out interface{}) error {
	token, err := gc.GetAccessToken()
	if err != nil {
		return fmt.Errorf("token.pickle: %v", err)
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("drive %s: HTTP %d %s", method, resp.StatusCode, gdErrText(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func gdErrText(b []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func gdQuote(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`) }

// gdFind: a file / folder of that name in the parent (STOP_DUPLICATE).
func gdFind(ctx context.Context, gc *GDriveClient, parent, name string) (*gdFile, error) {
	q := url.Values{}
	q.Set("q", fmt.Sprintf("name = '%s' and '%s' in parents and trashed = false", gdQuote(name), gdQuote(parent)))
	q.Set("fields", "files(id,name,mimeType)")
	q.Set("supportsAllDrives", "true")
	q.Set("includeItemsFromAllDrives", "true")
	var res struct {
		Files []gdFile `json:"files"`
	}
	if err := gdDo(ctx, gc, http.MethodGet, gdAPI+"?"+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	if len(res.Files) == 0 {
		return nil, nil
	}
	return &res.Files[0], nil
}

func gdCreateFolder(ctx context.Context, gc *GDriveClient, parent, name string) (string, error) {
	var f gdFile
	err := gdDo(ctx, gc, http.MethodPost, gdAPI+"?supportsAllDrives=true&fields=id",
		map[string]interface{}{"name": name, "mimeType": "application/vnd.google-apps.folder", "parents": []string{parent}}, &f)
	return f.ID, err
}

func gdSetPublic(ctx context.Context, gc *GDriveClient, id string) {
	_ = gdDo(ctx, gc, http.MethodPost, gdAPI+"/"+id+"/permissions?supportsAllDrives=true",
		map[string]string{"role": "reader", "type": "anyone"}, nil)
}

func gdLinks(o GDUploadOptions, id string, folder bool, indexPath string) GDResult {
	r := GDResult{DriveLink: "https://drive.google.com/file/d/" + id + "/view?usp=drivesdk"}
	if folder {
		r.DriveLink = "https://drive.google.com/drive/folders/" + id
	}
	if base := strings.TrimRight(strings.TrimSpace(o.IndexURL), "/"); base != "" {
		parts := strings.Split(indexPath, "/")
		for i, p := range parts {
			parts[i] = url.PathEscape(p)
		}
		r.IndexLink = base + "/" + strings.Join(parts, "/")
		if folder {
			r.IndexLink += "/"
		}
	}
	return r
}

// UploadToGDrive: one file of the task. In a session with a folder name the first file makes
// the folder and every file returns the folder's links.
func UploadToGDrive(ctx context.Context, path string, o GDUploadOptions, sess *GDSession, onProgress func(sent, total int64)) (GDResult, error) {
	if strings.TrimSpace(o.ParentID) == "" {
		return GDResult{}, fmt.Errorf("GDRIVE_ID not Provided!")
	}
	if sess == nil {
		sess = &GDSession{}
	}
	parent := o.ParentID
	name := filepath.Base(path)
	if sess.FolderName != "" {
		if sess.folderID == "" {
			if o.StopDuplicate {
				if f, err := gdFind(ctx, o.client(), o.ParentID, sess.FolderName); err == nil && f != nil {
					return GDResult{}, fmt.Errorf("already in Drive: %s", sess.FolderName)
				}
			}
			id, err := gdCreateFolder(ctx, o.client(), o.ParentID, sess.FolderName)
			if err != nil {
				return GDResult{}, err
			}
			if !o.IsTeamDrive && !o.Private {
				gdSetPublic(ctx, o.client(), id)
			}
			sess.folderID = id
		}
		parent = sess.folderID
	} else if o.StopDuplicate {
		if f, err := gdFind(ctx, o.client(), parent, name); err == nil && f != nil {
			return GDResult{}, fmt.Errorf("already in Drive: %s", name)
		}
	}

	id, err := gdUploadFile(ctx, o.client(), path, name, parent, onProgress)
	if err != nil {
		return GDResult{}, err
	}
	if sess.FolderName != "" {
		return gdLinks(o, sess.folderID, true, sess.FolderName), nil
	}
	if !o.IsTeamDrive && !o.Private {
		gdSetPublic(ctx, o.client(), id)
	}
	return gdLinks(o, id, false, name), nil
}

// gdUploadFile: a resumable upload, sent in parts (each part is retried; the access token is
// renewed while a long upload runs).
func gdUploadFile(ctx context.Context, gc *GDriveClient, path, name, parent string, onProgress func(sent, total int64)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()

	token, err := gc.GetAccessToken()
	if err != nil {
		return "", fmt.Errorf("token.pickle: %v", err)
	}
	meta, _ := json.Marshal(map[string]interface{}{"name": name, "parents": []string{parent}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		gdUploadAPI+"?uploadType=resumable&supportsAllDrives=true&fields=id", bytes.NewReader(meta))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Length", fmt.Sprintf("%d", size))
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	session := resp.Header.Get("Location")
	if resp.StatusCode >= 300 || session == "" {
		return "", fmt.Errorf("drive upload start: HTTP %d %s", resp.StatusCode, gdErrText(data))
	}

	chunk := gdChunk
	client := &http.Client{Timeout: 30 * time.Minute}
	var sent int64
	for {
		end := sent + chunk
		if end > size {
			end = size
		}
		var lastErr error
		var id string
		done := false
		for attempt := 0; attempt < 5; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(time.Duration(attempt*3) * time.Second):
				}
			}
			body := io.NewSectionReader(f, sent, end-sent)
			var rd io.Reader = body
			if onProgress != nil {
				base := sent
				rd = &gdProgress{r: body, f: func(n int64) { onProgress(base+n, size) }}
			}
			preq, err := http.NewRequestWithContext(ctx, http.MethodPut, session, rd)
			if err != nil {
				return "", err
			}
			preq.ContentLength = end - sent
			if size > 0 {
				preq.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", sent, end-1, size))
			}
			presp, err := client.Do(preq)
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				lastErr = err
				continue
			}
			pdata, _ := io.ReadAll(io.LimitReader(presp.Body, 1<<20))
			presp.Body.Close()
			switch {
			case presp.StatusCode == 200 || presp.StatusCode == 201:
				var res gdFile
				_ = json.Unmarshal(pdata, &res)
				id, done = res.ID, true
			case presp.StatusCode == 308: // this part is in, the next one
			default:
				lastErr = fmt.Errorf("drive upload: HTTP %d %s", presp.StatusCode, gdErrText(pdata))
				if presp.StatusCode < 500 && presp.StatusCode != 429 {
					return "", lastErr
				}
				continue
			}
			lastErr = nil
			break
		}
		if lastErr != nil {
			return "", lastErr
		}
		sent = end
		if done {
			if id == "" {
				return "", fmt.Errorf("drive upload: no file id")
			}
			return id, nil
		}
		if sent >= size {
			return "", fmt.Errorf("drive upload: not finished")
		}
	}
}

type gdProgress struct {
	r io.Reader
	n int64
	f func(int64)
}

func (p *gdProgress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	p.f(p.n)
	return n, err
}
