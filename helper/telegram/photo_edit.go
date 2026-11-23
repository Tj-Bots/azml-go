package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// SendPhotoURL sends a photo from a URL with an HTML caption and buttons (Bot API).
func (m *MessageSender) SendPhotoURL(ctx context.Context, peer tg.InputPeerClass, photoURL, caption string, replyTo int, btn *ButtonMaker, cols int) (int, error) {
	p := map[string]interface{}{
		"chat_id":    PeerToChatID(peer),
		"photo":      photoURL,
		"caption":    caption,
		"parse_mode": "HTML",
	}
	if btn != nil {
		p["reply_markup"] = json.RawMessage(btn.BuildBotAPIKeyboard(cols))
	}
	if replyTo > 0 {
		p["reply_parameters"] = map[string]interface{}{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	r, err := botAPICall(ctx, "sendPhoto", p)
	if err != nil {
		return 0, err
	}
	return r.Result.MessageID, nil
}

// EditPhotoURL replaces the photo, caption and buttons of a photo message (Bot API editMessageMedia).
func (m *MessageSender) EditPhotoURL(ctx context.Context, peer tg.InputPeerClass, msgID int, photoURL, caption string, btn *ButtonMaker, cols int) error {
	p := map[string]interface{}{
		"chat_id":    PeerToChatID(peer),
		"message_id": msgID,
		"media": map[string]interface{}{
			"type": "photo", "media": photoURL, "caption": caption, "parse_mode": "HTML",
		},
	}
	if btn != nil {
		p["reply_markup"] = json.RawMessage(btn.BuildBotAPIKeyboard(cols))
	}
	_, err := botAPICall(ctx, "editMessageMedia", p)
	if err != nil && strings.Contains(err.Error(), "not modified") {
		return nil
	}
	return err
}

// EditButtons replaces only the buttons of a message (Bot API editMessageReplyMarkup).
func (m *MessageSender) EditButtons(ctx context.Context, peer tg.InputPeerClass, msgID int, btn *ButtonMaker, cols int) error {
	p := map[string]interface{}{"chat_id": PeerToChatID(peer), "message_id": msgID}
	if btn != nil {
		p["reply_markup"] = json.RawMessage(btn.BuildBotAPIKeyboard(cols))
	}
	_, err := botAPICall(ctx, "editMessageReplyMarkup", p)
	if err != nil && strings.Contains(err.Error(), "not modified") {
		return nil
	}
	return err
}

// previewOptions: a link preview of url, big and above the text (the "modern" look); "" = none.
func previewOptions(url string) map[string]interface{} {
	if url == "" {
		return map[string]interface{}{"is_disabled": true}
	}
	return map[string]interface{}{"url": url, "prefer_large_media": true, "show_above_text": true}
}

// SendWithPreview sends an HTML text with url as a large preview above it (Bot API).
func (m *MessageSender) SendWithPreview(ctx context.Context, peer tg.InputPeerClass, text, url string, replyTo int, btn *ButtonMaker, cols int) (int, error) {
	p := map[string]interface{}{
		"chat_id":              PeerToChatID(peer),
		"text":                 text,
		"parse_mode":           "HTML",
		"link_preview_options": previewOptions(url),
	}
	if btn != nil {
		p["reply_markup"] = json.RawMessage(btn.BuildBotAPIKeyboard(cols))
	}
	if replyTo > 0 {
		p["reply_parameters"] = map[string]interface{}{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	r, err := botAPICall(ctx, "sendMessage", p)
	if err != nil {
		return 0, err
	}
	return r.Result.MessageID, nil
}

// EditWithPreview edits a text message: new text, url as its large preview above ("" = none).
func (m *MessageSender) EditWithPreview(ctx context.Context, peer tg.InputPeerClass, msgID int, text, url string, btn *ButtonMaker, cols int) error {
	p := map[string]interface{}{
		"chat_id":              PeerToChatID(peer),
		"message_id":           msgID,
		"text":                 text,
		"parse_mode":           "HTML",
		"link_preview_options": previewOptions(url),
	}
	if btn != nil {
		p["reply_markup"] = json.RawMessage(btn.BuildBotAPIKeyboard(cols))
	}
	_, err := botAPICall(ctx, "editMessageText", p)
	if err != nil && strings.Contains(err.Error(), "not modified") {
		return nil
	}
	return err
}

// CreateInviteLink: a new invite link of a chat (Bot API), for memberLimit people, valid for ttl.
func CreateInviteLink(ctx context.Context, chatID int64, name string, memberLimit int, ttl time.Duration) (string, error) {
	p := map[string]interface{}{"chat_id": chatID, "name": name}
	if memberLimit > 0 {
		p["member_limit"] = memberLimit
	}
	if ttl > 0 {
		p["expire_date"] = time.Now().Add(ttl).Unix()
	}
	token := getBotToken()
	if token == "" {
		return "", fmt.Errorf("no bot token")
	}
	body, _ := json.Marshal(p)
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.telegram.org/bot%s/createChatInviteLink", token), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := botAPIHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			InviteLink string `json:"invite_link"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if !r.Ok {
		return "", fmt.Errorf("%s", r.Description)
	}
	return r.Result.InviteLink, nil
}

// RemoveFromChat removes a user from a chat without banning him (ban + unban, Bot API).
func RemoveFromChat(ctx context.Context, chatID, userID int64) error {
	if _, err := botAPICall(ctx, "banChatMember", map[string]interface{}{"chat_id": chatID, "user_id": userID}); err != nil {
		return err
	}
	_, err := botAPICall(ctx, "unbanChatMember", map[string]interface{}{"chat_id": chatID, "user_id": userID, "only_if_banned": true})
	return err
}
