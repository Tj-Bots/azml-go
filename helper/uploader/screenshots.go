package uploader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"azml-go/config"
)

var botAPIUploadClient = &http.Client{Timeout: 2 * time.Minute}

// SendScreenshots sends the photos as an album (up to 10) replying to the uploaded file,
// like the Python bot, then copies the album to copyTo chats.
func SendScreenshots(chatID int64, replyTo int, files []string, caption string, copyTo []int64) error {
	if len(files) == 0 || chatID == 0 {
		return nil
	}
	token := config.Get().BotToken
	if token == "" {
		token = os.Getenv("BOT_TOKEN")
	}
	if len(files) > 10 {
		files = files[:10]
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	var media []map[string]string
	for i, f := range files {
		name := fmt.Sprintf("ss%d", i)
		item := map[string]string{"type": "photo", "media": "attach://" + name}
		if i == 0 && caption != "" {
			item["caption"] = caption
			item["parse_mode"] = "HTML"
		}
		media = append(media, item)
		part, err := w.CreateFormFile(name, filepath.Base(f))
		if err != nil {
			return err
		}
		fh, err := os.Open(f)
		if err != nil {
			return err
		}
		_, _ = io.Copy(part, fh)
		_ = fh.Close()
	}
	mj, _ := json.Marshal(media)
	_ = w.WriteField("chat_id", fmt.Sprint(chatID))
	_ = w.WriteField("media", string(mj))
	_ = w.WriteField("disable_notification", "true")
	if replyTo > 0 {
		rp, _ := json.Marshal(map[string]interface{}{"message_id": replyTo, "allow_sending_without_reply": true})
		_ = w.WriteField("reply_parameters", string(rp))
	}
	_ = w.Close()

	resp, err := botAPIUploadClient.Post(fmt.Sprintf("https://api.telegram.org/bot%s/sendMediaGroup", token), w.FormDataContentType(), &body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var res struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
		Result      []struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}
	if !res.Ok {
		return fmt.Errorf("sendMediaGroup: %s", res.Description)
	}
	var ids []int
	for _, m := range res.Result {
		ids = append(ids, m.MessageID)
	}
	sent := map[int64]bool{chatID: true}
	for _, to := range copyTo {
		if to == 0 || sent[to] {
			continue
		}
		sent[to] = true
		if !copyMessagesViaBotAPI(token, to, chatID, ids) {
			log.Printf("[Screenshots] copy to %d failed", to)
		}
	}
	return nil
}
