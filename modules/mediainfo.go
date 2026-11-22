package modules

import (
	"azml-go/config"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type MediaInfoModule struct {
	sender *telegram.MessageSender
	rawAPI func() *tg.Client
}

// SetRawAPI gives the module the bot client, to read the start of Telegram files.
func (m *MediaInfoModule) SetRawAPI(f func() *tg.Client) { m.rawAPI = f }

// tgDocument returns the document of a replied file (nil when it is not a file).
func tgDocument(media tg.MessageMediaClass) *tg.Document {
	md, ok := media.(*tg.MessageMediaDocument)
	if !ok || md.Document == nil {
		return nil
	}
	doc, _ := md.Document.(*tg.Document)
	return doc
}

func docFileName(doc *tg.Document) string {
	for _, a := range doc.Attributes {
		if fn, ok := a.(*tg.DocumentAttributeFilename); ok && fn.FileName != "" {
			return fn.FileName
		}
	}
	return "Media_File"
}

// readTelegramHead saves the first bytes of a Telegram file (enough for mediainfo).
func readTelegramHead(ctx context.Context, api *tg.Client, doc *tg.Document, dest string, maxBytes int64) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	const chunk = 1024 * 1024
	loc := &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}
	for offset := int64(0); offset < maxBytes && offset < doc.Size; offset += chunk {
		res, err := api.UploadGetFile(ctx, &tg.UploadGetFileRequest{Location: loc, Offset: offset, Limit: chunk, Precise: false})
		if err != nil {
			if offset > 0 {
				return nil // some bytes are enough
			}
			return err
		}
		part, ok := res.(*tg.UploadFile)
		if !ok || len(part.Bytes) == 0 {
			break
		}
		if _, err := f.Write(part.Bytes); err != nil {
			return err
		}
		if len(part.Bytes) < chunk {
			break
		}
	}
	return nil
}

func NewMediaInfoModule(sender *telegram.MessageSender) *MediaInfoModule {
	return &MediaInfoModule{sender: sender}
}

func (m *MediaInfoModule) HandleMediaInfo(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	link := ""
	parts := strings.Fields(msg.Text)
	doc := tgDocument(msg.RawMedia)
	if len(parts) > 1 {
		link = parts[1]
	} else if doc == nil && msg.IsReply && msg.ReplyText != "" {
		for _, w := range strings.Fields(msg.ReplyText) {
			if strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://") {
				link = w
				break
			}
		}
	}

	if doc != nil && link == "" {
		var api *tg.Client
		if m.rawAPI != nil {
			api = m.rawAPI()
		}
		if api == nil {
			_, _ = m.sender.SendMessage(ctx, peer, "× <b>MediaInfo:</b> the bot is not ready yet, try again.", msg.MessageID, telegram.CloseMarkup(msg.UserID))
			return
		}
		waitID, _ := m.sender.SendMessage(ctx, peer, "<i>Generating MediaInfo...</i>", msg.MessageID, nil)
		go func() {
			tmpDir, err := os.MkdirTemp("", "mediainfo_*")
			if err != nil {
				_ = m.sender.EditMessage(ctx, peer, waitID, "Error creating temp dir", telegram.CloseMarkup(msg.UserID))
				return
			}
			defer os.RemoveAll(tmpDir)
			fileName := docFileName(doc)
			destFile := filepath.Join(tmpDir, "sample_media"+filepath.Ext(fileName))
			if err := readTelegramHead(context.Background(), api, doc, destFile, 5*1024*1024); err != nil {
				_ = m.sender.EditMessage(ctx, peer, waitID, fmt.Sprintf("× <b>Failed to read the file:</b> <code>%s</code>", telegram.EscapeHTML(err.Error())), telegram.CloseMarkup(msg.UserID))
				return
			}
			m.sendMediaInfo(ctx, peer, waitID, msg.UserID, fileName, destFile)
		}()
		return
	}

	if link != "" && !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		link = ""
	}

	if link == "" {
		helpMsg := "㊂ <b><u>MediaInfo Usage :</u></b>\n\n" +
			"<blockquote><b>╭</b> <code>" + telegram.Cmd("mediainfo") + " [download link]</code>\n" +
			"<b>╰</b> Reply to a file or a link with <code>" + telegram.Cmd("mediainfo") + "</code></blockquote>"
		_, _ = m.sender.SendMessage(ctx, peer, helpMsg, msg.MessageID, telegram.CloseMarkup(msg.UserID))
		return
	}

	waitID, _ := m.sender.SendMessage(ctx, peer, "<i>Generating MediaInfo...</i>", msg.MessageID, nil)

	go func() {
		tmpDir, err := os.MkdirTemp("", "mediainfo_*")
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, "Error creating temp dir", nil)
			return
		}
		defer os.RemoveAll(tmpDir)

		destFile := filepath.Join(tmpDir, "sample_media")

		// Download first 10MB chunk for probe
		client := &http.Client{Timeout: 30 * time.Second}
		req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, fmt.Sprintf("Error request: %v", err), nil)
			return
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("Range", "bytes=0-1048576") // 1 MB chunk for fast probe

		resp, err := client.Do(req)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, fmt.Sprintf("Failed to fetch media: %v", err), nil)
			return
		}
		defer resp.Body.Close()

		f, err := os.Create(destFile)
		if err != nil {
			_ = m.sender.EditMessage(ctx, peer, waitID, "Failed to create temp file", nil)
			return
		}
		_, _ = io.CopyN(f, resp.Body, 1024*1024)
		_ = f.Close()

		fileName := filepath.Base(link)
		if u, err := url.Parse(link); err == nil {
			fileName = filepath.Base(u.Path)
		}
		if fileName == "" || fileName == "/" || fileName == "." {
			fileName = "Media_File"
		}

		m.sendMediaInfo(ctx, peer, waitID, msg.UserID, fileName, destFile)
	}()
}

// sendMediaInfo runs mediainfo on the file and posts the result (Telegraph page when possible).
func (m *MediaInfoModule) sendMediaInfo(ctx context.Context, peer tg.InputPeerClass, waitID int, userID int64, fileName, destFile string) {
	infoText := runMediaInfo(destFile)
	formattedHTML := formatMediaInfoHTML(fileName, infoText)

	pageURL, err := createTelegraphPage("MediaInfo", formattedHTML)
	if err != nil {
		_ = m.sender.EditMessage(ctx, peer, waitID, fmt.Sprintf("<b>MediaInfo Output:</b>\n<pre>%s</pre>", telegram.EscapeHTML(truncateString(infoText, 3000))), telegram.CloseMarkup(userID))
		return
	}

	respText := fmt.Sprintf("㊂ <b><u>MediaInfo :</u></b>\n\n<blockquote>◈ <b>Name :</b> <code>%s</code></blockquote>", telegram.EscapeHTML(fileName))
	btn := telegram.NewButtonMaker()
	btn.UButtonStyled("📃 MediaInfo", pageURL, "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("start_close %d", userID), "danger")
	btn.Row()
	_ = m.sender.EditMessage(ctx, peer, waitID, respText, btn.BuildMenu(2))
}

func runMediaInfo(filePath string) string {
	if _, err := exec.LookPath(ext.InfoBin()); err == nil {
		cmd := exec.Command(ext.InfoBin(), filePath)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil && out.Len() > 0 {
			return out.String()
		}
	}

	// Fallback to ffprobe
	if _, err := exec.LookPath(ext.FFprobeBin()); err == nil {
		cmd := exec.Command(ext.FFprobeBin(), "-hide_banner", filePath)
		var out bytes.Buffer
		cmd.Stderr = &out
		if err := cmd.Run(); err == nil || out.Len() > 0 {
			return out.String()
		}
	}

	meta, _ := ext.ProbeMedia(filePath)
	if meta != nil {
		return fmt.Sprintf("Video: %s\nDuration: %d s\nResolution: %dx%d\nAudio: %s\nQuality: %s",
			meta.VideoCodec, meta.Duration, meta.Width, meta.Height, meta.AudioCodec, meta.Quality)
	}

	return "No MediaInfo could be extracted."
}

// mediaInfoNode is a Telegraph content node.
type mediaInfoNode struct {
	Tag      string        `json:"tag"`
	Children []interface{} `json:"children,omitempty"`
}

// formatMediaInfoHTML turns the mediainfo output into Telegraph nodes (JSON):
// a header for every section (General, Video, Audio, Text, Menu) and its lines in a <pre>.
func formatMediaInfoHTML(fileName, out string) string {
	nodes := []mediaInfoNode{{Tag: "h4", Children: []interface{}{"◈ " + fileName}}}
	var pre strings.Builder
	flush := func() {
		if strings.TrimSpace(pre.String()) != "" {
			nodes = append(nodes, mediaInfoNode{Tag: "pre", Children: []interface{}{strings.TrimRight(pre.String(), "\n")}})
		}
		pre.Reset()
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		header := false
		for _, sec := range []string{"General", "Video", "Audio", "Text", "Subtitle", "Menu", "Image"} {
			if strings.HasPrefix(trimmed, sec) && !strings.Contains(trimmed, ":") {
				flush()
				nodes = append(nodes, mediaInfoNode{Tag: "h4", Children: []interface{}{"◈ " + trimmed}})
				header = true
				break
			}
		}
		if !header {
			pre.WriteString(line + "\n")
		}
	}
	flush()
	b, _ := json.Marshal(nodes)
	return string(b)
}

var (
	telegraphMu    sync.Mutex
	telegraphToken string
)

// telegraphAPIs: graph.org first like the Python bot, telegra.ph (same API) when it is not reachable.
var telegraphClient = &http.Client{Timeout: 20 * time.Second}

var telegraphAPIs = []string{"https://api.graph.org", "https://api.telegra.ph"}

func telegraphCall(method string, form url.Values) (json.RawMessage, string, error) {
	var lastErr error
	for _, api := range telegraphAPIs {
		resp, err := telegraphClient.PostForm(api+"/"+method, form)
		if err != nil {
			lastErr = err
			continue
		}
		var res struct {
			OK     bool            `json:"ok"`
			Error  string          `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		err = json.NewDecoder(resp.Body).Decode(&res)
		_ = resp.Body.Close()
		if err == nil && res.OK {
			return res.Result, api, nil
		}
		lastErr = fmt.Errorf("telegraph %s: %s", method, res.Error)
	}
	return nil, "", lastErr
}

// getTelegraphToken creates the bot's own Telegraph account once.
func getTelegraphToken() (string, error) {
	telegraphMu.Lock()
	defer telegraphMu.Unlock()
	if telegraphToken != "" {
		return telegraphToken, nil
	}
	cfg := config.Get()
	shortName := randomLetters(8) // a random account, like the Python bot
	form := url.Values{}
	form.Set("short_name", shortName)
	if cfg.AuthorName != "" {
		form.Set("author_name", cfg.AuthorName)
	}
	if cfg.AuthorURL != "" {
		form.Set("author_url", cfg.AuthorURL)
	}
	raw, _, err := telegraphCall("createAccount", form)
	if err != nil {
		return "", err
	}
	var acc struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &acc); err != nil || acc.AccessToken == "" {
		return "", fmt.Errorf("telegraph account not created")
	}
	telegraphToken = acc.AccessToken
	log.Printf("Telegraph Account Generated : %s", shortName)
	return telegraphToken, nil
}

// createTelegraphPage publishes a page; content is a JSON array of Telegraph nodes.
func createTelegraphPage(title, content string) (string, error) {
	token, err := getTelegraphToken()
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("access_token", token)
	form.Set("title", title)
	if cfg := config.Get(); cfg.AuthorName != "" {
		form.Set("author_name", cfg.AuthorName)
		if cfg.AuthorURL != "" {
			form.Set("author_url", cfg.AuthorURL)
		}
	}
	form.Set("content", content)
	raw, api, err := telegraphCall("createPage", form)
	if err != nil {
		return "", err
	}
	var page struct {
		URL  string `json:"url"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return "", err
	}
	if page.URL != "" {
		return page.URL, nil
	}
	if strings.Contains(api, "graph.org") {
		return "https://graph.org/" + page.Path, nil
	}
	return "https://telegra.ph/" + page.Path, nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// mediaInfoLink makes the MediaInfo Telegraph page of a local file ("" on failure).
func mediaInfoLink(filePath, name string) string {
	info := runMediaInfo(filePath)
	if strings.TrimSpace(info) == "" {
		return ""
	}
	link, err := createTelegraphPage("MediaInfo", formatMediaInfoHTML(name, info))
	if err != nil {
		return ""
	}
	return link
}

func randomLetters(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
