package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gotd/td/tg"
)

// Telegram Rich Messages (Bot API sendRichMessage / editMessageText rich_message), used for the
// status like the Python bot's RICH_MESSAGES: buttons inside every task and lines between tasks.

var (
	richBlockBefore = regexp.MustCompile(`(?:<br>\s*)+(<(?:/?blockquote|hr|/?p)\b[^>]*>)`)
	richBlockAfter  = regexp.MustCompile(`(</blockquote>|<hr/>|</p>)(?:\s*<br>)+`)
	richLineStart   = regexp.MustCompile(`(<br>|<blockquote>|<p>)`)
)

// ToRichHTML turns the normal HTML text into rich HTML (line breaks as <br>, every line LTR).
func ToRichHTML(text string) string {
	h := strings.ReplaceAll(strings.TrimSpace(text), "\n", "<br>")
	h = richBlockBefore.ReplaceAllString(h, "$1")
	h = richBlockAfter.ReplaceAllString(h, "$1")
	// keep lines left-to-right also when they start with a Hebrew name
	h = richLineStart.ReplaceAllStringFunc(h, func(s string) string { return s + "‎" })
	return h
}

// RichButton is an inline button inside a rich message.
func RichButton(text, data, style string) string {
	return fmt.Sprintf(`<tg-button type="callback_data" style="%s" data="%s">%s</tg-button>`, style, EscapeHTML(data), EscapeHTML(text))
}

type botAPIResult struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		MessageID int `json:"message_id"`
	} `json:"result"`
}

func botAPICall(ctx context.Context, method string, payload map[string]interface{}) (*botAPIResult, error) {
	token := getBotToken()
	if token == "" {
		return nil, fmt.Errorf("no bot token")
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := botAPIHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r botAPIResult
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if !r.Ok {
		return &r, fmt.Errorf("%s", r.Description)
	}
	return &r, nil
}

func richPayload(chatID int64, html string, btn *ButtonMaker) map[string]interface{} {
	p := map[string]interface{}{
		"chat_id":      chatID,
		"rich_message": map[string]interface{}{"html": html}, // hashtags, links, @users clickable (like the Python bot)
	}
	if btn != nil {
		if kb := btn.BuildBotAPIKeyboard(3); kb != "" {
			p["reply_markup"] = json.RawMessage(kb)
		}
	}
	return p
}

// SendRich sends a rich message.
func (m *MessageSender) SendRich(ctx context.Context, peer tg.InputPeerClass, html string, replyTo int, btn *ButtonMaker) (int, error) {
	p := richPayload(PeerToChatID(peer), html, btn)
	if replyTo > 0 {
		p["reply_parameters"] = map[string]interface{}{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	r, err := botAPICall(ctx, "sendRichMessage", p)
	if err != nil {
		return 0, err
	}
	return r.Result.MessageID, nil
}

// EditRich edits a rich message ("message is not modified" counts as success).
func (m *MessageSender) EditRich(ctx context.Context, peer tg.InputPeerClass, msgID int, html string, btn *ButtonMaker) error {
	p := richPayload(PeerToChatID(peer), html, btn)
	p["message_id"] = msgID
	_, err := botAPICall(ctx, "editMessageText", p)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

// RichKeyboardHTML puts the buttons inside the rich message (an edit of a rich message keeps them,
// a reply_markup on it was dropped).
func RichKeyboardHTML(btn *ButtonMaker) string {
	if btn == nil {
		return ""
	}
	var sb strings.Builder
	for _, row := range btn.Rows(3) {
		if len(row) == 0 {
			continue
		}
		sb.WriteString(`<tg-button-row align="center">`)
		for _, b := range row {
			style := ""
			if b.Style != "" {
				style = fmt.Sprintf(` style="%s"`, b.Style)
			}
			if b.URL != "" {
				sb.WriteString(fmt.Sprintf(`<tg-button type="url"%s url="%s">%s</tg-button>`, style, EscapeHTML(b.URL), EscapeHTML(b.Text)))
			} else {
				sb.WriteString(fmt.Sprintf(`<tg-button type="callback_data"%s data="%s">%s</tg-button>`, style, EscapeHTML(b.Data), EscapeHTML(b.Text)))
			}
		}
		sb.WriteString(`</tg-button-row>`)
	}
	return sb.String()
}

// SendEphemeral answers a button press with a message only the presser sees (also in groups).
func (m *MessageSender) SendEphemeral(ctx context.Context, peer tg.InputPeerClass, queryID int64, text string, btn *ButtonMaker) (int, error) {
	p := map[string]interface{}{
		"chat_id":                      PeerToChatID(peer),
		"text":                         text,
		"parse_mode":                   "HTML",
		"ephemeral_message_parameters": map[string]interface{}{"callback_query_id": fmt.Sprint(queryID)},
	}
	if btn != nil {
		if kb := btn.BuildBotAPIKeyboard(2); kb != "" {
			p["reply_markup"] = json.RawMessage(kb)
		}
	}
	r, err := botAPICall(ctx, "sendMessage", p)
	if err != nil {
		return 0, err
	}
	return r.Result.MessageID, nil
}
