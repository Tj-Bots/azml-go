package telegram

import (
	"context"
	"encoding/json"
	"strings"

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
