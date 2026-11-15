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
	"strings"
	"time"

	"azml-go/config"
)

// MirrorHost is a place /mirror can upload to.
type MirrorHost struct {
	ID   string
	Name string
	// Key returns the API key / token from the config ("" when not set).
	Key func(cfg *config.Config) string
	// NeedsKey: the host does not work without a key.
	NeedsKey bool
	// KeyVar is the config variable that holds the key.
	KeyVar string
	// Desc and Prompt are shown in the user's DDL Servers page.
	Desc   string
	Prompt string
}

// MirrorHosts are the /mirror upload hosts (the same ones as the DDL list of the Python bot).
var MirrorHosts = []MirrorHost{
	{ID: "gofile", Name: "GoFile", KeyVar: "GOFILE_TOKEN", Key: func(c *config.Config) string { return c.GoFileToken }, Desc: "Gofile is a free file sharing and storage platform. You can store and share your content without any limit. Works without API Key too (as Guest).", Prompt: "Send GoFile's API Key. Get it on https://gofile.io/myProfile"},
	{ID: "vikingfile", Name: "VikingFile", KeyVar: "VIKINGFILE_USER", Key: func(c *config.Config) string { return c.VikingFileUser }, Desc: "VikingFile is a free file hosting with no size limit. Works without User Hash too (as Guest).", Prompt: "Send VikingFile's User Hash. Get it on https://vikingfile.com/account"},
	{ID: "pixeldrain", Name: "PixelDrain", KeyVar: "PIXELDRAIN_KEY", Key: func(c *config.Config) string { return c.PixelDrainKey }, Desc: "PixelDrain is a fast free file hosting.", Prompt: "Send PixelDrain's API Key. Get it on https://pixeldrain.com/user/api_keys"},
	{ID: "buzzheavier", Name: "BuzzHeavier", KeyVar: "BUZZHEAVIER_TOKEN", NeedsKey: true, Key: func(c *config.Config) string { return c.BuzzHeavierToken }, Desc: "BuzzHeavier is a free file hosting with no size limit.", Prompt: "Send BuzzHeavier's Account ID (API token)."},
	{ID: "devuploads", Name: "DevUploads", KeyVar: "DEVUPLOADS_KEY", NeedsKey: true, Key: func(c *config.Config) string { return c.DevUploadsKey }, Desc: "DevUploads is a file hosting that pays per download.", Prompt: "Send DevUploads's API Key. Get it on https://devuploads.com/?op=my_account"},
	{ID: "streamtape", Name: "StreamTape", KeyVar: "STREAMTAPE_API", NeedsKey: true, Key: func(c *config.Config) string { return c.StreamTapeAPI }, Desc: "Streamtape is free Video Streaming & sharing Hoster", Prompt: "Send StreamTape's Login and Key\n<b>Format:</b> <code>user_login:pass_key</code>"},
}

// Endpoints (overridable in tests).
var (
	GoFileUploadURL      = "https://upload.gofile.io/uploadfile"
	PixelDrainAPI        = "https://pixeldrain.com/api"
	BuzzHeavierUploadURL = "https://w.buzzheavier.com"
	DevUploadsAPI        = "https://devuploads.com/api"
	VikingFileAPI        = "https://vikingfile.com/api"
	StreamTapeAPIURL     = "https://api.streamtape.com"
)

var uploadClient = &http.Client{Timeout: 6 * time.Hour}
var apiClient = &http.Client{Timeout: 30 * time.Second}

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

// AvailableMirrorHosts are the hosts that can be used now (keyless hosts, or the key is set).
func AvailableMirrorHosts() []MirrorHost {
	return AvailableMirrorHostsFor(nil)
}

// HostKey: the user's own key first, then the owner's key from /bs.
func HostKey(h MirrorHost, userKeys map[string]string) string {
	if k := userKeys[h.ID]; k != "" {
		return k
	}
	return h.Key(config.Get())
}

// AvailableMirrorHostsFor: the hosts this user can use (with the user's own keys).
func AvailableMirrorHostsFor(userKeys map[string]string) []MirrorHost {
	var out []MirrorHost
	for _, h := range MirrorHosts {
		if !h.NeedsKey || HostKey(h, userKeys) != "" {
			out = append(out, h)
		}
	}
	return out
}

// MirrorSession keeps state between the files of one task (GoFile puts them in one folder).
type MirrorSession struct {
	// Keys are the user's own host keys (they win over the ones in /bs).
	Keys         map[string]string
	goFileToken  string
	goFileFolder string
	goFileLink   string
}

// UploadToHost uploads one file to the chosen host and returns its link.
func UploadToHost(ctx context.Context, hostID, path string, sess *MirrorSession, onProgress func(sent, total int64)) (string, error) {
	if sess == nil {
		sess = &MirrorSession{}
	}
	h, ok := FindMirrorHost(hostID)
	if !ok {
		return "", fmt.Errorf("unknown upload host %q", hostID)
	}
	key := HostKey(h, sess.Keys)
	if h.NeedsKey && key == "" {
		return "", fmt.Errorf("%s needs an API key: set it in /us → Mirror Settings → DDL Servers (or %s in /bs)", h.Name, h.KeyVar)
	}
	switch h.ID {
	case "gofile":
		return uploadGoFile(ctx, path, key, sess, onProgress)
	case "pixeldrain":
		return uploadPixelDrain(ctx, path, key, onProgress)
	case "buzzheavier":
		return uploadBuzzHeavier(ctx, path, key, onProgress)
	case "devuploads":
		return uploadDevUploads(ctx, path, key, onProgress)
	case "vikingfile":
		return uploadVikingFile(ctx, path, key, onProgress)
	case "streamtape":
		return uploadStreamTape(ctx, path, key, onProgress)
	}
	return "", fmt.Errorf("unknown upload host %q", hostID)
}

// UploadToGoFile keeps the older single-call API (guest upload).
func UploadToGoFile(ctx context.Context, path string, onProgress func(sent, total int64)) (string, error) {
	return uploadGoFile(ctx, path, "", &MirrorSession{}, onProgress)
}

func uploadGoFile(ctx context.Context, path, token string, sess *MirrorSession, onProgress func(sent, total int64)) (string, error) {
	fields := map[string]string{}
	if t := sess.goFileToken; t != "" {
		fields["token"] = t
	} else if token != "" {
		fields["token"] = token
	}
	if sess.goFileFolder != "" {
		fields["folderId"] = sess.goFileFolder
	}
	body, status, err := postMultipart(ctx, GoFileUploadURL, fields, "file", path, onProgress)
	if err != nil {
		return "", err
	}
	var out struct {
		Status string `json:"status"`
		Data   struct {
			DownloadPage string `json:"downloadPage"`
			GuestToken   string `json:"guestToken"`
			ParentFolder string `json:"parentFolder"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("gofile: bad response (HTTP %d)", status)
	}
	if out.Status != "ok" || out.Data.DownloadPage == "" {
		return "", fmt.Errorf("gofile: upload failed (status %q, HTTP %d)", out.Status, status)
	}
	if sess.goFileToken == "" {
		sess.goFileToken = out.Data.GuestToken
		if sess.goFileToken == "" {
			sess.goFileToken = token
		}
	}
	if sess.goFileFolder == "" {
		sess.goFileFolder = out.Data.ParentFolder
	}
	if sess.goFileLink == "" {
		sess.goFileLink = out.Data.DownloadPage
	}
	// every file of the task lands in the same folder, so they share one link
	return sess.goFileLink, nil
}

func uploadPixelDrain(ctx context.Context, path, key string, onProgress func(sent, total int64)) (string, error) {
	name := strings.ReplaceAll(filepath.Base(path), " ", ".")
	body, status, err := putFile(ctx, PixelDrainAPI+"/file/"+url.PathEscape(name), path, func(r *http.Request) {
		if key != "" {
			r.SetBasicAuth("", key)
		}
	}, onProgress)
	if err != nil {
		return "", err
	}
	var out struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &out)
	if (status == 200 || status == 201) && out.ID != "" {
		return "https://pixeldrain.com/u/" + out.ID, nil
	}
	msg := out.Message
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", status)
	}
	return "", fmt.Errorf("pixeldrain: %s", msg)
}

func uploadBuzzHeavier(ctx context.Context, path, token string, onProgress func(sent, total int64)) (string, error) {
	name := strings.ReplaceAll(filepath.Base(path), " ", ".")
	endpoint := strings.TrimRight(BuzzHeavierUploadURL, "/") + "/" + url.PathEscape(name)
	body, status, err := putFile(ctx, endpoint, path, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	}, onProgress)
	if err != nil {
		return "", err
	}
	if status != 200 && status != 201 {
		return "", fmt.Errorf("buzzheavier: HTTP %d %s", status, trimText(body))
	}
	id := extractID(body)
	if id == "" || strings.Contains(id, "{") {
		return "", fmt.Errorf("buzzheavier: bad response %s", trimText(body))
	}
	if strings.HasPrefix(id, "http") {
		return id, nil
	}
	return "https://buzzheavier.com/" + id, nil
}

func uploadDevUploads(ctx context.Context, path, key string, onProgress func(sent, total int64)) (string, error) {
	var srv struct {
		Status int    `json:"status"`
		SessID string `json:"sess_id"`
		Result string `json:"result"`
	}
	if err := getJSON(ctx, fmt.Sprintf("%s/upload/server?key=%s", DevUploadsAPI, url.QueryEscape(key)), &srv); err != nil || srv.Status != 200 || srv.Result == "" {
		return "", fmt.Errorf("devuploads: invalid API key or no upload server")
	}
	body, status, err := postMultipart(ctx, srv.Result, map[string]string{"sess_id": srv.SessID, "utype": "reg"}, "file", path, onProgress)
	if err != nil {
		return "", err
	}
	type res struct {
		FileCode string `json:"file_code"`
		Message  string `json:"message"`
	}
	var one res
	var many []res
	if json.Unmarshal(body, &many) == nil && len(many) > 0 {
		one = many[0]
	} else {
		_ = json.Unmarshal(body, &one)
	}
	if one.FileCode == "" {
		return "", fmt.Errorf("devuploads: upload failed (HTTP %d) %s", status, one.Message)
	}
	return "https://devuploads.com/" + one.FileCode, nil
}

func uploadVikingFile(ctx context.Context, path, user string, onProgress func(sent, total int64)) (string, error) {
	var srv struct {
		Server string `json:"server"`
	}
	_ = getJSON(ctx, VikingFileAPI+"/get-server", &srv)
	if srv.Server == "" {
		srv.Server = "https://upload.vikingfile.com"
	}
	body, status, err := postMultipart(ctx, srv.Server, map[string]string{"user": user}, "file", path, onProgress)
	if err != nil {
		return "", err
	}
	var out struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(body, &out) == nil && out.URL != "" {
		return out.URL, nil
	}
	return "", fmt.Errorf("vikingfile: upload failed (HTTP %d) %s", status, trimText(body))
}

func uploadStreamTape(ctx context.Context, path, api string, onProgress func(sent, total int64)) (string, error) {
	login, key, ok := strings.Cut(api, ":")
	if !ok || login == "" || key == "" {
		return "", fmt.Errorf("streamtape: STREAMTAPE_API must be login:key")
	}
	var ul struct {
		Status int `json:"status"`
		Result struct {
			URL string `json:"url"`
		} `json:"result"`
	}
	if err := getJSON(ctx, fmt.Sprintf("%s/file/ul?login=%s&key=%s", StreamTapeAPIURL, url.QueryEscape(login), url.QueryEscape(key)), &ul); err != nil || ul.Status != 200 || ul.Result.URL == "" {
		return "", fmt.Errorf("streamtape: wrong login/key or no upload url")
	}
	body, status, err := postMultipart(ctx, ul.Result.URL, nil, "file1", path, onProgress)
	if err != nil {
		return "", err
	}
	var out struct {
		Status int `json:"status"`
		Result struct {
			URL string `json:"url"`
			ID  string `json:"id"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &out) == nil {
		if out.Result.URL != "" {
			return out.Result.URL, nil
		}
		if out.Result.ID != "" {
			return "https://streamtape.to/v/" + out.Result.ID, nil
		}
	}
	return "", fmt.Errorf("streamtape: upload failed (HTTP %d) %s", status, trimText(body))
}

// ---- helpers ----

func postMultipart(ctx context.Context, endpoint string, fields map[string]string, fileField, path string, onProgress func(sent, total int64)) ([]byte, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	total := info.Size()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		var perr error
		for k, v := range fields {
			if perr = mw.WriteField(k, v); perr != nil {
				break
			}
		}
		if perr == nil {
			var part io.Writer
			part, perr = mw.CreateFormFile(fileField, filepath.Base(path))
			if perr == nil {
				var sent int64
				_, perr = io.Copy(part, &progressReader{r: f, onRead: func(n int) {
					sent += int64(n)
					if onProgress != nil {
						onProgress(sent, total)
					}
				}})
			}
		}
		if perr == nil {
			perr = mw.Close()
		}
		_ = pw.CloseWithError(perr)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		_ = pr.Close()
		return nil, 0, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := uploadClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return b, resp.StatusCode, err
}

func putFile(ctx context.Context, endpoint, path string, prepare func(*http.Request), onProgress func(sent, total int64)) ([]byte, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	total := info.Size()
	var sent int64
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, &progressReader{r: f, onRead: func(n int) {
		sent += int64(n)
		if onProgress != nil {
			onProgress(sent, total)
		}
	}})
	if err != nil {
		return nil, 0, err
	}
	req.ContentLength = total
	if prepare != nil {
		prepare(req)
	}
	resp, err := uploadClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return b, resp.StatusCode, err
}

func getJSON(ctx context.Context, endpoint string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return json.NewDecoder(resp.Body).Decode(out)
}

// extractID reads an id from a JSON body ({"id"} or {"data":{"id"}}) or a plain text body.
func extractID(body []byte) string {
	var obj map[string]interface{}
	if json.Unmarshal(body, &obj) == nil {
		if v, ok := obj["id"].(string); ok {
			return v
		}
		if d, ok := obj["data"].(map[string]interface{}); ok {
			if v, ok := d["id"].(string); ok {
				return v
			}
		}
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(body)), `"`)
}

func trimText(b []byte) string {
	s := string(bytes.TrimSpace(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
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
