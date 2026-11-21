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
	"azml-go/helper/ext"
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

// Task gate like the Python bot (task_utils): in groups, a user who didn't start the bot in PM
// and/or didn't join the FSUB_IDS channels gets ONE numbered message with all the buttons.
// The message then follows the user: every item he completes disappears from it, and when
// nothing is left it says so (then deletes itself). Owner and sudo are never checked.

type gateState struct {
	pm    bool       // didn't start the bot in PM
	chans []fsubChat // channels not joined yet
}

func (g gateState) empty() bool { return !g.pm && len(g.chans) == 0 }

func (g gateState) key() string {
	k := fmt.Sprint(g.pm)
	for _, c := range g.chans {
		k += "|" + c.title
	}
	return k
}

// gateCheck returns what the user still has to do.
func (m *LeechModule) gateCheck(ctx context.Context, userID int64) gateState {
	var g gateState
	if u := m.userMap.Get(userID); u == nil || !u.StartedInPM {
		g.pm = true
	}
	for _, s := range strings.Fields(config.Get().FsubIDs) {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			continue
		}
		ch, info, ok := m.fsubInfo(ctx, id)
		if !ok {
			continue
		}
		_, err = m.rawAPI.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{Channel: ch,
			Participant: &tg.InputPeerUser{UserID: userID, AccessHash: telegram.GetUserAccessHash(userID)}})
		if err == nil {
			continue
		}
		if !tgerr.Is(err, "USER_NOT_PARTICIPANT") {
			log.Printf("[FSUB] check in %d failed: %v", id, err)
			continue // can't check: don't block the user
		}
		g.chans = append(g.chans, info)
	}
	return g
}

func gateRender(tag string, g gateState) (string, *telegram.ButtonMaker) {
	text := fmt.Sprintf("<b><i>User:</i> %s</b>,\n", tag)
	btn := telegram.NewButtonMaker()
	n := 0
	if g.pm {
		n++
		text += fmt.Sprintf("\n<b>%d</b>: <i>You didn't START the bot in PM (Private)</i>\n", n)
		bu := telegram.CurrentBotUsername
		btn.UButtonStyled("Start Bot Now", fmt.Sprintf("https://t.me/%s?start=start", bu), "primary")
	}
	if len(g.chans) > 0 {
		n++
		text += fmt.Sprintf("\n<b>%d</b>: You haven't joined our channel yet!\n", n)
		for _, c := range g.chans {
			if c.link != "" {
				btn.UButtonStyled("Join "+c.title, c.link, "primary")
			}
		}
	}
	text += "\n<i>This message updates by itself when you're done.</i>"
	return text, btn
}

// forceSubOK runs the gate; false = the user got the message and the task does not start now.
// With resume, the task starts by itself as soon as the user is done (joined, started the bot),
// like the Retry button; the command and its replied file stay until then.
func (m *LeechModule) forceSubOK(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, resume ...func()) bool {
	if msg.ChatID == msg.UserID || msg.Internal || telegram.IsOwner(msg.UserID) || telegram.IsSudo(msg.UserID, m.userMap) {
		return true
	}
	g := m.gateCheck(ctx, msg.UserID)
	if g.empty() {
		return true
	}
	tag := fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", msg.UserID, telegram.EscapeHTML(msg.SenderName))
	if msg.Username != "" {
		tag = "@" + msg.Username
	}
	text, btn := gateRender(tag, g)
	id, err := m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 2)
	if err != nil || id == 0 {
		return false
	}
	var run func()
	if len(resume) > 0 {
		run = resume[0]
	}
	if run == nil {
		m.deleteCommandLinks(peer, msg)
	}
	go m.gateFollow(peer, msg, id, tag, g, run)
	return false
}

// gateFollow re-checks the user every few seconds (10 minutes at most) and edits the message.
func (m *LeechModule) gateFollow(peer tg.InputPeerClass, msg *types.MessageContext, msgID int, tag string, g gateState, resume func()) {
	ctx := context.Background()
	userID := msg.UserID
	last := g.key()
	kick, stop := ext.GateWatch(userID)
	defer stop()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-kick: // he joined a channel / started the bot: check now
			time.Sleep(700 * time.Millisecond)
		case <-time.After(4 * time.Second):
		}
		cur := m.gateCheck(ctx, userID)
		if cur.empty() {
			if resume != nil { // the task starts now, the gate message goes
				_ = m.sender.EditMessage(ctx, peer, msgID, fmt.Sprintf("<b><i>User:</i> %s</b>,\n\n✓ <b>All set!</b> <i>Starting your task...</i>", tag), nil)
				m.sender.AutoDeleteMessage(peer, msgID, 3)
				resume()
				return
			}
			done := fmt.Sprintf("<b><i>User:</i> %s</b>,\n\n✓ <b>All set!</b> Send the command again.", tag)
			_ = m.sender.EditMessage(ctx, peer, msgID, done, nil)
			m.sender.AutoDeleteMessage(peer, msgID, 60)
			return
		}
		if k := cur.key(); k != last {
			last = k
			text, btn := gateRender(tag, cur)
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		}
	}
	_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
	if resume != nil { // gave up: the waiting command goes now (DELETE_LINKS)
		m.deleteCommandLinks(peer, msg)
	}
}

// ---- USER_TIME_INTERVAL: a user waits between tasks (Python bot: timeval_check) ----

var (
	userTaskMu   sync.Mutex
	userLastTask = map[int64]time.Time{}
)

// timeIntervalOK refuses a new task too soon after the user's last one.
func (m *LeechModule) timeIntervalOK(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) bool {
	iv := config.Get().UserTimeInterval
	if iv <= 0 || telegram.IsOwner(msg.UserID) || telegram.IsSudo(msg.UserID, m.userMap) {
		return true
	}
	limit := time.Duration(iv) * time.Second
	userTaskMu.Lock()
	last, ok := userLastTask[msg.UserID]
	if ok && time.Since(last) < limit {
		userTaskMu.Unlock()
		left := (limit - time.Since(last)).Round(time.Second)
		text := fmt.Sprintf("<blockquote>Please Wait %s, Users have time interval Restrictions for %s.</blockquote>", left, limit)
		id, _ := m.sender.SendMessage(ctx, peer, text, msg.MessageID, nil)
		if id > 0 {
			m.sender.AutoDeleteMessage(peer, id, 10)
		}
		m.deleteCommandLinks(peer, msg)
		return false
	}
	userLastTask[msg.UserID] = time.Now()
	userTaskMu.Unlock()
	return true
}

// TaskGate is the PM / FSUB check for commands that start in bot/ (like /mirror before its host question).
func (m *LeechModule) TaskGate(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, resume func()) bool {
	return m.forceSubOK(ctx, peer, msg, resume)
}
