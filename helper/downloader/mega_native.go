package downloader

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Native MEGA client for public links (file and folder, old #! and new /file/ formats).
// No megatools needed: Mega's public API + AES-CTR decryption, several connections in parallel.

type MegaLink struct {
	Folder bool
	ID     string
	Key    string
	Sub    string // /file/<id> or /folder/<id> inside a folder link
}

var (
	megaNewRe = regexp.MustCompile(`mega\.(?:nz|co\.nz)/(file|folder)/([A-Za-z0-9_-]+)#([A-Za-z0-9_-]+)(?:/(?:file|folder)/([A-Za-z0-9_-]+))?`)
	megaOldRe = regexp.MustCompile(`mega\.(?:nz|co\.nz)/#(F?)!([A-Za-z0-9_-]+)!([A-Za-z0-9_-]+)(?:!([A-Za-z0-9_-]+))?`)
)

func ParseMegaLink(link string) (MegaLink, bool) {
	if m := megaNewRe.FindStringSubmatch(link); m != nil {
		return MegaLink{Folder: m[1] == "folder", ID: m[2], Key: m[3], Sub: m[4]}, true
	}
	if m := megaOldRe.FindStringSubmatch(link); m != nil {
		return MegaLink{Folder: m[1] == "F", ID: m[2], Key: m[3], Sub: m[4]}, true
	}
	return MegaLink{}, false
}

func megaB64(s string) ([]byte, error) {
	s = strings.NewReplacer("-", "+", "_", "/", ",", "").Replace(s)
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return base64.StdEncoding.DecodeString(s)
}

var megaHTTP = &http.Client{Timeout: 60 * time.Second}

type megaAPIError int

func (e megaAPIError) Error() string {
	switch int(e) {
	case -2:
		return "MEGA: wrong link"
	case -9:
		return "MEGA: file or folder not found (deleted?)"
	case -11, -16:
		return "MEGA: access denied / link blocked"
	case -17:
		return "MEGA: bandwidth limit reached, try later"
	case -18:
		return "MEGA: temporarily unavailable, try later"
	}
	return fmt.Sprintf("MEGA API error %d", int(e))
}

func megaAPI(ctx context.Context, folderID string, req interface{}, out interface{}) error {
	body, _ := json.Marshal([]interface{}{req})
	url := fmt.Sprintf("https://g.api.mega.co.nz/cs?id=%d", time.Now().UnixNano()%1e9)
	if folderID != "" {
		url += "&n=" + folderID
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		hreq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		hreq.Header.Set("Content-Type", "application/json")
		resp, err := megaHTTP.Do(hreq)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var code int
		if json.Unmarshal(data, &code) == nil && code < 0 { // whole request failed
			if code == -3 { // EAGAIN
				time.Sleep(time.Duration(attempt+1) * time.Second)
				lastErr = megaAPIError(code)
				continue
			}
			return megaAPIError(code)
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(data, &arr); err != nil || len(arr) == 0 {
			return fmt.Errorf("MEGA: bad answer: %s", strings.TrimSpace(string(data)))
		}
		if json.Unmarshal(arr[0], &code) == nil && code < 0 {
			if code == -3 {
				time.Sleep(time.Duration(attempt+1) * time.Second)
				lastErr = megaAPIError(code)
				continue
			}
			return megaAPIError(code)
		}
		return json.Unmarshal(arr[0], out)
	}
	return lastErr
}

func words(b []byte) []uint32 {
	w := make([]uint32, len(b)/4)
	for i := range w {
		w[i] = binary.BigEndian.Uint32(b[i*4:])
	}
	return w
}

func wordsBytes(w []uint32) []byte {
	b := make([]byte, len(w)*4)
	for i, v := range w {
		binary.BigEndian.PutUint32(b[i*4:], v)
	}
	return b
}

// fileKey splits a 32 byte node key into the AES key and the CTR nonce.
func fileKey(raw []byte) (key, iv []byte, err error) {
	if len(raw) != 32 {
		return nil, nil, fmt.Errorf("MEGA: bad file key")
	}
	w := words(raw)
	key = wordsBytes([]uint32{w[0] ^ w[4], w[1] ^ w[5], w[2] ^ w[6], w[3] ^ w[7]})
	iv = wordsBytes([]uint32{w[4], w[5], 0, 0})
	return key, iv, nil
}

type megaAttr struct {
	N string `json:"n"`
}

func decryptAttr(at string, key []byte) (string, error) {
	data, err := megaB64(at)
	if err != nil || len(data)%16 != 0 {
		return "", fmt.Errorf("MEGA: bad attributes")
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(blk, make([]byte, 16)).CryptBlocks(out, data)
	s := strings.TrimRight(string(out), "\x00")
	if !strings.HasPrefix(s, "MEGA{") {
		return "", fmt.Errorf("MEGA: wrong key")
	}
	var a megaAttr
	if err := json.Unmarshal([]byte(s[4:]), &a); err != nil {
		return "", err
	}
	return a.N, nil
}

func ecbDecrypt(key, data []byte) []byte {
	blk, err := aes.NewCipher(key)
	if err != nil || len(data)%16 != 0 {
		return nil
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += 16 {
		blk.Decrypt(out[i:i+16], data[i:i+16])
	}
	return out
}

// MegaNode is one file of a link, with what is needed to download it.
type MegaNode struct {
	Handle string
	Name   string
	Path   string // folder path inside the link
	Size   int64
	IsDir  bool
	key    []byte
	iv     []byte
	folder string // folder link id, for the download request
}

type megaFolderNode struct {
	H string `json:"h"`
	P string `json:"p"`
	T int    `json:"t"`
	A string `json:"a"`
	K string `json:"k"`
	S int64  `json:"s"`
}

// MegaList lists the files of a link (one for a file link).
func MegaList(ctx context.Context, link string) ([]MegaNode, error) {
	ml, ok := ParseMegaLink(link)
	if !ok {
		return nil, fmt.Errorf("MEGA: unsupported link")
	}
	if !ml.Folder {
		raw, err := megaB64(ml.Key)
		if err != nil {
			return nil, fmt.Errorf("MEGA: bad key")
		}
		key, iv, err := fileKey(raw)
		if err != nil {
			return nil, err
		}
		var res struct {
			S  int64  `json:"s"`
			At string `json:"at"`
		}
		if err := megaAPI(ctx, "", map[string]interface{}{"a": "g", "p": ml.ID, "ssl": 2}, &res); err != nil {
			return nil, err
		}
		name, err := decryptAttr(res.At, key)
		if err != nil {
			return nil, err
		}
		return []MegaNode{{Handle: ml.ID, Name: name, Size: res.S, key: key, iv: iv}}, nil
	}

	master, err := megaB64(ml.Key)
	if err != nil || len(master) != 16 {
		return nil, fmt.Errorf("MEGA: bad folder key")
	}
	var res struct {
		F []megaFolderNode `json:"f"`
	}
	if err := megaAPI(ctx, ml.ID, map[string]interface{}{"a": "f", "c": 1, "r": 1, "ca": 1}, &res); err != nil {
		return nil, err
	}
	type info struct {
		name, parent string
		dir          bool
	}
	nodes := map[string]info{}
	var files []MegaNode
	for _, n := range res.F {
		enc := n.K
		if i := strings.Index(enc, ":"); i >= 0 {
			enc = enc[i+1:]
		}
		if i := strings.Index(enc, "/"); i >= 0 {
			enc = enc[:i]
		}
		encKey, err := megaB64(enc)
		if err != nil {
			continue
		}
		k := ecbDecrypt(master, encKey)
		if k == nil {
			continue
		}
		var attrKey, iv []byte
		if n.T == 0 {
			attrKey, iv, err = fileKey(k)
			if err != nil {
				continue
			}
		} else {
			attrKey = k[:16]
		}
		name, err := decryptAttr(n.A, attrKey)
		if err != nil {
			continue
		}
		nodes[n.H] = info{name: name, parent: n.P, dir: n.T == 1}
		if n.T == 0 {
			files = append(files, MegaNode{Handle: n.H, Name: name, Size: n.S, key: attrKey, iv: iv, folder: ml.ID, Path: n.P})
		}
	}
	// folder path of every file
	for i := range files {
		var parts []string
		for p := files[i].Path; p != ""; {
			nd, ok := nodes[p]
			if !ok {
				break
			}
			parts = append([]string{nd.name}, parts...)
			p = nd.parent
		}
		files[i].Path = filepath.Join(parts...)
	}
	if ml.Sub != "" { // a file or sub folder inside the folder link
		var sel []MegaNode
		for _, f := range files {
			if f.Handle == ml.Sub || megaUnder(nodes, f.Handle, ml.Sub, func(h string) string { return nodes[h].parent }) {
				sel = append(sel, f)
			}
		}
		files = sel
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("MEGA: no files in this link")
	}
	return files, nil
}

func megaUnder(nodes interface{}, h, root string, parent func(string) string) bool {
	for i := 0; i < 64 && h != ""; i++ {
		h = parent(h)
		if h == root {
			return true
		}
	}
	return false
}

const megaChunk = 4 << 20

// MegaDownloadNode downloads and decrypts one file into dest (8 parallel connections).
func MegaDownloadNode(ctx context.Context, n MegaNode, dest string, onBytes func(int64)) error {
	req := map[string]interface{}{"a": "g", "g": 1, "ssl": 2}
	if n.folder != "" {
		req["n"] = n.Handle
	} else {
		req["p"] = n.Handle
	}
	var res struct {
		G string `json:"g"`
		S int64  `json:"s"`
	}
	if err := megaAPI(ctx, n.folder, req, &res); err != nil {
		return err
	}
	if res.G == "" {
		return fmt.Errorf("MEGA: no download url (quota?)")
	}
	_ = os.MkdirAll(filepath.Dir(dest), 0755)
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if n.Size > 0 {
		_ = f.Truncate(n.Size)
	}
	blk, err := aes.NewCipher(n.key)
	if err != nil {
		return err
	}

	chunks := make(chan int64, (n.Size/megaChunk)+1)
	for off := int64(0); off < n.Size; off += megaChunk {
		chunks <- off
	}
	close(chunks)
	var firstErr atomic.Value
	var wg sync.WaitGroup
	client := &http.Client{Timeout: 5 * time.Minute}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for off := range chunks {
				if ctx.Err() != nil || firstErr.Load() != nil {
					return
				}
				end := off + megaChunk - 1
				if end >= n.Size {
					end = n.Size - 1
				}
				var data []byte
				var err error
				for attempt := 0; attempt < 5; attempt++ {
					data, err = megaGetRange(ctx, client, res.G, off, end)
					if err == nil {
						break
					}
					time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
				}
				if err != nil {
					firstErr.Store(err)
					return
				}
				ctr := make([]byte, 16)
				copy(ctr, n.iv[:8])
				binary.BigEndian.PutUint64(ctr[8:], uint64(off/16))
				cipher.NewCTR(blk, ctr).XORKeyStream(data, data)
				if _, err := f.WriteAt(data, off); err != nil {
					firstErr.Store(err)
					return
				}
				if onBytes != nil {
					onBytes(int64(len(data)))
				}
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e := firstErr.Load(); e != nil {
		return e.(error)
	}
	return nil
}

func megaGetRange(ctx context.Context, client *http.Client, url string, start, end int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/%d-%d", url, start, end), nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 509 {
		return nil, fmt.Errorf("MEGA: bandwidth limit reached, try later")
	}
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return nil, fmt.Errorf("MEGA: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != end-start+1 {
		return nil, fmt.Errorf("MEGA: short chunk")
	}
	return data, nil
}

// MegaDownloadLink downloads every file of the link (or only `only` handles when set) into outDir.
// It returns the file (one file) or the folder.
func MegaDownloadLink(ctx context.Context, link, outDir string, only map[string]bool, onProgress func(cur, total int64, speed float64, eta string)) (string, error) {
	files, err := MegaList(ctx, link)
	if err != nil {
		return "", err
	}
	if len(only) > 0 {
		var sel []MegaNode
		for _, f := range files {
			if only[f.Handle] {
				sel = append(sel, f)
			}
		}
		files = sel
	}
	if len(files) == 0 {
		return "", fmt.Errorf("MEGA: nothing selected")
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	var done atomic.Int64
	start := time.Now()
	var lastMu sync.Mutex
	lastT, lastB := start, int64(0)
	speed := 0.0
	report := func(n int64) {
		cur := done.Add(n)
		if onProgress == nil {
			return
		}
		lastMu.Lock()
		defer lastMu.Unlock()
		if dt := time.Since(lastT).Seconds(); dt >= 1 {
			inst := float64(cur-lastB) / dt
			if speed == 0 {
				speed = inst
			} else {
				speed = speed*0.6 + inst*0.4
			}
			lastT, lastB = time.Now(), cur
			eta := "-"
			if speed > 0 {
				eta = (time.Duration(float64(total-cur)/speed) * time.Second).Round(time.Second).String()
			}
			onProgress(cur, total, speed, eta)
		}
	}
	var last string
	for _, f := range files {
		name := strings.NewReplacer("/", "_", "\\", "_").Replace(f.Name)
		dest := filepath.Join(outDir, f.Path, name)
		if err := MegaDownloadNode(ctx, f, dest, report); err != nil {
			return "", fmt.Errorf("%s: %w", f.Name, err)
		}
		last = dest
	}
	if onProgress != nil {
		onProgress(total, total, speed, "-")
	}
	if len(files) == 1 {
		return last, nil
	}
	// one top folder: upload it as is
	entries, _ := os.ReadDir(outDir)
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(outDir, entries[0].Name()), nil
	}
	return outDir, nil
}
