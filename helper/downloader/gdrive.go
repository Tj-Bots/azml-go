package downloader

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type GDriveFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size,string"`
	Path     string `json:"path"`
	IsDir    bool   `json:"is_dir"`
	Selected bool   `json:"selected"`
}

type GDriveClient struct {
	clientID     string
	clientSecret string
	refreshToken string
	accessToken  string
	expiryTime   time.Time
	lock         sync.Mutex
}

var (
	gdriveInstance *GDriveClient
	gdriveOnce     sync.Once
)

func GetGDriveClient() *GDriveClient {
	gdriveOnce.Do(func() {
		gdriveInstance = &GDriveClient{}
		_ = gdriveInstance.LoadCredentials("token.pickle")
	})
	return gdriveInstance
}

// LoadCredentials loads OAuth tokens from token.pickle or credentials.json
func (g *GDriveClient) LoadCredentials(tokenPath string) error {
	g.lock.Lock()
	defer g.lock.Unlock()

	// a new file replaces everything of the old one
	g.clientID, g.clientSecret, g.refreshToken, g.accessToken = "", "", "", ""
	g.expiryTime = time.Time{}

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		// Fallback check in parent dir
		data, err = os.ReadFile(filepath.Join("..", tokenPath))
		if err != nil {
			return err
		}
	}
	return g.parseCredentials(data, tokenPath)
}

// NewGDriveClientFromBytes: a client of a user's own token.pickle (/us → GDrive Tools).
func NewGDriveClientFromBytes(data []byte) (*GDriveClient, error) {
	g := &GDriveClient{}
	g.lock.Lock()
	defer g.lock.Unlock()
	if err := g.parseCredentials(data, "the user's token.pickle"); err != nil {
		return nil, err
	}
	return g, nil
}

// parseCredentials: JSON, a Python pickle or a printed credentials dict (called with g.lock held).
func (g *GDriveClient) parseCredentials(data []byte, tokenPath string) error {

	// 1. Try JSON
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("{")) {
		var js map[string]interface{}
		if err := json.Unmarshal(trimmed, &js); err == nil {
			if rt, ok := js["refresh_token"].(string); ok {
				g.refreshToken = rt
			}
			if cid, ok := js["client_id"].(string); ok {
				g.clientID = cid
			}
			if cs, ok := js["client_secret"].(string); ok {
				g.clientSecret = cs
			}
			if tk, ok := js["token"].(string); ok {
				g.accessToken = tk
			}
			return nil
		}
	}

	// 2. Parse Python pickle strings
	stringsList := extractPickleStrings(data)
	for i := 0; i < len(stringsList)-1; i++ {
		key := stringsList[i]
		val := stringsList[i+1]
		switch key {
		case "_refresh_token", "refresh_token":
			if g.refreshToken == "" && val != "" {
				g.refreshToken = val
			}
		case "_client_id", "client_id":
			if g.clientID == "" && val != "" {
				g.clientID = val
			}
		case "_client_secret", "client_secret":
			if g.clientSecret == "" && val != "" {
				g.clientSecret = val
			}
		case "_token", "token":
			if g.accessToken == "" && val != "" {
				g.accessToken = val
			}
		}
	}

	// 3. text (a printed credentials dict, e.g. copied from /shell of the Python bot):
	// 'refresh_token': '...' or "refresh_token": "..."
	if g.refreshToken == "" {
		for _, k := range []struct {
			names []string
			dst   *string
		}{
			{[]string{"refresh_token", "_refresh_token"}, &g.refreshToken},
			{[]string{"client_id", "_client_id"}, &g.clientID},
			{[]string{"client_secret", "_client_secret"}, &g.clientSecret},
			{[]string{"access_token", "token", "_token"}, &g.accessToken},
		} {
			for _, n := range k.names {
				re := regexp.MustCompile(`['"]` + regexp.QuoteMeta(n) + `['"]\s*:\s*['"]([^'"]+)['"]`)
				if m := re.FindSubmatch(data); m != nil && *k.dst == "" {
					*k.dst = string(m[1])
					break
				}
			}
		}
	}

	if g.refreshToken != "" || g.accessToken != "" {
		log.Printf("[GDRIVE] Loaded credentials from %s (refresh token: %v, client id: %v)", tokenPath, g.refreshToken != "", g.clientID != "")
		return nil
	}
	return fmt.Errorf("no valid credentials found in token.pickle")
}

func extractPickleStrings(data []byte) []string {
	var results []string
	n := len(data)
	i := 0
	for i < n {
		b := data[i]
		switch b {
		case 0x8c: // SHORT_BINUNICODE (1 byte length)
			i++
			if i < n {
				l := int(data[i])
				i++
				if i+l <= n {
					results = append(results, string(data[i:i+l]))
					i += l
					continue
				}
			}
		case 'X': // BINUNICODE (4 bytes length)
			i++
			if i+4 <= n {
				l := int(binary.LittleEndian.Uint32(data[i : i+4]))
				i += 4
				if i+l <= n {
					results = append(results, string(data[i:i+l]))
					i += l
					continue
				}
			}
		case 'V', 'S': // UNICODE / STRING terminated with newline
			i++
			start := i
			for i < n && data[i] != '\n' {
				i++
			}
			if i <= n {
				val := strings.Trim(string(data[start:i]), "'\"")
				results = append(results, val)
				i++
				continue
			}
		}
		i++
	}
	return results
}

// GetAccessToken returns a valid Bearer token, refreshing if necessary
func (g *GDriveClient) GetAccessToken() (string, error) {
	g.lock.Lock()
	defer g.lock.Unlock()

	if g.accessToken != "" && time.Now().Before(g.expiryTime) {
		return g.accessToken, nil
	}

	if g.refreshToken == "" {
		if g.accessToken != "" {
			return g.accessToken, nil
		}
		return "", fmt.Errorf("no refresh_token or access_token available")
	}

	v := url.Values{}
	v.Set("client_id", g.clientID)
	v.Set("client_secret", g.clientSecret)
	v.Set("refresh_token", g.refreshToken)
	v.Set("grant_type", "refresh_token")

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", v)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var res struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", err
	}
	if res.Error != "" {
		if res.Error == "invalid_grant" {
			return "", fmt.Errorf("token refresh error: invalid_grant (this token.pickle is expired or revoked: send the current one again)")
		}
		return "", fmt.Errorf("token refresh error: %s", res.Error)
	}

	g.accessToken = res.AccessToken
	g.expiryTime = time.Now().Add(time.Duration(res.ExpiresIn-60) * time.Second)
	return g.accessToken, nil
}

func ExtractGDriveID(link string) string {
	re := regexp.MustCompile(`https?://(?:drive\.google\.com/(?:file/d/|drive/folders/|open\?id=|folderview\?id=)|docs\.google\.com/file/d/)([-\w]+)`)
	matches := re.FindStringSubmatch(link)
	if len(matches) > 1 {
		return matches[1]
	}
	if strings.Contains(link, "id=") {
		u, err := url.Parse(link)
		if err == nil {
			return u.Query().Get("id")
		}
	}
	return ""
}

func IsGDriveLink(link string) bool {
	return strings.Contains(link, "drive.google.com") || strings.Contains(link, "docs.google.com")
}

// GetFileInfo fetches metadata of a file or folder from Google Drive
func (g *GDriveClient) GetFileInfo(id string) (*GDriveFile, error) {
	token, err := g.GetAccessToken()
	if err != nil {
		return workerInfo(id) // no token.pickle: public links through GDRIVE_WORKER
	}
	f, err := g.apiFileInfo(id, token)
	if err != nil {
		if wf, wErr := workerInfo(id); wErr == nil {
			g.markPublic(id)
			return wf, nil
		}
	}
	return f, err
}

// public: ids the token can't open but the worker can (their downloads go through the worker too)
var (
	publicMu  sync.Mutex
	publicIDs = map[string]bool{}
)

func (g *GDriveClient) markPublic(id string) {
	publicMu.Lock()
	publicIDs[id] = true
	publicMu.Unlock()
}

func isPublicID(id string) bool {
	publicMu.Lock()
	defer publicMu.Unlock()
	return publicIDs[id]
}

func (g *GDriveClient) apiFileInfo(id, token string) (*GDriveFile, error) {

	apiURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?supportsAllDrives=true&fields=id,name,mimeType,size", id)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gdrive API error (%d): %s", resp.StatusCode, string(b))
	}

	var f GDriveFile
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil {
		return nil, err
	}
	f.IsDir = f.MimeType == "application/vnd.google-apps.folder"
	return &f, nil
}

// GetFolderFiles lists all files inside a Google Drive folder recursively
func (g *GDriveClient) GetFolderFiles(folderID string, currentPath string) ([]GDriveFile, error) {
	token, err := g.GetAccessToken()
	if err != nil || isPublicID(folderID) {
		files, wErr := workerFolder(folderID, currentPath, 0)
		for _, f := range files {
			g.markPublic(f.ID)
		}
		return files, wErr
	}

	var allFiles []GDriveFile
	pageToken := ""
	client := &http.Client{Timeout: 30 * time.Second}

	for {
		query := fmt.Sprintf("'%s' in parents and trashed = false", folderID)
		apiURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files?q=%s&supportsAllDrives=true&includeItemsFromAllDrives=true&pageSize=1000&fields=nextPageToken,files(id,name,mimeType,size)", url.QueryEscape(query))
		if pageToken != "" {
			apiURL += "&pageToken=" + pageToken
		}

		req, _ := http.NewRequest("GET", apiURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			return allFiles, err
		}

		var res struct {
			NextPageToken string       `json:"nextPageToken"`
			Files         []GDriveFile `json:"files"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&res)
		resp.Body.Close()

		for _, item := range res.Files {
			item.Path = currentPath
			if item.MimeType == "application/vnd.google-apps.folder" {
				item.IsDir = true
				allFiles = append(allFiles, item)
				subFiles, _ := g.GetFolderFiles(item.ID, filepath.Join(currentPath, item.Name))
				allFiles = append(allFiles, subFiles...)
			} else {
				item.IsDir = false
				item.Selected = true
				allFiles = append(allFiles, item)
			}
		}

		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}

	return allFiles, nil
}

// DownloadFile downloads a Google Drive file using Aria2c (16 connections) or HTTP streaming fallback
func (g *GDriveClient) DownloadFile(ctx context.Context, fileID, destDir, fileName string, progress func(curr, total int64, speed float64)) error {
	token, err := g.GetAccessToken()
	downloadURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?alt=media&supportsAllDrives=true", fileID)
	header := fmt.Sprintf("Authorization: Bearer %s", token)
	var headers []string
	if err != nil || isPublicID(fileID) {
		if gdriveWorker() == "" {
			if err == nil {
				err = fmt.Errorf("GDRIVE_WORKER is off")
			}
			return err
		}
		downloadURL, header, token = workerFileURL(fileID, fileName), "", ""
	} else {
		headers = []string{header}
	}

	// Try fast 16-connection download via Aria2c
	aria := GetAria2Client()
	if aria != nil {
		gid, err := aria.AddURI(downloadURL, destDir, fileName, headers)
		if err == nil {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					_ = aria.Cancel(gid)
					return ctx.Err()
				case <-ticker.C:
					status, sErr := aria.TellStatus(gid)
					if sErr == nil {
						if progress != nil {
							cLen, _ := strconv.ParseInt(status.CompletedLength, 10, 64)
							tLen, _ := strconv.ParseInt(status.TotalLength, 10, 64)
							spd, _ := strconv.ParseFloat(status.DownloadSpeed, 64)
							progress(cLen, tLen, spd)
						}
						if status.Status == "complete" {
							return nil
						}
						if status.Status == "error" {
							return fmt.Errorf("tjurl gdrive download failed: %s", status.ErrorMessage)
						}
					}
				}
			}
		}
	}

	// HTTP streaming fallback
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gdrive download returned HTTP %d", resp.StatusCode)
	}

	outPath := filepath.Join(destDir, fileName)
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	total := resp.ContentLength
	var curr int64
	buf := make([]byte, 256*1024)
	start := time.Now()

	for {
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = f.Write(buf[:n])
			curr += int64(n)
			if progress != nil {
				elapsed := time.Since(start).Seconds()
				spd := float64(0)
				if elapsed > 0 {
					spd = float64(curr) / elapsed
				}
				progress(curr, total, spd)
			}
		}
		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			return rErr
		}
	}
	return nil
}
