package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
			"<blockquote>╭ <code>/mediainfo [download link]</code>\n" +
			"╰ Reply to a file or a link with <code>/mediainfo</code></blockquote>"
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

	respText := fmt.Sprintf("㊂ <b><u>MediaInfo :</u></b>\n\n<blockquote>╭ <b>Name :</b> <code>%s</code>\n╰ <b>Link :</b> <a href=\"%s\">MediaInfo</a></blockquote>", telegram.EscapeHTML(fileName), pageURL)
	_ = m.sender.EditMessage(ctx, peer, waitID, respText, telegram.CloseMarkup(userID))
}

func runMediaInfo(filePath string) string {
	if _, err := exec.LookPath("mediainfo"); err == nil {
		cmd := exec.Command("mediainfo", filePath)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil && out.Len() > 0 {
			return out.String()
		}
	}

	// Fallback to ffprobe
	if _, err := exec.LookPath("ffprobe"); err == nil {
		cmd := exec.Command("ffprobe", "-hide_banner", filePath)
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

func formatMediaInfoHTML(fileName, out string) string {
	sections := map[string]string{
		"General":  "◈",
		"Video":    "◈",
		"Audio":    "◈",
		"Text":     "◈",
		"Subtitle": "◈",
		"Menu":     "◈",
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<h4>◈ %s</h4><br>", fileName))

	lines := strings.Split(out, "\n")
	inPre := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHeader := false
		for sec, emoji := range sections {
			if strings.HasPrefix(trimmed, sec) {
				if inPre {
					sb.WriteString("</pre><br>")
				}
				sb.WriteString(fmt.Sprintf("<h4>%s %s</h4><br><pre>", emoji, trimmed))
				inPre = true
				isHeader = true
				break
			}
		}
		if !isHeader {
			if !inPre {
				sb.WriteString("<pre>")
				inPre = true
			}
			sb.WriteString(line + "\n")
		}
	}

	if inPre {
		sb.WriteString("</pre>")
	}
	return sb.String()
}

func createTelegraphPage(title, htmlContent string) (string, error) {
	apiURL := "https://api.graph.org/createPage"

	type telegraphNode struct {
		Tag      string          `json:"tag"`
		Children []interface{}   `json:"children,omitempty"`
		Attrs    json.RawMessage `json:"attrs,omitempty"`
	}

	// Create page with Telegraph / Graph.org API
	postData := url.Values{}
	postData.Set("access_token", "d3b25feccb89e508a9114afb82aa421fe2a9712b963b387cc62ac036ee2b")
	postData.Set("title", title)
	postData.Set("author_name", "MediaInfo")
	postData.Set("author_url", "https://t.me/TJ_Bots")

	contentNodes := []telegraphNode{
		{
			Tag:      "p",
			Children: []interface{}{htmlContent},
		},
	}
	contentJSON, _ := json.Marshal(contentNodes)
	postData.Set("content", string(contentJSON))
	postData.Set("return_content", "false")

	resp, err := http.PostForm(apiURL, postData)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Path string `json:"path"`
			URL  string `json:"url"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.OK {
		if result.Result.URL != "" {
			return result.Result.URL, nil
		}
		return "https://graph.org/" + result.Result.Path, nil
	}

	return "", fmt.Errorf("telegraph create page failed")
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
