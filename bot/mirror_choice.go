package bot

import (
	"azml-go/config"
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
			rest := append(append([]string{}, fields[:i]...), fields[i+2:]...)
			switch v := fields[i+1]; strings.ToLower(v) {
			case "gd", "gdrive", "tjgd": // like the Python bot: -up gd = Google Drive
				return strings.Join(rest, " "), ext.GDriveHostID
			case "rc", "rcl", "rclone", "tjrc": // -up rc = RCLONE_PATH
				return strings.Join(rest, " "), ext.RcloneHostID
			default:
				if strings.Contains(v, ":") && !strings.Contains(v, "://") { // -up remote:path
					return strings.Join(rest, " "), ext.RcloneHostID + "=" + v
				}
			case "ddl": // the DDL servers: the user's default one, else the question
				return strings.Join(rest, " "), "ddl"
			}
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
	// no -up: the user's Default Upload (/us), else DEFAULT_UPLOAD of /bs, like the Python bot
	// (gd only with GDRIVE_ID, rc only with RCLONE_PATH; otherwise the DDL servers)
	if host == "" {
		cfg := config.Get()
		up := cfg.DefaultUpload
		if b.userMap != nil {
			if u := b.userMap.Get(msgCtx.UserID); u != nil && u.DefaultUpload != "" {
				up = u.DefaultUpload
			}
		}
		userPath := ""
		if b.userMap != nil {
			if u := b.userMap.Get(msgCtx.UserID); u != nil {
				userPath = u.RclonePath
			}
		}
		switch {
		case up == "gd" && cfg.GDriveID != "":
			host = ext.GDriveHostID
		case up == "rc" && (cfg.RclonePath != "" || userPath != ""):
			host = ext.RcloneHostID
		}
	}
	if host == "ddl" {
		host = ""
	}
	if strings.HasPrefix(host, ext.RcloneHostID) {
		dest := config.Get().RclonePath // -up rc: his RClone Path (/us), else RCLONE_PATH
		if b.userMap != nil {
			if u := b.userMap.Get(msgCtx.UserID); u != nil && u.RclonePath != "" {
				dest = u.RclonePath
			}
		}
		if i := strings.Index(host, "="); i > 0 {
			dest = host[i+1:]
		}
		host, msgCtx.RcloneDest = ext.RcloneHostID, dest
	}
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
	// DDL without a server of his own: the first DDL server, like the Python bot (no question)
	var userKeys map[string]string
	if b.userMap != nil {
		if u := b.userMap.Get(msgCtx.UserID); u != nil {
			userKeys = u.DDLKeys
		}
	}
	msgCtx.MirrorHost = ext.AvailableMirrorHostsFor(userKeys)[0].ID
	b.leechHandler(ctx, peer, msgCtx, false, false)
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
