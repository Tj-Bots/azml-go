package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/config"
)

// TorBox (TORBOX_API): magnets / .torrent files (and, on paid plans, hoster links) are downloaded
// by TorBox; the bot then takes the files from TorBox's CDN over HTTPS (no torrent traffic from
// the server). TORBOX_MODE: all / torrents / links. TORBOX_ACCESS: sudo (default: the owner and
// sudo users only, TorBox's terms forbid sharing an account), premium, or all.

var tbBase = "https://api.torbox.app/v1/api" // a var for the tests

var tbHTTP = &http.Client{Timeout: 90 * time.Second}

func tbKey() string { return strings.TrimSpace(config.Get().TorBoxAPI) }

// TorBoxFor: TorBox is set, used for "links" or "torrents", and this user may use it.
func TorBoxFor(kind string, userID int64, isSudo, isPremium bool) bool {
	cfg := config.Get()
	if tbKey() == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.TorBoxMode)) {
	case "torrents", "torrent":
		if kind != "torrents" {
			return false
		}
	case "links", "link":
		if kind != "links" {
			return false
		}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.TorBoxAccess)) {
	case "all":
		return true
	case "premium":
		return isSudo || isPremium
	}
	return isSudo
}

// tbCall: a TorBox API call; the "data" of the answer, or its error / detail.
func tbCall(ctx context.Context, method, path string, q url.Values, body io.Reader, contentType string) (json.RawMessage, error) {
	u := tbBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tbKey())
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := tbHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Success bool            `json:"success"`
		Error   interface{}     `json:"error"`
		Detail  string          `json:"detail"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("TorBox: bad answer (HTTP %d)", resp.StatusCode)
	}
	if !r.Success {
		msg := strings.TrimSpace(r.Detail)
		if msg == "" && r.Error != nil {
			msg = fmt.Sprint(r.Error)
		}
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("TorBox: %s", msg)
	}
	return r.Data, nil
}

func tbForm(fields map[string]string, fileField, filePath string) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if filePath != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return nil, "", err
		}
		fw, _ := mw.CreateFormFile(fileField, filepath.Base(filePath))
		_, _ = fw.Write(b)
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType(), nil
}

// ADFile is one finished file of a debrid download: its folder inside the torrent and a direct link.
type ADFile struct {
	Path string // folder inside the torrent ("" = top)
	Name string
	Size int64
	Link string
}

// tbItem: an item of torrents/mylist or webdl/mylist.
type tbItem struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Size             int64   `json:"size"`
	Progress         float64 `json:"progress"`
	DownloadSpeed    float64 `json:"download_speed"`
	DownloadState    string  `json:"download_state"`
	DownloadFinished bool    `json:"download_finished"`
	DownloadPresent  bool    `json:"download_present"`
	Files            []struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		Size      int64  `json:"size"`
	} `json:"files"`
}

// tbWait polls an item (torrents or webdl) every 5 seconds until it is on TorBox's servers.
func tbWait(ctx context.Context, kind string, id int64, onProgress func(done, total int64, speed float64, name string)) (*tbItem, error) {
	deadline := time.Now().Add(12 * time.Hour)
	for {
		data, err := tbCall(ctx, "GET", "/"+kind+"/mylist", url.Values{"id": {strconv.FormatInt(id, 10)}, "bypass_cache": {"true"}}, nil, "")
		if err != nil {
			return nil, err
		}
		var it tbItem
		if json.Unmarshal(data, &it) != nil { // a list when the id is not found as one item
			var list []tbItem
			if json.Unmarshal(data, &list) == nil && len(list) > 0 {
				it = list[0]
			}
		}
		state := strings.ToLower(it.DownloadState)
		if it.DownloadFinished && it.DownloadPresent {
			if onProgress != nil {
				onProgress(it.Size, it.Size, 0, it.Name)
			}
			return &it, nil
		}
		if strings.Contains(state, "error") || strings.Contains(state, "failed") {
			return nil, fmt.Errorf("TorBox: %s", it.DownloadState)
		}
		if onProgress != nil {
			p := it.Progress
			if p > 1 { // some answers give percent
				p /= 100
			}
			onProgress(int64(p*float64(it.Size)), it.Size, it.DownloadSpeed, it.Name)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("TorBox: not ready in time (%s)", it.DownloadState)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// tbFiles: a direct (CDN) link for every file of a finished item.
func tbFiles(ctx context.Context, kind string, it *tbItem) ([]ADFile, error) {
	idParam, path := "torrent_id", "/torrents/requestdl"
	if kind == "webdl" {
		idParam, path = "web_id", "/webdl/requestdl"
	}
	var out []ADFile
	for _, f := range it.Files {
		q := url.Values{"token": {tbKey()}, idParam: {strconv.FormatInt(it.ID, 10)}, "file_id": {strconv.FormatInt(f.ID, 10)}}
		data, err := tbCall(ctx, "GET", path, q, nil, "")
		if err != nil {
			return nil, err
		}
		var link string
		if err := json.Unmarshal(data, &link); err != nil || link == "" {
			return nil, fmt.Errorf("TorBox: no link for %s", f.Name)
		}
		dir, name := filepath.Dir(filepath.ToSlash(f.Name)), f.ShortName
		if name == "" {
			name = filepath.Base(f.Name)
		}
		if dir == "." {
			dir = ""
		}
		// the files of a torrent sit in a folder of its name: keep only what is under it
		if rest := strings.TrimPrefix(dir, it.Name); rest != dir {
			dir = strings.TrimPrefix(rest, "/")
		}
		out = append(out, ADFile{Path: dir, Name: name, Size: f.Size, Link: link})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("TorBox: no files")
	}
	return out, nil
}

// TorBoxTorrent: a magnet (or a local .torrent file) downloaded by TorBox; the files with direct
// links, a cleanup (removes it from the account) and its name.
func TorBoxTorrent(ctx context.Context, magnetOrFile string, onProgress func(done, total int64, speed float64, name string)) ([]ADFile, func(), string, error) {
	fields := map[string]string{"seed": "3", "allow_zip": "false"} // 3 = no seeding
	file := ""
	if fi, err := os.Stat(magnetOrFile); err == nil && !fi.IsDir() {
		file = magnetOrFile
	} else {
		fields["magnet"] = magnetOrFile
	}
	body, ct, err := tbForm(fields, "file", file)
	if err != nil {
		return nil, nil, "", err
	}
	data, err := tbCall(ctx, "POST", "/torrents/createtorrent", nil, body, ct)
	if err != nil {
		return nil, nil, "", err
	}
	var cr struct {
		TorrentID *int64 `json:"torrent_id"`
		QueuedID  *int64 `json:"queued_id"`
	}
	_ = json.Unmarshal(data, &cr)
	if cr.TorrentID == nil {
		if cr.QueuedID != nil { // no free slot: it waits on TorBox, not here
			tbDelete("/torrents/controlqueued", map[string]interface{}{"queued_id": *cr.QueuedID, "operation": "delete"})
			return nil, nil, "", fmt.Errorf("TorBox: all the download slots of the account are busy, try again later")
		}
		return nil, nil, "", fmt.Errorf("TorBox: the torrent was not added")
	}
	id := *cr.TorrentID
	cleanup := func() {
		tbDelete("/torrents/controltorrent", map[string]interface{}{"torrent_id": id, "operation": "delete"})
	}
	it, err := tbWait(ctx, "torrents", id, onProgress)
	if err != nil {
		cleanup()
		return nil, nil, "", err
	}
	files, err := tbFiles(ctx, "torrents", it)
	if err != nil {
		cleanup()
		return nil, nil, it.Name, err
	}
	return files, cleanup, it.Name, nil
}

// TorBoxLink: a hoster link downloaded by TorBox (web downloads, paid plans); its direct link
// and a cleanup to call after the bot has the file. "" when TorBox can't take it.
func TorBoxLink(ctx context.Context, link string) (string, func(), error) {
	body, ct, err := tbForm(map[string]string{"link": link}, "", "")
	if err != nil {
		return "", nil, err
	}
	data, err := tbCall(ctx, "POST", "/webdl/createwebdownload", nil, body, ct)
	if err != nil {
		return "", nil, err
	}
	var cr struct {
		ID json.Number `json:"webdownload_id"`
	}
	_ = json.Unmarshal(data, &cr)
	id, _ := cr.ID.Int64()
	if id == 0 {
		return "", nil, fmt.Errorf("TorBox: the link was not added")
	}
	cleanup := func() {
		tbDelete("/webdl/controlwebdownload", map[string]interface{}{"webdl_id": id, "operation": "delete"})
	}
	it, err := tbWait(ctx, "webdl", id, nil)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	files, err := tbFiles(ctx, "webdl", it)
	if err != nil || len(files) == 0 {
		cleanup()
		return "", nil, err
	}
	return files[0].Link, cleanup, nil
}

func tbDelete(path string, body map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, _ := json.Marshal(body)
	_, _ = tbCall(ctx, "POST", path, nil, bytes.NewReader(b), "application/json")
}

var (
	tbHostsMu sync.Mutex
	tbHosts   []string
	tbHostsAt time.Time
)

// IsDebridHost: the link is on a hoster TorBox supports (webdl/hosters, kept 6 hours). Plain
// direct links (archive.org ...) are downloaded by the bot itself, not through TorBox.
func IsDebridHost(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	tbHostsMu.Lock()
	defer tbHostsMu.Unlock()
	if time.Since(tbHostsAt) > 6*time.Hour {
		tbHostsAt = time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		data, err := tbCall(ctx, "GET", "/webdl/hosters", nil, nil, "")
		cancel()
		if err == nil {
			var list []struct {
				Domains []string `json:"domains"`
				Status  bool     `json:"status"`
			}
			if json.Unmarshal(data, &list) == nil {
				tbHosts = tbHosts[:0]
				for _, h := range list {
					if !h.Status {
						continue
					}
					for _, d := range h.Domains {
						tbHosts = append(tbHosts, strings.TrimPrefix(strings.ToLower(d), "www."))
					}
				}
			}
		}
	}
	for _, d := range tbHosts {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
