package modules

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"azml-go/config"
	"azml-go/helper/telegram"

	"github.com/gotd/td/tg"
)

// File selection message like the Python bot: Select Files (web page), Pincode (pop-up only for
// the task's user), Cancel and Done Selecting. The task waits until Done or Cancel.

type selSession struct {
	userID int64
	pin    string
	done   chan bool
}

var selSessions sync.Map // id -> *selSession

func selPin(id string) string {
	pin := ""
	for _, ch := range id {
		if unicode.IsDigit(ch) {
			pin += string(ch)
			if len(pin) == 4 {
				break
			}
		}
	}
	for len(pin) < 4 {
		pin += "0"
	}
	return pin
}

// selectionButtons: web id = the id the web page knows (aria2 GID), key = short callback id.
func selectionButtons(webID, key string) *telegram.ButtonMaker {
	cfg := config.Get()
	base := strings.TrimSuffix(cfg.BaseURL, "/")
	if base == "" {
		base = fmt.Sprintf("http://localhost:%d", cfg.BaseURLPort)
	}
	b := telegram.NewButtonMaker()
	b.UButtonStyled("Select Files", fmt.Sprintf("%s/app/files/%s", base, webID), "primary")
	b.IButton("Pincode", "sel pin "+key)
	b.Row()
	b.IButtonStyled("Cancel", "sel rm "+key, "danger")
	b.IButtonStyled("Done Selecting", "sel done "+key, "success")
	b.Row()
	return b
}

// waitSelection shows the selection message on msgID and blocks until Done (true) or Cancel.
func (m *LeechModule) waitSelection(ctx context.Context, peer tg.InputPeerClass, replyTo int, userID int64, webID, what string) bool {
	key := webID
	if len(key) > 24 {
		key = key[:24]
	}
	s := &selSession{userID: userID, pin: selPin(webID), done: make(chan bool, 1)}
	selSessions.Store(key, s)
	defer selSessions.Delete(key)
	text := fmt.Sprintf("㊂ <b><u>Select Files :</u></b>\n\n<blockquote>Your %s download is paused. Choose the files on the web page, then press <b>Done Selecting</b> to start downloading.</blockquote>", what)
	if config.Get().BaseURL == "" {
		text += "\n\n⚠️ <i>BASE_URL is not set in /bs, the web page will not open.</i>"
	}
	// its own message (the status message keeps refreshing), deleted when done
	msgID, _ := m.sender.SendMessageWithButtons(ctx, peer, text, replyTo, selectionButtons(webID, key), 2)
	defer func() {
		if msgID > 0 {
			_ = m.sender.DeleteMessage(context.Background(), peer, []int{msgID})
		}
	}()
	select {
	case ok := <-s.done:
		return ok
	case <-ctx.Done():
		return false
	}
}

// HandleSelectCallback: "sel pin|rm|done <key>".
func (m *LeechModule) HandleSelectCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 {
		return
	}
	v, ok := selSessions.Load(parts[2])
	if !ok {
		_ = m.sender.AnswerCallback(ctx, queryID, "This task is not available anymore!", true)
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}
	s := v.(*selSession)
	if fromUID != s.userID && !telegram.IsOwner(fromUID) && !telegram.IsSudo(fromUID, m.userMap) {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	switch parts[1] {
	case "pin":
		_ = m.sender.AnswerCallback(ctx, queryID, "Pincode: "+s.pin, true)
	case "rm":
		_ = m.sender.AnswerCallback(ctx, queryID, "Task has been cancelled!", false)
		select {
		case s.done <- false:
		default:
		}
	case "done":
		_ = m.sender.AnswerCallback(ctx, queryID, "Starting the download...", false)
		select {
		case s.done <- true:
		default:
		}
	}
}
