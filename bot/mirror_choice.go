package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type pendingMirror struct {
	peer tg.InputPeerClass
	msg  *types.MessageContext
	at   time.Time
}

// pendingMirrors maps "chatID:promptMsgID" to the /mirror request waiting for a host choice.
var pendingMirrors sync.Map

func mirrorKey(chatID int64, msgID int) string { return fmt.Sprintf("%d:%d", chatID, msgID) }

// splitMirrorHost removes "-up <host>" from the command text and returns the chosen host id ("" if none).
func splitMirrorHost(text string) (string, string) {
	fields := strings.Fields(text)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "-up" {
			if h, ok := ext.FindMirrorHost(fields[i+1]); ok {
				rest := append(append([]string{}, fields[:i]...), fields[i+2:]...)
				return strings.Join(rest, " "), h.ID
			}
		}
	}
	return text, ""
}

// handleMirrorCommand runs /mirror: with "-up host" it starts at once, otherwise it asks for the host.
func (b *BotApp) handleMirrorCommand(ctx context.Context, peer tg.InputPeerClass, msgCtx *types.MessageContext) {
	msgCtx.IsMirror = true
	if b.leechHandler == nil {
		return
	}
	text, host := splitMirrorHost(msgCtx.Text)
	msgCtx.Text = text
	if host == "" && b.userMap != nil {
		if u := b.userMap.Get(msgCtx.UserID); u != nil && u.MirrorHost != "" {
			for _, h := range ext.AvailableMirrorHostsFor(u.DDLKeys) {
				if h.ID == u.MirrorHost {
					host = h.ID
				}
			}
		}
	}
	if host != "" {
		msgCtx.MirrorHost = host
		b.leechHandler(ctx, peer, msgCtx, false, false)
		return
	}

	// nothing to mirror yet: the leech module shows the usage
	if !hasMirrorTarget(msgCtx) {
		b.leechHandler(ctx, peer, msgCtx, false, false)
		return
	}
	if b.gateHandler != nil && !b.gateHandler(ctx, peer, msgCtx, func() { b.handleMirrorCommand(context.Background(), peer, msgCtx) }) {
		return // before the host question
	}
	var userKeys map[string]string
	if b.userMap != nil {
		if u := b.userMap.Get(msgCtx.UserID); u != nil {
			userKeys = u.DDLKeys
		}
	}
	hosts := ext.AvailableMirrorHostsFor(userKeys)
	btn := telegram.NewButtonMaker()
	for i, h := range hosts {
		btn.IButton(h.Name, fmt.Sprintf("mirror %d %s", msgCtx.UserID, h.ID))
		if i%2 == 1 {
			btn.Row()
		}
	}
	btn.Row()
	btn.IButtonStyled("Cancel", fmt.Sprintf("mirror %d cancel", msgCtx.UserID), "danger")
	btn.Row()

	prompt := "⌬ <b><u>Mirror</u></b>\n\n<blockquote><b>╭</b> <b>Choose where to upload</b>\n<b>╰</b> <i>Or use</i> <code>-up gofile</code></blockquote>"
	promptID, err := b.sender.SendMessageWithButtons(ctx, peer, prompt, msgCtx.MessageID, btn, 2)
	if err != nil || promptID == 0 {
		// no buttons possible: fall back to the first host
		msgCtx.MirrorHost = hosts[0].ID
		b.leechHandler(ctx, peer, msgCtx, false, false)
		return
	}
	pendingMirrors.Store(mirrorKey(telegram.PeerToChatID(peer), promptID), pendingMirror{peer: peer, msg: msgCtx, at: time.Now()})
	promptChat := telegram.PeerToChatID(peer)
	time.AfterFunc(5*time.Minute, func() { pendingMirrors.Delete(mirrorKey(promptChat, promptID)) })
}

// handleMirrorCallback handles the buttons of the host choice: "mirror <userID> <host|cancel>".
func (b *BotApp) handleMirrorCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 {
		_ = b.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	if owner, err := strconv.ParseInt(parts[1], 10, 64); err != nil || owner != fromUID {
		_ = b.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	chatID := telegram.PeerToChatID(peer)
	val, ok := pendingMirrors.LoadAndDelete(mirrorKey(chatID, msgID))
	if !ok {
		_ = b.sender.AnswerCallback(ctx, queryID, "This request expired, send the command again.", true)
		_ = b.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}
	req := val.(pendingMirror)
	_ = b.sender.DeleteMessage(ctx, peer, []int{msgID})
	if parts[2] == "cancel" {
		_ = b.sender.AnswerCallback(ctx, queryID, "Cancelled", false)
		return
	}
	h, found := ext.FindMirrorHost(parts[2])
	if !found {
		_ = b.sender.AnswerCallback(ctx, queryID, "Unknown host", true)
		return
	}
	_ = b.sender.AnswerCallback(ctx, queryID, "Uploading to "+h.Name, false)
	req.msg.MirrorHost = h.ID
	if b.leechHandler != nil {
		go b.leechHandler(context.Background(), req.peer, req.msg, false, false)
	}
}

// hasMirrorTarget: a link in the command, or a reply / file to take it from.
func hasMirrorTarget(msgCtx *types.MessageContext) bool {
	if msgCtx.IsReply || msgCtx.HasMedia {
		return true
	}
	for _, f := range strings.Fields(msgCtx.Text) {
		if strings.Contains(f, "://") || strings.HasPrefix(f, "magnet:") || strings.HasPrefix(f, "t.me/") {
			return true
		}
	}
	return false
}
