package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// GoFile folders like the Python bot: a guest token, the X-Website-Token signature, every file
// (also in sub folders) with its path; downloads need the accountToken cookie.

const gofileUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:122.0) Gecko/20100101 Firefox/122.0"

type GofileFile struct {
	ID   string
	Name string
	Path string
	Size int64
	URL  string
}

var (
	gofileRe     = regexp.MustCompile(`(?i)gofile\.io/d/([A-Za-z0-9_-]+)`)
	gofileSaltMu sync.Mutex
	gofileSalt   string
	gofileSaltAt time.Time
	gofileHTTP   = &http.Client{Timeout: 30 * time.Second}
)

func IsGofileLink(link string) bool { return gofileRe.MatchString(link) }

// salt from GoFile's website script (generateWT), cached for an hour
func gofileGetSalt(ctx context.Context) string {
	gofileSaltMu.Lock()
	defer gofileSaltMu.Unlock()
	if gofileSalt != "" && time.Since(gofileSaltAt) < time.Hour {
		return gofileSalt
	}
	salt := "12af056dacea0b"
	if req, err := http.NewRequestWithContext(ctx, "GET", "https://gofile.io/js/wt.obf.js", nil); err == nil {
		req.Header.Set("User-Agent", gofileUA)
		if resp, err := gofileHTTP.Do(req); err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			_ = resp.Body.Close()
			js := regexp.MustCompile(`\\x([0-9a-f]{2})`).ReplaceAllStringFunc(string(b), func(m string) string {
				var c byte
				_, _ = fmt.Sscanf(m[2:], "%02x", &c)
				return string([]byte{c})
			})
			if i := strings.Index(js, "generateWT"); i >= 0 {
				if m := regexp.MustCompile(`'([0-9a-f]{14})'`).FindStringSubmatch(js[i:]); m != nil {
					salt = m[1]
				}
			}
		}
	}
	gofileSalt, gofileSaltAt = salt, time.Now()
	return salt
}

func gofileToken(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.gofile.io/accounts", nil)
	req.Header.Set("User-Agent", gofileUA)
	resp, err := gofileHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Status string `json:"status"`
		Data   struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || r.Status != "ok" || r.Data.Token == "" {
		return "", fmt.Errorf("GoFile: could not get a guest token")
	}
	return r.Data.Token, nil
}

// GofileList lists every file of a GoFile link ("link::password" for protected ones).
// The returned header must be sent with the downloads.
func GofileList(ctx context.Context, link string) ([]GofileFile, string, error) {
	password := ""
	if i := strings.Index(link, "::"); i >= 0 {
		link, password = link[:i], link[i+2:]
	}
	m := gofileRe.FindStringSubmatch(link)
	if m == nil {
		return nil, "", fmt.Errorf("GoFile: bad link")
	}
	token, err := gofileToken(ctx)
	if err != nil {
		return nil, "", err
	}
	if password != "" {
		h := sha256.Sum256([]byte(password))
		password = hex.EncodeToString(h[:])
	}
	salt := gofileGetSalt(ctx)
	var files []GofileFile
	var fetch func(id, folder string, depth int) error
	fetch = func(id, folder string, depth int) error {
		if depth > 10 {
			return nil
		}
		u := "https://api.gofile.io/contents/" + id + "?cache=true"
		if password != "" {
			u += "&password=" + password
		}
		slot := time.Now().Unix() / 14400
		wt := sha256.Sum256([]byte(fmt.Sprintf("%s::en-US::%s::%d::%s", gofileUA, token, slot, salt)))
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", gofileUA)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Website-Token", hex.EncodeToString(wt[:]))
		req.Header.Set("X-BL", "en-US")
		resp, err := gofileHTTP.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		var r struct {
			Status string `json:"status"`
			Data   struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Name     string `json:"name"`
				Size     int64  `json:"size"`
				Link     string `json:"link"`
				Children map[string]struct {
					ID     string `json:"id"`
					Type   string `json:"type"`
					Name   string `json:"name"`
					Size   int64  `json:"size"`
					Link   string `json:"link"`
					Public *bool  `json:"public"`
				} `json:"children"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return fmt.Errorf("GoFile: bad answer")
		}
		switch r.Status {
		case "ok":
		case "error-passwordRequired":
			return fmt.Errorf("GoFile: this link needs a password, send it as link::password")
		case "error-passwordWrong":
			return fmt.Errorf("GoFile: wrong password")
		case "error-notFound":
			return fmt.Errorf("GoFile: file not found")
		case "error-notPublic":
			return fmt.Errorf("GoFile: this folder is not public")
		default:
			return fmt.Errorf("GoFile: %s", r.Status)
		}
		if r.Data.Type != "folder" {
			files = append(files, GofileFile{ID: r.Data.ID, Name: r.Data.Name, Path: folder, Size: r.Data.Size, URL: r.Data.Link})
			return nil
		}
		if folder == "" && depth == 0 {
			folder = r.Data.Name
		}
		for _, c := range r.Data.Children {
			if c.Type == "folder" {
				if c.Public != nil && !*c.Public {
					continue
				}
				if err := fetch(c.ID, path.Join(folder, c.Name), depth+1); err != nil {
					return err
				}
				continue
			}
			files = append(files, GofileFile{ID: c.ID, Name: c.Name, Path: folder, Size: c.Size, URL: c.Link})
		}
		return nil
	}
	if err := fetch(m[1], "", 0); err != nil {
		return nil, "", err
	}
	if len(files) == 0 {
		return nil, "", fmt.Errorf("GoFile: no files")
	}
	if len(files) == 1 {
		files[0].Path = ""
	}
	return files, "Cookie: accountToken=" + token, nil
}
