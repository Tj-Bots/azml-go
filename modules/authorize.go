package modules

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"azml-go/helper/ext"
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
		_, _ = m.sender.SendMessage(ctx, peer, "<b>Usage:</b> <code>/addpremium [user_id] [duration]</code> (e.g. <code>/addpremium 12345 30d</code>)", msg.MessageID, nil)
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
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>User Promoted to Premium!</b>\n• <b>User ID:</b> <code>%d</code>\n• <b>Duration:</b> <code>%s</code>\n• <b>Benefits:</b> 4GB upload, No token verification", targetID, expiryStr), msg.MessageID, nil)
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
	_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Premium Removed for:</b> <code>%d</code>", targetID), msg.MessageID, nil)
}

func (m *AuthorizeModule) HandleCheckPremium(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	targetID := msg.UserID
	if (telegram.IsOwner(msg.UserID) || telegram.IsSudo(msg.UserID, m.userMap)) && msg.IsReply {
		targetID = msg.ReplyUserID
	}

	if telegram.IsPremium(targetID, m.userMap) {
		u := m.userMap.Get(targetID)
		remStr := "Lifetime"
		if u != nil && u.ExpiryTime > 0 {
			remSec := u.ExpiryTime - time.Now().Unix()
			remStr = ext.GetReadableTime(remSec)
		}
		_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Premium User Status:</b>\n• <b>User ID:</b> <code>%d</code>\n✓ <b>Status:</b> Active\n• <b>Remaining:</b> <code>%s</code>", targetID, remStr), msg.MessageID, nil)
	} else {
		_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Premium User Status:</b>\n• <b>User ID:</b> <code>%d</code>\n× <b>Status:</b> Not Premium", targetID), msg.MessageID, nil)
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
			return fmt.Sprintf("<blockquote>╭ <b>%s:</b> 0</blockquote>\n", title)
		}
		shown := ids
		more := ""
		if len(shown) > 30 {
			more = fmt.Sprintf("\n╰ <i>...and %d more</i>", len(ids)-30)
			shown = shown[:30]
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("<blockquote>╭ <b>%s:</b> %d\n", title, len(ids)))
		for i, id := range shown {
			mark := "┠"
			if i == len(shown)-1 && more == "" {
				mark = "╰"
			}
			sb.WriteString(fmt.Sprintf("%s %s\n", mark, id))
		}
		out := strings.TrimRight(sb.String(), "\n") + more + "</blockquote>\n"
		return out
	}
	cmd := strings.ToLower(strings.TrimPrefix(strings.Fields(msg.Text + " x")[0], "/"))
	if i := strings.Index(cmd, "@"); i >= 0 {
		cmd = cmd[:i]
	}
	var text string
	switch {
	case strings.HasPrefix(cmd, "authlist"):
		text = "⌬ <b><u>Authorized</u></b>\n\n" + list("Authorized", auth)
	case strings.HasPrefix(cmd, "sudolist"):
		text = "⌬ <b><u>Sudo</u></b>\n\n" + list("Sudo", sudo)
	case strings.HasPrefix(cmd, "blacklisted"):
		text = "⌬ <b><u>Blacklisted</u></b>\n\n" + list("Blacklisted", black)
	default:
		text = fmt.Sprintf("⌬ <b><u>Users</u></b>\n\n<blockquote>╭ <b>Saved users / chats:</b> %d</blockquote>\n", len(all)) +
			list("Authorized", auth) + list("Sudo", sudo) + list("Premium", prem) + list("Blacklisted", black)
	}
	_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, nil)
}
