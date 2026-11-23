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
