package modules

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// /broadcast like the Python bot: the replied message is copied (media, formatting, buttons)
// to every user who started the bot; -f forwards it with its tag, -q sends it quietly; with the
// broadcast ID it can be edited (-e) or deleted (-d) until the next restart.

type BroadcastModule struct {
	rawAPI  *tg.Client
	sender  *telegram.MessageSender
	userMap *types.SafeUserMap
}

func NewBroadcastModule(rawAPI *tg.Client, sender *telegram.MessageSender, userMap *types.SafeUserMap) *BroadcastModule {
	return &BroadcastModule{rawAPI: rawAPI, sender: sender, userMap: userMap}
}

type bcSent struct {
	peer      *tg.InputPeerUser
	msgID     int
	forwarded bool
}

var (
	bcMu    sync.Mutex
	bcCache = map[string][]bcSent{}
	bcBusy  bool
)

const bcUsage = "㊂ <b><u>Broadcast :</u></b>\n\n" +
	"<blockquote><b>Reply to a message:</b> <code>/broadcast</code> — copy it to every user\n" +
	"<code>-f</code> / <code>-forward</code> — forward it (with its tag)\n" +
	"<code>-q</code> / <code>-quiet</code> — without notification\n\n" +
	"<b>Edit:</b> reply to the new text with <code>/broadcast ID -e</code>\n" +
	"<b>Delete:</b> <code>/broadcast ID -d</code>\n\n" +
	"<b>Text:</b> <code>/broadcast your message</code></blockquote>\n\n" +
	"<i>Broadcasts can be edited or deleted only until the next restart. Forwarded ones can only be deleted.</i>"

func (m *BroadcastModule) HandleBroadcast(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner and Sudo can broadcast messages!", msg.MessageID, nil)
		return
	}
	args := strings.Fields(msg.Text)
	var bcID, textArgs []string
	forward, quiet, del, edit := false, false, false, false
	for _, a := range args[1:] {
		switch strings.ToLower(a) {
		case "-f", "-forward":
			forward = true
		case "-q", "-quiet":
			quiet = true
		case "-d", "-delete":
			del = true
		case "-e", "-edit":
			edit = true
		default:
			bcMu.Lock()
			_, known := bcCache[a]
			bcMu.Unlock()
			if known && len(bcID) == 0 && len(textArgs) == 0 {
				bcID = append(bcID, a)
			} else {
				textArgs = append(textArgs, a)
			}
		}
	}
	reply := func(text string) {
		_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, telegram.CloseMarkup(msg.UserID))
	}

	if del || edit {
		if len(bcID) == 0 {
			reply("<i>Broadcast ID not found! After a restart broadcasts can't be edited or deleted.</i>")
			return
		}
		if edit && (!msg.IsReply || msg.ReplyToID <= 0) {
			reply("<i>Reply to the new message with</i> <code>/broadcast ID -e</code>")
			return
		}
		go m.editOrDelete(peer, msg, bcID[0], del)
		return
	}

	hasReply := msg.IsReply && msg.ReplyToID > 0
	text := strings.Join(textArgs, " ")
	if !hasReply && text == "" {
		reply(bcUsage)
		return
	}
	if !hasReply && forward {
		reply("<i>-f needs a replied message.</i>")
		return
	}
	bcMu.Lock()
	if bcBusy {
		bcMu.Unlock()
		reply("<i>A broadcast is already running.</i>")
		return
	}
	bcBusy = true
	bcMu.Unlock()
	go m.run(peer, msg, hasReply, text, forward, quiet)
}

// bcTargets: the users who started the bot in PM (not blacklisted).
func (m *BroadcastModule) bcTargets() []*tg.InputPeerUser {
	var out []*tg.InputPeerUser
	for _, u := range m.userMap.GetAllUsers() {
		if u == nil || u.UserID <= 0 || !u.StartedInPM || u.IsBlacklist {
			continue
		}
		out = append(out, &tg.InputPeerUser{UserID: u.UserID, AccessHash: telegram.GetUserAccessHash(u.UserID)})
	}
	return out
}

// bcRetry: one call, again after a flood wait.
func bcRetry(ctx context.Context, f func() error) error {
	for i := 0; i < 3; i++ {
		err := f()
		if d, ok := tgerr.AsFloodWait(err); ok {
			time.Sleep(d + time.Second)
			continue
		}
		return err
	}
	return fmt.Errorf("flood wait")
}

func sentMsgID(u tg.UpdatesClass) int {
	var list []tg.UpdateClass
	switch v := u.(type) {
	case *tg.Updates:
		list = v.Updates
	case *tg.UpdatesCombined:
		list = v.Updates
	case *tg.UpdateShortSentMessage:
		return v.ID
	}
	for _, up := range list {
		switch x := up.(type) {
		case *tg.UpdateMessageID:
			return x.ID
		case *tg.UpdateNewMessage:
			if mm, ok := x.Message.(*tg.Message); ok {
				return mm.ID
			}
		}
	}
	return 0
}

func (m *BroadcastModule) run(peer tg.InputPeerClass, msg *types.MessageContext, hasReply bool, text string, forward, quiet bool) {
	defer func() {
		bcMu.Lock()
		bcBusy = false
		bcMu.Unlock()
	}()
	ctx := context.Background()
	targets := m.bcTargets()
	start := time.Now()
	id := ext.RandomHex(4)
	statID, _ := m.sender.SendMessage(ctx, peer, fmt.Sprintf("<i>Broadcasting to %d users... Please wait</i>", len(targets)), msg.MessageID, nil)

	plain, entities := telegram.ParseFormatting(text)
	var sent []bcSent
	success, blocked, deleted, failed := 0, 0, 0, 0
	lastEdit := time.Now()
	for i, t := range targets {
		if t.AccessHash == 0 {
			failed++
			continue
		}
		var up tg.UpdatesClass
		err := bcRetry(ctx, func() error {
			var e error
			if hasReply {
				up, e = m.rawAPI.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
					FromPeer:   peer,
					ID:         []int{msg.ReplyToID},
					RandomID:   []int64{rand.Int63()},
					ToPeer:     t,
					DropAuthor: !forward, // a copy, not a forward
					Silent:     quiet,
				})
			} else {
				up, e = m.rawAPI.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
					Peer: t, Message: plain, Entities: entities, RandomID: rand.Int63(), Silent: quiet, NoWebpage: true,
				})
			}
			return e
		})
		switch {
		case err == nil:
			success++
			if mid := sentMsgID(up); mid > 0 {
				sent = append(sent, bcSent{peer: t, msgID: mid, forwarded: forward})
			}
		case strings.Contains(err.Error(), "USER_IS_BLOCKED"):
			blocked++
		case strings.Contains(err.Error(), "INPUT_USER_DEACTIVATED") || strings.Contains(err.Error(), "USER_DEACTIVATED"):
			deleted++
		default:
			failed++
		}
		time.Sleep(60 * time.Millisecond)
		if statID > 0 && time.Since(lastEdit) > 10*time.Second {
			lastEdit = time.Now()
			_ = m.sender.EditMessage(ctx, peer, statID, fmt.Sprintf("<i>Broadcasting... %d / %d</i>", i+1, len(targets)), nil)
		}
	}
	bcMu.Lock()
	bcCache[id] = sent
	bcMu.Unlock()

	res := fmt.Sprintf("⌬ <b><i>Broadcast Stats :</i></b>\n"+
		"<blockquote><b>╭</b> <b>Total Users:</b> <code>%d</code>\n"+
		"┠ <b>Success:</b> <code>%d</code>\n"+
		"┠ <b>Blocked Users:</b> <code>%d</code>\n"+
		"┠ <b>Deleted Accounts:</b> <code>%d</code>\n"+
		"┠ <b>Unsuccess Attempt:</b> <code>%d</code>\n"+
		"<b>╰</b> <b>Time Taken:</b> <code>%s</code></blockquote>\n\n"+
		"<b>Broadcast ID:</b> <code>%s</code>",
		len(targets), success, blocked, deleted, failed, ext.GetReadableTime(int64(time.Since(start).Seconds())), id)
	if statID > 0 {
		_ = m.sender.EditMessage(ctx, peer, statID, res, telegram.CloseMarkup(msg.UserID))
	} else {
		_, _ = m.sender.SendMessage(ctx, peer, res, msg.MessageID, telegram.CloseMarkup(msg.UserID))
	}
}

func (m *BroadcastModule) editOrDelete(peer tg.InputPeerClass, msg *types.MessageContext, id string, del bool) {
	ctx := context.Background()
	bcMu.Lock()
	list := append([]bcSent(nil), bcCache[id]...)
	bcMu.Unlock()
	what := "Editing"
	if del {
		what = "Deleting"
	}
	statID, _ := m.sender.SendMessage(ctx, peer, "<i>"+what+" the broadcasted message! Please wait...</i>", msg.MessageID, nil)

	var newText string
	var newEnt []tg.MessageEntityClass
	if !del {
		src, err := telegram.FetchMessage(ctx, m.rawAPI, peer, msg.ReplyToID)
		if err != nil || src == nil || src.Message == "" {
			_ = m.sender.EditMessage(ctx, peer, statID, "<i>Reply to a message with text.</i>", nil)
			return
		}
		newText, newEnt = src.Message, src.Entities
	}

	ok, fail := 0, 0
	var keep []bcSent
	for _, s := range list {
		var err error
		if del {
			err = bcRetry(ctx, func() error {
				_, e := m.rawAPI.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{ID: []int{s.msgID}, Revoke: true})
				return e
			})
		} else if s.forwarded {
			err = fmt.Errorf("forwarded")
		} else {
			err = bcRetry(ctx, func() error {
				_, e := m.rawAPI.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{Peer: s.peer, ID: s.msgID, Message: newText, Entities: newEnt})
				return e
			})
		}
		if err == nil || strings.Contains(err.Error(), "MESSAGE_NOT_MODIFIED") {
			ok++
		} else {
			fail++
		}
		if !del {
			keep = append(keep, s)
		}
		time.Sleep(60 * time.Millisecond)
	}
	bcMu.Lock()
	if del {
		delete(bcCache, id)
	} else {
		bcCache[id] = keep
	}
	bcMu.Unlock()

	title := "Broadcast Edited Stats"
	if del {
		title = "Broadcast Deleted Stats"
	}
	res := fmt.Sprintf("⌬ <b><i>%s :</i></b>\n"+
		"<blockquote><b>╭</b> <b>Total Users:</b> <code>%d</code>\n"+
		"┠ <b>Success:</b> <code>%d</code>\n"+
		"<b>╰</b> <b>Unsuccess Attempt:</b> <code>%d</code></blockquote>\n\n"+
		"<b>Broadcast ID:</b> <code>%s</code>", title, len(list), ok, fail, id)
	_ = m.sender.EditMessage(ctx, peer, statID, res, telegram.CloseMarkup(msg.UserID))
}
