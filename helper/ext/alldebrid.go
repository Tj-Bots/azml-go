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

// AllDebrid (ALLDEBRID_API): hoster links become fast direct links, and magnets / .torrent files
// are downloaded by AllDebrid's servers (the bot only downloads the finished files over HTTPS:
// no torrent traffic from the server). ALLDEBRID_MODE: all (default), torrents or links.

var adBase = "https://api.alldebrid.com" // a var for the tests

var adHTTP = &http.Client{Timeout: 60 * time.Second}

// AllDebridFor: AllDebrid is set and used for "links" or "torrents".
func AllDebridFor(kind string) bool {
	cfg := config.Get()
	if strings.TrimSpace(cfg.AllDebridAPI) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.AllDebridMode)) {
	case "torrents", "torrent":
		return kind == "torrents"
	case "links", "link":
		return kind == "links"
	}
	return true
}

type adError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *adError) Error() string { return "AllDebrid: " + e.Message }

// adCall: a POST to the API with form values (or a multipart body), the "data" of the answer.
func adCall(ctx context.Context, path string, form url.Values, body io.Reader, contentType string) (json.RawMessage, error) {
	if body == nil {
		body = strings.NewReader(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", adBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(config.Get().AllDebridAPI))
	req.Header.Set("Content-Type", contentType)
	resp, err := adHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
		Error  *adError        `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("AllDebrid: bad answer (HTTP %d)", resp.StatusCode)
	}
	if r.Status != "success" {
		if r.Error != nil {
			return nil, r.Error
		}
		return nil, fmt.Errorf("AllDebrid: HTTP %d", resp.StatusCode)
	}
	return r.Data, nil
}

var (
	adHostsMu  sync.Mutex
	adHosts    []string
	adHostsAt  time.Time
	adHostsErr bool
)

// adSupported: the host of link is one AllDebrid unlocks (the list is kept 6 hours). When the
// list can't be read, every link is tried.
func adSupported(ctx context.Context, link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	adHostsMu.Lock()
	defer adHostsMu.Unlock()
	if time.Since(adHostsAt) > 6*time.Hour {
		adHostsAt = time.Now()
		data, err := adCall(ctx, "/v4.1/user/hosts", url.Values{}, nil, "")
		adHostsErr = err != nil
		if err == nil {
			var d map[string]map[string]struct {
				Domains []string `json:"domains"`
			}
			if json.Unmarshal(data, &d) == nil {
				adHosts = adHosts[:0]
				for _, group := range []string{"hosts", "streams", "redirectors"} {
					for _, h := range d[group] {
						for _, dom := range h.Domains {
							adHosts = append(adHosts, strings.ToLower(dom))
						}
					}
				}
			}
		}
	}
	if adHostsErr || len(adHosts) == 0 {
		return true
	}
	for _, d := range adHosts {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// AllDebridUnlock: a direct link for a hoster link ("" when AllDebrid does not support the host).
func AllDebridUnlock(ctx context.Context, link string) (string, error) {
	if !adSupported(ctx, link) {
		return "", nil
	}
	return adUnlock(ctx, link)
}

func adUnlock(ctx context.Context, link string) (string, error) {
	data, err := adCall(ctx, "/v4/link/unlock", url.Values{"link": {link}}, nil, "")
	if err != nil {
		if e, ok := err.(*adError); ok && (e.Code == "LINK_HOST_NOT_SUPPORTED" || e.Code == "LINK_NOT_SUPPORTED") {
			return "", nil
		}
		return "", err
	}
	var d struct {
		Link    string `json:"link"`
		Delayed int64  `json:"delayed"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return "", err
	}
	if d.Link != "" {
		return d.Link, nil
	}
	if d.Delayed == 0 {
		return "", fmt.Errorf("AllDebrid: no link")
	}
	// a delayed link: asked again every 5 seconds, up to 10 minutes
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
		data, err := adCall(ctx, "/v4/link/delayed", url.Values{"id": {strconv.FormatInt(d.Delayed, 10)}}, nil, "")
		if err != nil {
			return "", err
		}
		var s struct {
			Status int    `json:"status"`
			Link   string `json:"link"`
		}
		if json.Unmarshal(data, &s) == nil {
			switch s.Status {
			case 2:
				return s.Link, nil
			case 3:
				return "", fmt.Errorf("AllDebrid could not make the link")
			}
		}
	}
	return "", fmt.Errorf("AllDebrid: the link was not ready in time")
}

// ADFile is one finished file of a magnet: its path inside the torrent and a direct link.
type ADFile struct {
	Path string // folder inside the torrent ("" = top)
	Name string
	Size int64
	Link string
}

var adStatusText = map[int]string{
	5: "upload failed", 6: "unpacking failed", 7: "not downloaded in 20 minutes", 8: "the file is too big",
	9: "internal error", 10: "download took over 72 hours", 11: "deleted on the hoster",
	12: "processing failed", 13: "processing failed", 14: "could not contact the tracker",
	15: "no peers / the file is not available",
}

// AllDebridTorrent: a magnet (or a local .torrent file) downloaded by AllDebrid. onProgress gets
// its download on AllDebrid's servers. The answer: the files with direct links, the magnet id
// (for AllDebridDelete) and the torrent name.
func AllDebridTorrent(ctx context.Context, magnetOrFile string, onProgress func(done, total int64, speed float64, name string)) ([]ADFile, int64, string, error) {
	var data json.RawMessage
	var err error
	if fi, sErr := os.Stat(magnetOrFile); sErr == nil && !fi.IsDir() {
		b, rErr := os.ReadFile(magnetOrFile)
		if rErr != nil {
			return nil, 0, "", rErr
		}
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("files[]", filepath.Base(magnetOrFile))
		_, _ = fw.Write(b)
		_ = mw.Close()
		data, err = adCall(ctx, "/v4/magnet/upload/file", nil, &buf, mw.FormDataContentType())
	} else {
		data, err = adCall(ctx, "/v4/magnet/upload", url.Values{"magnets[]": {magnetOrFile}}, nil, "")
	}
	if err != nil {
		return nil, 0, "", err
	}
	var up struct {
		Magnets []struct {
			ID    int64    `json:"id"`
			Name  string   `json:"name"`
			Error *adError `json:"error"`
		} `json:"magnets"`
		Files []struct {
			ID    int64    `json:"id"`
			Name  string   `json:"name"`
			Error *adError `json:"error"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &up); err != nil {
		return nil, 0, "", err
	}
	items := up.Magnets
	if len(items) == 0 {
		items = up.Files
	}
	if len(items) == 0 {
		return nil, 0, "", fmt.Errorf("AllDebrid: the torrent was not accepted")
	}
	if items[0].Error != nil {
		return nil, 0, "", items[0].Error
	}
	id, name := items[0].ID, items[0].Name

	// until it is ready on AllDebrid (statusCode 4); 5..15 are errors
	for {
		data, err := adCall(ctx, "/v4.1/magnet/status", url.Values{"id": {strconv.FormatInt(id, 10)}}, nil, "")
		if err != nil {
			AllDebridDelete(id)
			return nil, 0, "", err
		}
		var st struct {
			Magnets json.RawMessage `json:"magnets"`
		}
		_ = json.Unmarshal(data, &st)
		type mag struct {
			Filename      string  `json:"filename"`
			Size          int64   `json:"size"`
			Status        string  `json:"status"`
			StatusCode    int     `json:"statusCode"`
			Downloaded    int64   `json:"downloaded"`
			DownloadSpeed float64 `json:"downloadSpeed"`
		}
		var m mag
		var list []mag
		if json.Unmarshal(st.Magnets, &list) == nil && len(list) > 0 {
			m = list[0]
		} else {
			_ = json.Unmarshal(st.Magnets, &m) // one magnet asked: an object
		}
		if m.Filename != "" {
			name = m.Filename
		}
		if m.StatusCode == 4 {
			if onProgress != nil {
				onProgress(m.Size, m.Size, 0, name)
			}
			break
		}
		if m.StatusCode >= 5 {
			AllDebridDelete(id)
			reason := adStatusText[m.StatusCode]
			if reason == "" {
				reason = m.Status
			}
			return nil, 0, name, fmt.Errorf("AllDebrid: %s", reason)
		}
		if onProgress != nil {
			onProgress(m.Downloaded, m.Size, m.DownloadSpeed, name)
		}
		select {
		case <-ctx.Done():
			AllDebridDelete(id)
			return nil, 0, name, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	// the files (a tree) and a direct link for each
	data, err = adCall(ctx, "/v4/magnet/files", url.Values{"id[]": {strconv.FormatInt(id, 10)}}, nil, "")
	if err != nil {
		AllDebridDelete(id)
		return nil, 0, name, err
	}
	type node struct {
		N string `json:"n"`
		S int64  `json:"s"`
		L string `json:"l"`
		E []node `json:"e"`
	}
	var fr struct {
		Magnets []struct {
			Files []node   `json:"files"`
			Error *adError `json:"error"`
		} `json:"magnets"`
	}
	if err := json.Unmarshal(data, &fr); err != nil || len(fr.Magnets) == 0 {
		AllDebridDelete(id)
		return nil, 0, name, fmt.Errorf("AllDebrid: no files")
	}
	if fr.Magnets[0].Error != nil {
		AllDebridDelete(id)
		return nil, 0, name, fr.Magnets[0].Error
	}
	var out []ADFile
	var walk func(ns []node, dir string)
	walk = func(ns []node, dir string) {
		for _, n := range ns {
			if len(n.E) > 0 || n.L == "" {
				walk(n.E, filepath.Join(dir, n.N))
				continue
			}
			out = append(out, ADFile{Path: dir, Name: n.N, Size: n.S, Link: n.L})
		}
	}
	walk(fr.Magnets[0].Files, "")
	for i := range out {
		direct, uErr := adUnlock(ctx, out[i].Link)
		if uErr != nil || direct == "" {
			AllDebridDelete(id)
			if uErr == nil {
				uErr = fmt.Errorf("AllDebrid: no link for %s", out[i].Name)
			}
			return nil, 0, name, uErr
		}
		out[i].Link = direct
	}
	return out, id, name, nil
}

// AllDebridDelete removes a magnet from the AllDebrid account.
func AllDebridDelete(id int64) {
	if id == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, _ = adCall(ctx, "/v4/magnet/delete", url.Values{"id": {strconv.FormatInt(id, 10)}}, nil, "")
}
