package modules

import (
	"context"
	"fmt"
	"log"
	"regexp"
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
)

var timePattern = regexp.MustCompile(`(?i)(\d+)\s*([a-zA-Z]+)`)

type AuthorizeModule struct {
	sender  *telegram.MessageSender
	userMap *types.SafeUserMap
}

func NewAuthorizeModule(sender *telegram.MessageSender, userMap *types.SafeUserMap) *AuthorizeModule {
	return &AuthorizeModule{
		sender:  sender,
		userMap: userMap,
	}
}

func (m *AuthorizeModule) HandleAuthorize(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can authorize!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		targetID = msg.ChatID
	}

	u := m.userMap.GetOrCreate(targetID)
	wasAuthorized := u.IsAuth
	u.IsAuth = true
	if db := ext.GetDB(); db == nil {
		u.IsAuth = wasAuthorized
		log.Printf("[Authorize] Cannot persist authorization for %d: database is not initialized", targetID)
		_, _ = m.sender.SendMessage(ctx, peer, "× <b>Authorization was not saved.</b> Database is unavailable; please try again later.", msg.MessageID, nil)
		return
	} else if err := db.SaveUser(u); err != nil {
		u.IsAuth = wasAuthorized
		log.Printf("[Authorize] Failed to persist authorization for %d: %v", targetID, err)
		_, _ = m.sender.SendMessage(ctx, peer, "× <b>Authorization was not saved.</b> Database error; please try again later.", msg.MessageID, nil)
		return
	}
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>Authorized:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleUnauthorize(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can unauthorize!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		targetID = msg.ChatID
	}

	u := m.userMap.GetOrCreate(targetID)
	wasAuthorized := u.IsAuth
	u.IsAuth = false
	if db := ext.GetDB(); db == nil {
		u.IsAuth = wasAuthorized
		log.Printf("[Authorize] Cannot persist unauthorization for %d: database is not initialized", targetID)
		_, _ = m.sender.SendMessage(ctx, peer, "× <b>Unauthorization was not saved.</b> Database is unavailable; please try again later.", msg.MessageID, nil)
		return
	} else if err := db.SaveUser(u); err != nil {
		u.IsAuth = wasAuthorized
		log.Printf("[Authorize] Failed to persist unauthorization for %d: %v", targetID, err)
		_, _ = m.sender.SendMessage(ctx, peer, "× <b>Unauthorization was not saved.</b> Database error; please try again later.", msg.MessageID, nil)
		return
	}
	// a group authorized by an older version is saved by its raw id: unauthorize that one too
	if targetID < 0 {
		raw := pool.RawChannelID(targetID)
		for _, id := range []int64{raw, -raw, -1000000000000 - raw} {
			if id == targetID {
				continue
			}
			if old := m.userMap.Get(id); old != nil && old.IsAuth {
				old.IsAuth = false
				_ = ext.GetDB().SaveUser(old)
			}
		}
	}
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Unauthorized:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleAddSudo(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Bot Owner can add Sudo!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "Give User ID or reply to a user message!", msg.MessageID, nil)
		return
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsSudo = true
	_ = ext.DB.SaveUser(u)
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Promoted as Sudo:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleRmSudo(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Bot Owner can remove Sudo!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "Give User ID or reply to a user message!", msg.MessageID, nil)
		return
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsSudo = false
	_ = ext.DB.SaveUser(u)
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Demoted from Sudo:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleAddPremium(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can add Premium!", msg.MessageID, nil)
		return
	}

	targetID, durationSec := parsePremiumArgs(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "<b>Usage:</b> <code>"+telegram.Cmd("addpremium")+" [user_id] [duration]</code> (e.g. <code>"+telegram.Cmd("addpremium")+" 12345 30d</code>)", msg.MessageID, nil)
		return
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsPremium = true
	if durationSec > 0 {
		u.ExpiryTime = time.Now().Unix() + durationSec
	} else {
		u.ExpiryTime = 0 // Lifetime
	}
	_ = ext.DB.SaveUser(u)

	expiryStr := "Lifetime"
	if u.ExpiryTime > 0 {
		expiryStr = ext.GetReadableTime(durationSec)
	}
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>User Promoted to Premium!</b>\n• <b>User ID:</b> <code>%d</code>\n• <b>Duration:</b> <code>%s</code>\n• <b>Benefits:</b> more parallel tasks, the bot in PM, the Dump channel (<code>%s</code>)", targetID, expiryStr, telegram.Cmd("dump")), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleRmPremium(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can remove Premium!", msg.MessageID, nil)
		return
	}

	targetID := extractTargetID(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "Give User ID or reply to a user message!", msg.MessageID, nil)
		return
	}

	u := m.userMap.GetOrCreate(targetID)
	u.IsPremium = false
	u.ExpiryTime = 0
	_ = ext.DB.SaveUser(u)
	// he leaves the Dump channel he joined as premium (owner and sudo stay)
	if ch := config.Get().LeechLogID; ch != 0 && !config.Get().DumpForAll && !telegram.IsOwner(targetID) && !telegram.IsSudo(targetID, m.userMap) {
		_ = telegram.RemoveFromChat(ctx, ch, targetID)
	}
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Premium Removed for:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

// HandleCheckPremium: /premium, his premium status (always works in PM, like /us). Owner / sudo
// can reply to someone to see his.
func (m *AuthorizeModule) HandleCheckPremium(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	targetID := msg.UserID
	if (telegram.IsOwner(msg.UserID) || telegram.IsSudo(msg.UserID, m.userMap)) && msg.IsReply && msg.ReplyUserID != 0 {
		targetID = msg.ReplyUserID
	}
	cfg := config.Get()
	status, remaining := "<i>Not Premium</i>", "-"
	if telegram.IsPremium(targetID, m.userMap) {
		status, remaining = "<i>Active</i> ✓", "Lifetime"
		if u := m.userMap.Get(targetID); u != nil && u.ExpiryTime > 0 {
			remaining = ext.GetReadableTime(u.ExpiryTime - time.Now().Unix())
		}
		if telegram.IsOwner(targetID) || telegram.IsSudo(targetID, m.userMap) {
			status = "<i>Owner / Sudo</i> ✓"
		}
	}
	tasks := "∞"
	if cfg.PremiumMaxTasks > 0 {
		tasks = strconv.Itoa(cfg.PremiumMaxTasks)
	}
	text := fmt.Sprintf("㊂ <b><u>Premium Status :</u></b>\n\n"+
		"<blockquote><b>╭</b> <b>User :</b> <code>%d</code>\n"+
		"┠ <b>Status :</b> %s\n"+
		"<b>╰</b> <b>Remaining :</b> <code>%s</code></blockquote>\n\n"+
		"<blockquote><b>╭</b> <b>Premium gives :</b>\n"+
		"┠ %s tasks at once (instead of %d)\n"+
		"┠ The bot in private chat\n"+
		"<b>╰</b> The Dump channel: <code>%s</code></blockquote>",
		targetID, status, remaining, tasks, cfg.UserMaxTasks, telegram.Cmd("dump"))
	if !telegram.IsPremium(targetID, m.userMap) {
		text += "\n\n<i>Ask the bot owner for premium.</i>"
	}
	_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, telegram.CloseMarkup(msg.UserID))
}

// dumpLinks: the last /dump link of each user (a new /dump cancels the old one).
var (
	dumpLinksMu sync.Mutex
	dumpLinks   = map[int64]string{}
)

// HandleDump: /dump in PM, a join link of the Dump channel (LEECH_LOG_ID) for premium users
// (everyone with DUMP_FOR_ALL): for him only, once, 5 minutes; none while he is in the channel.
func (m *AuthorizeModule) HandleDump(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	cfg := config.Get()
	reply := func(text string) {
		_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, telegram.CloseMarkup(msg.UserID))
	}
	if msg.ChatID != msg.UserID {
		reply("<i>Send</i> <code>" + telegram.Cmd("dump") + "</code> <i>in my PM to get the Dump channel link.</i>")
		return
	}
	ch := cfg.LeechLogID
	if ch == 0 {
		reply("<i>This bot has no Dump channel.</i>")
		return
	}
	if !cfg.DumpForAll && !telegram.IsPremium(msg.UserID, m.userMap) {
		reply("<i>The Dump channel is for premium users only.</i> " + telegram.Cmd("premium"))
		return
	}
	in, err := telegram.IsChatMember(ctx, ch, msg.UserID)
	if err != nil {
		reply("<i>Can't check the Dump channel: " + telegram.EscapeHTML(err.Error()) + "</i>\n<i>(the bot must be an admin there, with Invite Users)</i>")
		return
	}
	if in {
		reply("㊂ <b><u>Dump Channel :</u></b>\n\n<blockquote>✓ You are already in the Dump channel.</blockquote>")
		return
	}
	dumpLinksMu.Lock()
	old := dumpLinks[msg.UserID]
	dumpLinksMu.Unlock()
	if old != "" { // only one link per user: the old one stops working
		_ = telegram.RevokeInviteLink(ctx, ch, old)
	}
	link, err := telegram.CreateInviteLink(ctx, ch, fmt.Sprintf("dump %d", msg.UserID), 1, 5*time.Minute)
	if err != nil {
		reply("<i>Can't make a link: " + telegram.EscapeHTML(err.Error()) + "</i>\n<i>(the bot must be an admin there, with Invite Users)</i>")
		return
	}
	dumpLinksMu.Lock()
	dumpLinks[msg.UserID] = link
	dumpLinksMu.Unlock()
	btn := telegram.NewButtonMaker()
	btn.UButtonStyled("📥 Join Dump Channel", link, "primary")
	btn.Row()
	text := "㊂ <b><u>Dump Channel :</u></b>\n\n<blockquote><b>╭</b> For you only, works once\n" +
		"┠ Valid for 5 minutes\n<b>╰</b> A new " + telegram.Cmd("dump") + " cancels this link</blockquote>"
	if id, err := m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 1); err == nil && id > 0 {
		m.sender.AutoDeleteMessage(peer, id, 300) // the link is dead after 5 minutes anyway
	}
}

func extractTargetID(msg *types.MessageContext) int64 {
	tokens := strings.Fields(msg.Text)
	if len(tokens) > 1 {
		if id, err := strconv.ParseInt(tokens[1], 10, 64); err == nil {
			return id
		}
	}
	if msg.IsReply {
		return msg.ReplyUserID
	}
	return 0
}

func parsePremiumArgs(msg *types.MessageContext) (int64, int64) {
	tokens := strings.Fields(msg.Text)
	var targetID int64
	var totalSeconds int64

	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		if id, err := strconv.ParseInt(t, 10, 64); err == nil && targetID == 0 {
			targetID = id
			continue
		}

		if m := timePattern.FindStringSubmatch(t); len(m) > 2 {
			val, _ := strconv.ParseInt(m[1], 10, 64)
			unit := strings.ToLower(m[2])
			switch unit {
			case "s", "sec", "second":
				totalSeconds += val
			case "m", "min", "minute":
				totalSeconds += val * 60
			case "h", "hr", "hour":
				totalSeconds += val * 3600
			case "d", "day", "days":
				totalSeconds += val * 86400
			case "w", "week":
				totalSeconds += val * 7 * 86400
			case "mo", "month":
				totalSeconds += val * 30 * 86400
			case "y", "yr", "year":
				totalSeconds += val * 365 * 86400
			}
		}
	}

	if targetID == 0 && msg.IsReply {
		targetID = msg.ReplyUserID
	}

	return targetID, totalSeconds
}

func (m *AuthorizeModule) HandleBlacklist(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can blacklist!", msg.MessageID, nil)
		return
	}
	targetID := extractTargetID(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "Give User ID or reply to a user message!", msg.MessageID, nil)
		return
	}
	if telegram.IsOwner(targetID) {
		_, _ = m.sender.SendMessage(ctx, peer, "× The owner can not be blacklisted.", msg.MessageID, nil)
		return
	}
	u := m.userMap.GetOrCreate(targetID)
	u.IsBlacklist = true
	if db := ext.GetDB(); db != nil {
		_ = db.SaveUser(u)
	}
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Blacklisted:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleRmBlacklist(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can remove from the blacklist!", msg.MessageID, nil)
		return
	}
	targetID := extractTargetID(msg)
	if targetID == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "Give User ID or reply to a user message!", msg.MessageID, nil)
		return
	}
	u := m.userMap.GetOrCreate(targetID)
	u.IsBlacklist = false
	if db := ext.GetDB(); db != nil {
		_ = db.SaveUser(u)
	}
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>Removed from the blacklist:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

// HandleUsers shows /users (overview), /authlist, /sudolist and /blacklisted.
func (m *AuthorizeModule) HandleUsers(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can view this!", msg.MessageID, nil)
		return
	}
	all := m.userMap.GetAllUsers()
	var auth, sudo, prem, black []string
	for _, u := range all {
		id := fmt.Sprintf("<code>%d</code>", u.UserID)
		if u.IsAuth {
			auth = append(auth, id)
		}
		if u.IsSudo {
			sudo = append(sudo, id)
		}
		if u.IsPremium {
			prem = append(prem, id)
		}
		if u.IsBlacklist {
			black = append(black, id)
		}
	}
	list := func(title string, ids []string) string {
		if len(ids) == 0 {
			return fmt.Sprintf("<blockquote>◈ <b>%s :</b> 0</blockquote>", title)
		}
		shown := ids
		more := 0
		if len(shown) > 30 {
			more = len(ids) - 30
			shown = shown[:30]
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("<blockquote><b>╭</b> <b>%s :</b> %d", title, len(ids)))
		for i, id := range shown {
			mark := "┠"
			if i == len(shown)-1 && more == 0 {
				mark = "<b>╰</b>"
			}
			sb.WriteString(fmt.Sprintf("\n%s %s", mark, id))
		}
		if more > 0 {
			sb.WriteString(fmt.Sprintf("\n<b>╰</b> <i>...and %d more</i>", more))
		}
		sb.WriteString("</blockquote>")
		return sb.String()
	}
	cmd := strings.ToLower(strings.TrimPrefix(strings.Fields(msg.Text + " x")[0], "/"))
	if i := strings.Index(cmd, "@"); i >= 0 {
		cmd = cmd[:i]
	}
	var text string
	switch {
	case strings.HasPrefix(cmd, "authlist"):
		text = "㊂ <b><u>Authorized :</u></b>\n\n" + list("Authorized", auth)
	case strings.HasPrefix(cmd, "sudolist"):
		text = "㊂ <b><u>Sudo :</u></b>\n\n" + list("Sudo", sudo)
	case strings.HasPrefix(cmd, "blacklisted"):
		text = "㊂ <b><u>Blacklisted :</u></b>\n\n" + list("Blacklisted", black)
	default:
		text = fmt.Sprintf("㊂ <b><u>Users :</u></b>\n\n"+
			"<blockquote><b>╭</b> <b>Saved users / chats :</b> %d\n"+
			"┠ <b>Authorized :</b> %d\n"+
			"┠ <b>Sudo :</b> %d\n"+
			"┠ <b>Premium :</b> %d\n"+
			"<b>╰</b> <b>Blacklisted :</b> %d</blockquote>\n\n"+
			"➲ <i>Lists:</i> /authlist /sudolist /blacklisted",
			len(all), len(auth), len(sudo), len(prem), len(black))
	}
	_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, telegram.CloseMarkup(msg.UserID))
}
