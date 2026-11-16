package modules

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/helper/pool"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// Force subscribe (FSUB_IDS) like the Python bot: in groups, users must join these channels
// before they can start tasks. Owner and sudo are never checked.

type fsubChat struct {
	title string
	link  string
	at    time.Time
}

var (
	fsubMu    sync.Mutex
	fsubCache = map[int64]fsubChat{}
)

func (m *LeechModule) fsubInfo(ctx context.Context, chatID int64) (*tg.InputChannel, fsubChat, bool) {
	raw := pool.RawChannelID(chatID)
	hash := telegram.ResolveChannelAccessHash(ctx, m.rawAPI, chatID)
	if hash == 0 {
		return nil, fsubChat{}, false
	}
	ch := &tg.InputChannel{ChannelID: raw, AccessHash: hash}
	fsubMu.Lock()
	c, ok := fsubCache[raw]
	fsubMu.Unlock()
	if ok && time.Since(c.at) < time.Hour {
		return ch, c, true
	}
	full, err := m.rawAPI.ChannelsGetFullChannel(ctx, ch)
	if err != nil {
		log.Printf("[FSUB] can't read %d: %v (is the bot admin there?)", chatID, err)
		return ch, fsubChat{}, false
	}
	c = fsubChat{at: time.Now()}
	for _, x := range full.Chats {
		if cc, ok := x.(*tg.Channel); ok && cc.ID == raw {
			c.title = cc.Title
			if cc.Username != "" {
				c.link = "https://t.me/" + cc.Username
			}
		}
	}
	if c.link == "" {
		if f, ok := full.FullChat.(*tg.ChannelFull); ok {
			if inv, ok := f.ExportedInvite.(*tg.ChatInviteExported); ok {
				c.link = inv.Link
			}
		}
	}
	if c.link == "" {
		if inv, err := m.rawAPI.MessagesExportChatInvite(ctx, &tg.MessagesExportChatInviteRequest{Peer: &tg.InputPeerChannel{ChannelID: raw, AccessHash: hash}}); err == nil {
			if e, ok := inv.(*tg.ChatInviteExported); ok {
				c.link = e.Link
			}
		}
	}
	if c.title == "" {
		c.title = "Channel"
	}
	fsubMu.Lock()
	fsubCache[raw] = c
	fsubMu.Unlock()
	return ch, c, true
}

// forceSubOK sends the "join first" message and returns false when the user is missing a channel.
func (m *LeechModule) forceSubOK(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) bool {
	ids := strings.Fields(config.Get().FsubIDs)
	if len(ids) == 0 || msg.ChatID == msg.UserID || telegram.IsOwner(msg.UserID) || telegram.IsSudo(msg.UserID, m.userMap) {
		return true
	}
	user := &tg.InputUser{UserID: msg.UserID, AccessHash: telegram.GetUserAccessHash(msg.UserID)}
	btn := telegram.NewButtonMaker()
	missing := 0
	for _, s := range ids {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			continue
		}
		ch, info, ok := m.fsubInfo(ctx, id)
		if !ok {
			continue
		}
		_, err = m.rawAPI.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{Channel: ch, Participant: &tg.InputPeerUser{UserID: user.UserID, AccessHash: user.AccessHash}})
		if err == nil {
			continue
		}
		if !tgerr.Is(err, "USER_NOT_PARTICIPANT") {
			log.Printf("[FSUB] check in %d failed: %v", id, err)
			continue // can't check: don't block the user
		}
		missing++
		if info.link != "" {
			btn.UButtonStyled("Join "+info.title, info.link, "primary")
			btn.Row()
		}
	}
	if missing == 0 {
		return true
	}
	tag := fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", msg.UserID, telegram.EscapeHTML(msg.SenderName))
	if msg.Username != "" {
		tag = "@" + msg.Username
	}
	text := fmt.Sprintf("㊂ <b><u>Join First :</u></b>\n\n<blockquote>Hey %s, you haven't joined our channel yet!\nJoin and send the command again.</blockquote>", tag)
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 1)
	return false
}
