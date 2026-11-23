package modules

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/helper/uploader"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

type BotSettingsModule struct {
	sender              *telegram.MessageSender
	userMap             *types.SafeUserMap
	userSessionReloader func(ctx context.Context, sessionStr string) (string, int64, bool, error)
	uploader            *uploader.TelegramUploader
	leechMod            *LeechModule
}

func NewBotSettingsModule(sender *telegram.MessageSender, userMap *types.SafeUserMap) *BotSettingsModule {
	return &BotSettingsModule{
		sender:  sender,
		userMap: userMap,
	}
}

func (m *BotSettingsModule) SetUserSessionReloader(fn func(ctx context.Context, sessionStr string) (string, int64, bool, error)) {
	m.userSessionReloader = fn
}

func (m *BotSettingsModule) SetUploader(u *uploader.TelegramUploader) {
	m.uploader = u
}

func (m *BotSettingsModule) SetLeechModule(l *LeechModule) {
	m.leechMod = l
}

var configVarKeys = []string{
	"MIRROR_LOG_ID",
	"DEFAULT_UPLOAD",
	"GDRIVE_ID",
	"INDEX_URL",
	"IS_TEAM_DRIVE",
	"STOP_DUPLICATE",
	"DISABLE_DRIVE_LINK",
	"RCLONE_PATH",
	"RCLONE_FLAGS",
	"LINKS_LOG_ID",
	"GDRIVE_WORKER",
	"MEDIA_STORE",
	"INCOMPLETE_TASK_NOTIFIER",
	"DISABLE_VT",
	"VT_CUSTOM_CMDS",
	"REAL_DEBRID_API",
	"DEBRID_LINK_API",
	"TORBOX_API",
	"TORBOX_MODE",
	"TORBOX_ACCESS",
	"TORBOX_USER_TASKS",
	"SAFE_MODE",
	"AUTO_DELETE_MESSAGE_DURATION",
	"PM_ACCESS",
	"DUMP_FOR_ALL",
	"SET_COMMANDS",
	"WEB_PINCODE",
	"YT_DLP_OPTIONS",
	"PLAYLIST_LIMIT",
	"LEECH_FILENAME_REMNAME",
	"EXTENSION_FILTER",
	"EXCEP_CHATS",
	"DELETE_LINKS",
	"RICH_MESSAGES",
	"CLEAN_LOG_MSG",
	"USER_TIME_INTERVAL",
	"SAVE_MSG",
	"SOURCE_LINK",
	"FSUB_IDS",
	"DISABLE_TORRENTS",
	"TORRENT_TIMEOUT",
	"USER_SESSION_STRING",
	"LEECH_LOG_ID",
	"STATUS_UPDATE_INTERVAL",
	"STATUS_LIMIT",
	"LEECH_SPLIT_SIZE",
	"AS_DOCUMENT",
	"AUTO_THUMBNAIL",
	"EQUAL_SPLITS",
	"MEDIA_GROUP",
	"LEECH_FILENAME_PREFIX",
	"LEECH_FILENAME_SUFFIX",
	"LEECH_FILENAME_CAPTION",
	"BASE_URL",
	"BOT_MAX_TASKS",
	"USER_MAX_TASKS",
	"PREMIUM_MAX_TASKS",
	"LEECH_LIMIT",
	"TORRENT_LIMIT",
	"DIRECT_LIMIT",
	"YTDLP_LIMIT",
	"MEGA_LIMIT",
	"DAILY_LEECH_LIMIT",
	"DAILY_TASK_LIMIT",
	"SHORTENER",
	"SHORTENER_API",
	"SHORTENER_ENABLED",
	"TOKEN_TIMEOUT",
	"UPSTREAM_REPO",
	"UPSTREAM_BRANCH",
	"GOFILE_TOKEN",
	"PIXELDRAIN_KEY",
	"BUZZHEAVIER_TOKEN",
	"DEVUPLOADS_KEY",
	"STREAMTAPE_API",
	"VIKINGFILE_USER",
	"DOWNLOAD_DIR",
	"CMD_SUFFIX",
	"AUTHORIZED_CHATS",
	"BLACKLIST_USERS",
	"BOT_PM",
	"CAP_FONT",
	"LEECH_FILENAME_REMNAME_AUTO",
	"LEECH_FILENAME_REMNAME_REGEX",
	"ARIA2_PORT",
	"BASE_URL_PORT",
	"MEGA_EMAIL",
	"MEGA_PASSWORD",
	"TOKEN_IN_PM",
	"SCREENSHOTS_MODE",
	"SHOW_MEDIAINFO",
	"METADATA",
	"AUTHOR_NAME",
	"AUTHOR_URL",
	"SHOW_EXTRA_CMDS",
	"TIMEZONE",
	"DISABLE_FF_MODE",
	"FFMPEG_ENCODING",
	"TMDB_ACCESS_TOKEN",
	"DDL_API",
	"QUEUE_ALL",
	"QUEUE_DOWNLOAD",
	"QUEUE_UPLOAD",
	"STORAGE_THRESHOLD",
}

var defaultVarValues = map[string]string{
	"OWNER_USERNAME":               "",
	"USER_SESSION_STRING":          "",
	"LEECH_LOG_ID":                 "0",
	"STATUS_UPDATE_INTERVAL":       "10",
	"STATUS_LIMIT":                 "4",
	"LEECH_SPLIT_SIZE":             "0",
	"AS_DOCUMENT":                  "false",
	"AUTO_THUMBNAIL":               "false",
	"EQUAL_SPLITS":                 "false",
	"MEDIA_GROUP":                  "false",
	"LEECH_FILENAME_PREFIX":        "",
	"LEECH_FILENAME_SUFFIX":        "",
	"LEECH_FILENAME_CAPTION":       "",
	"TORRENT_TIMEOUT":              "0",
	"BASE_URL":                     "",
	"BOT_MAX_TASKS":                "15",
	"QUEUE_ALL":                    "10",
	"QUEUE_DOWNLOAD":               "5",
	"QUEUE_UPLOAD":                 "5",
	"USER_MAX_TASKS":               "3",
	"PREMIUM_MAX_TASKS":            "6",
	"TORBOX_MODE":                  "all",
	"TORBOX_ACCESS":                "sudo",
	"TORBOX_USER_TASKS":            "1",
	"LEECH_LIMIT":                  "0",
	"TORRENT_LIMIT":                "0",
	"DIRECT_LIMIT":                 "0",
	"YTDLP_LIMIT":                  "0",
	"MEGA_LIMIT":                   "0",
	"DAILY_LEECH_LIMIT":            "0",
	"DAILY_TASK_LIMIT":             "0",
	"AUTO_DELETE_MESSAGE_DURATION": "60",
	"SHORTENER":                    "",
	"SHORTENER_API":                "",
	"SHORTENER_ENABLED":            "true",
	"TOKEN_TIMEOUT":                "0",
	"UPSTREAM_REPO":                "https://github.com/Tj-Bots/azml-go",
	"UPSTREAM_BRANCH":              "new",
	"GOFILE_TOKEN":                 "",
	"PIXELDRAIN_KEY":               "",
	"BUZZHEAVIER_TOKEN":            "",
	"DEVUPLOADS_KEY":               "",
	"STREAMTAPE_API":               "",
	"VIKINGFILE_USER":              "",
}

// configValue: the live value, or the environment for vars that apply after a restart.
func configValue(cfgMap map[string]string, key string) string {
	if v, ok := cfgMap[key]; ok {
		return v
	}
	if v, ok := config.StartupValues()[key]; ok {
		return v
	}
	return os.Getenv(key)
}

// restartOnlyVar: the few variables used only while the bot starts (connections, commands, ports).
// Every other one applies as soon as it is changed.
func restartOnlyVar(key string) bool {
	switch key {
	case "BOT_TOKEN", "BOT_TOKENS", "OWNER_ID", "TELEGRAM_API", "TELEGRAM_HASH", "DATABASE_URL", "DATABASE_NAME",
		"CMD_SUFFIX", "DOWNLOAD_DIR", "ARIA2_PORT", "BASE_URL_PORT", "SET_COMMANDS", "AUTHORIZED_CHATS", "SUDO_USERS",
		"BLACKLIST_USERS", "UPSTREAM_REPO", "UPSTREAM_BRANCH":
		return true
	}
	return false
}

func isBoolVar(key string) bool {
	// every var whose description ends with "Bool" is a True / False choice
	if d := strings.TrimRight(strings.TrimSpace(varDescriptions[key]), "."); strings.HasSuffix(d, "Bool") {
		return true
	}
	switch key {
	case "MEDIA_STORE", "INCOMPLETE_TASK_NOTIFIER", "DISABLE_VT", "SAFE_MODE", "PM_ACCESS", "SET_COMMANDS", "FFMPEG_ENCODING":
		return true
	case "WEB_PINCODE", "DELETE_LINKS", "RICH_MESSAGES", "CLEAN_LOG_MSG", "DISABLE_TORRENTS", "AS_DOCUMENT", "AUTO_THUMBNAIL", "EQUAL_SPLITS", "MEDIA_GROUP", "SHORTENER_ENABLED",
		"BOT_PM", "TOKEN_IN_PM", "SCREENSHOTS_MODE", "SHOW_MEDIAINFO", "SAVE_MSG", "SOURCE_LINK",
		"SHOW_EXTRA_CMDS", "DISABLE_FF_MODE", "UPDATE_ON_STARTUP":
		return true
	default:
		return false
	}
}

// isSecretVar: values that should not be shown in full in the chat.
func isSecretVar(key string) bool {
	if strings.Contains(key, "SESSION_STRING") || strings.Contains(key, "PASSWORD") ||
		strings.HasSuffix(key, "_HASH") || key == "DATABASE_URL" || key == "LOGIN_PASS" || strings.HasSuffix(key, "_TOKENS") {
		return true
	}
	for _, w := range []string{"_TOKEN", "_KEY", "_API"} {
		if strings.HasSuffix(key, w) {
			return true
		}
	}
	return false
}

func maskAPI(api string) string {
	if api == "" {
		return "Not Set"
	}
	if len(api) > 8 {
		return api[:4] + "***" + api[len(api)-4:]
	}
	return "***"
}

func parseDurationSec(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	if strings.HasSuffix(s, "d") {
		d, err := strconv.ParseInt(strings.TrimSuffix(s, "d"), 10, 64)
		if err == nil {
			return d * 86400
		}
	}
	if strings.HasSuffix(s, "h") {
		h, err := strconv.ParseInt(strings.TrimSuffix(s, "h"), 10, 64)
		if err == nil {
			return h * 3600
		}
	}
	if strings.HasSuffix(s, "m") {
		m, err := strconv.ParseInt(strings.TrimSuffix(s, "m"), 10, 64)
		if err == nil {
			return m * 60
		}
	}
	if strings.HasSuffix(s, "s") {
		s = strings.TrimSuffix(s, "s")
	}
	sec, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		return sec
	}
	return 0
}

func (m *BotSettingsModule) HandleBotSettings(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if !telegram.IsOwner(msg.UserID) && !telegram.IsSudo(msg.UserID, m.userMap) {
		_, _ = m.sender.SendMessage(ctx, peer, "Only Owner/Sudo can access Bot Settings!", msg.MessageID, nil)
		return
	}

	parts := strings.Fields(msg.Text)
	if len(parts) >= 3 {
		key := strings.ToUpper(parts[1])
		val := strings.Join(parts[2:], " ")
		if key == "USER_SESSION_STRING" && m.userSessionReloader != nil {
			uname, uid, isPrem, err := m.userSessionReloader(ctx, val)
			if err != nil {
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("× <b>Failed to set USER_SESSION_STRING:</b> <code>%v</code>", err), msg.MessageID, nil)
				return
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>USER_SESSION_STRING</b> set successfully as @%s (ID: %d)! Premium: %v", uname, uid, isPrem), msg.MessageID, nil)
			return
		}
		if key == "DUMP_CHAT_ID" || key == "LEECH_LOG_ID" { // one channel (see config.syncLogDump)
			_ = config.UpdateConfigVar(key, val)
			dID := config.Get().DumpChatID
			if m.uploader != nil {
				m.uploader.SetDumpChatID(dID)
			}
			if m.leechMod != nil {
				m.leechMod.SetDumpChatID(dID)
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>LEECH_LOG_ID</b> set to: <code>%d</code>", dID), msg.MessageID, nil)
			return
		}
		if err := config.UpdateConfigVar(key, val); err == nil {
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>%s</b> has been set to: <code>%s</code>", key, val), msg.MessageID, nil)
			return
		}
	}

	text, btn := m.buildMainMenu()
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 1)
}

func (m *BotSettingsModule) buildMainMenu() (string, *telegram.ButtonMaker) {
	text := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n<i>Select a category below to view or edit configuration:</i>", "Bot Settings")
	btn := telegram.NewButtonMaker()
	btn.IButton("Config Variables", "botset var 0")
	btn.Row()
	btn.IButton("User Session (4GB+)", "botset session")
	btn.Row()
	btn.IButton("Private Files", "botset private")
	btn.Row()
	btn.IButton("Aria2c Settings", "botset aria")
	btn.Row()
	btn.IButton("Shortener Settings", "botset shortener")
	btn.Row()
	btn.IButton("Import/Export Settings", "botset ie")
	btn.Row()
	btn.IButtonStyled("❌", "botset close", "danger")
	btn.Row()
	return text, btn
}

func (m *BotSettingsModule) buildSessionMenu() (string, *telegram.ButtonMaker) {
	cfg := config.Get()
	hasSession := cfg.UserSessionString != ""
	isPrem := config.IsPremiumUser
	maxSplit := config.GetMaxSplitSize()

	status := "Not Configured (Standard 2GB Limit)"
	if hasSession {
		if isPrem {
			status = "Active — Premium (4GB Uploads Enabled)"
		} else {
			status = "Active — Non-Premium (2GB Uploads)"
		}
	}

	dumpInfo := "Not Set"
	if cfg.DumpChatID != 0 {
		dumpInfo = fmt.Sprintf("<code>%d</code>", cfg.DumpChatID)
	}

	text := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
		"<blockquote><b>╭</b> <b>Status:</b> %s\n"+
		"┠ <b>Max Split Size:</b> <code>%s</code>\n"+
		"┠ <b>Dump Channel:</b> %s\n"+
		"┠ <b>Session String:</b> <code>%s</code>\n"+
		"<b>╰</b> <b>Upload Flow:</b> <code>User String ➔ Dump Channel ➔ User DM</code></blockquote>\n\n"+
		"<i>Supported session string formats: Pyrogram (v1, v2), Pyrofork, Telethon, gotd</i>\n\n"+
		"<i>Click <b>Set Session String</b> to save/update your user session for 4GB uploads:</i>",
		"User Session Settings",
		status,
		ext.GetReadableFileSize(maxSplit),
		dumpInfo,
		config.MaskSession(cfg.UserSessionString),
	)

	btn := telegram.NewButtonMaker()
	btn.IButton("Set Session String", "botset prompt_session")
	if hasSession {
		btn.IButtonStyled("Reset Session", "botset resetsession", "danger")
	}
	btn.Row()
	btn.IButtonStyled("◀️", "botset back", "primary")
	btn.IButtonStyled("❌", "botset close", "danger")
	btn.Row()

	return text, btn
}

func (m *BotSettingsModule) buildVarsMenu(page int) (string, *telegram.ButtonMaker) {
	// like the Python bot: sorted, 10 per page, 2 per row
	var keys []string
	for _, k := range configVarKeys {
		// with DISABLE_TORRENTS the torrent vars are hidden
		if config.Get().DisableTorrents && (k == "TORRENT_TIMEOUT" || (k == "TORRENT_LIMIT" && strings.TrimSpace(config.Get().TorBoxAPI) == "")) { // with TorBox the torrents (and their limit) are back
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	limit := 10
	totalPages := (len(keys) + limit - 1) / limit
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * limit
	end := start + limit
	if end > len(keys) {
		end = len(keys)
	}

	btn := telegram.NewButtonMaker()
	for i := start; i < end; i++ {
		btn.IButton(keys[i], fmt.Sprintf("botset editvar %s %d", keys[i], page))
		if (i-start)%2 == 1 || i == end-1 {
			btn.Row()
		}
	}
	btn.IButtonStyled("◀️", "botset back", "primary")
	btn.IButtonStyled("❌", "botset close", "danger")
	btn.Row()
	for p := 0; p < totalPages; p++ {
		if p == page {
			btn.IButtonStyled(fmt.Sprintf("» %d «", p+1), fmt.Sprintf("botset var %d", p), "success")
		} else {
			btn.IButton(fmt.Sprintf("%d", p+1), fmt.Sprintf("botset var %d", p))
		}
		if (p+1)%6 == 0 || p == totalPages-1 {
			btn.Row()
		}
	}
	return fmt.Sprintf("<b>Config Variables</b> | <b>Page: %d</b>", page+1), btn
}

func isPrivatePeer(peer tg.InputPeerClass) bool {
	_, ok := peer.(*tg.InputPeerUser)
	return ok
}

// varPageText is the variable page like the Python bot: Variable, Description, Value (spoiler).
// In groups secret values stay masked.
func varPageText(key, value string, private bool, live bool) string {
	desc := varDescriptions[key]
	if desc == "" {
		desc = "No Description Provided"
	}
	val := "None"
	if value != "" {
		if isSecretVar(key) && !private {
			val = maskAPI(value)
		} else {
			val = value
		}
	}
	text := fmt.Sprintf("<b>Variable:</b> <code>%s</code>\n\n<b>Description:</b> %s\n\n<b>Value:</b> <span class=\"tg-spoiler\">%s</span>",
		key, desc, telegram.EscapeHTML(val))
	if !live {
		text += "\n\n<i>Applies after /restart</i>"
	}
	return text
}

func (m *BotSettingsModule) buildVarDetailMenu(key string, page int, editing, private bool) (string, *telegram.ButtonMaker) {
	cfgMap := config.GetConfigMap()
	live := !restartOnlyVar(key)
	text := varPageText(key, configValue(cfgMap, key), private, live)
	btn := telegram.NewButtonMaker()

	if isBoolVar(key) {
		text += "\n\n<i>Choose a valid value for the above Var:</i>"
		// like the Python bot: ✓ and green on the current value only
		on := isTrueValue(configValue(cfgMap, key))
		if on {
			btn.IButtonStyled("✓ True", fmt.Sprintf("botset boolvar %s true %d", key, page), "success")
			btn.IButton("False", fmt.Sprintf("botset boolvar %s false %d", key, page))
		} else {
			btn.IButton("True", fmt.Sprintf("botset boolvar %s true %d", key, page))
			btn.IButtonStyled("✓ False", fmt.Sprintf("botset boolvar %s false %d", key, page), "success")
		}
		btn.Row()
		btn.IButtonStyled("◀️", fmt.Sprintf("botset var %d", page), "primary")
		btn.IButtonStyled("❌", "botset close", "danger")
		btn.Row()
		return text, btn
	}

	if editing {
		text += "\n\n<i>Send a valid value for the above Var.</i> <b>Timeout:</b> 60 sec"
		btn.IButtonStyled("Stop Edit", fmt.Sprintf("botset editvar %s %d", key, page), "danger")
		btn.Row()
		btn.IButtonStyled("◀️", fmt.Sprintf("botset var %d", page), "primary")
		btn.IButtonStyled("❌", "botset close", "danger")
		btn.Row()
		return text, btn
	}

	btn.IButtonStyled("Edit Value", fmt.Sprintf("botset editvar_prompt %s %d", key, page), "success")
	// Reset only when there is something to reset (the value is not the default)
	defVal, ok := config.DefaultValue(key)
	if !ok {
		defVal = defaultVarValues[key]
	}
	if strings.TrimSpace(configValue(cfgMap, key)) != strings.TrimSpace(defVal) {
		btn.IButtonStyled("Reset", fmt.Sprintf("botset resetvar %s %d", key, page), "danger")
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("botset var %d", page), "primary")
	btn.IButtonStyled("❌", "botset close", "danger")
	btn.Row()
	return text, btn
}

func (m *BotSettingsModule) HandleSessionValue(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key, val string, page int) {
	val = strings.TrimSpace(val)

	if key == "USER_SESSION_STRING" {
		if m.userSessionReloader != nil {
			uname, uid, isPrem, err := m.userSessionReloader(ctx, val)
			if err != nil {
				errMsg := fmt.Sprintf("\n\n× <b>Failed to connect User Session:</b> <code>%v</code>\n<i>Please check that the session string is valid and active.</i>", err)
				if page == -2 {
					text, btn := m.buildSessionMenu()
					text += errMsg
					if m.sender != nil {
						_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
					}
				} else {
					text, btn := m.buildVarDetailMenu(key, page, false, isPrivatePeer(peer))
					text += errMsg
					if m.sender != nil {
						_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
					}
				}
				return
			}

			successMsg := fmt.Sprintf("\n\n✓ <b>User Session connected!</b>\n<blockquote><b>╭</b> <b>User:</b> @%s (ID: %d)\n┠ <b>Premium:</b> %v\n┠ <b>Max Split:</b> %s\n<b>╰</b> <b>Upload Flow:</b> <code>User String ➔ Dump Channel ➔ User DM</code></blockquote>",
				uname, uid, isPrem, ext.GetReadableFileSize(config.GetMaxSplitSize()))

			if page == -2 {
				text, btn := m.buildSessionMenu()
				text += successMsg
				if m.sender != nil {
					_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
				}
			} else {
				text, btn := m.buildVarDetailMenu(key, page, false, isPrivatePeer(peer))
				text += successMsg
				if m.sender != nil {
					_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
				}
			}
			return
		}
	}

	if key == "DUMP_CHAT_ID" || key == "LEECH_LOG_ID" { // one channel (see config.syncLogDump)
		_ = config.UpdateConfigVar(key, val)
		dID := config.Get().DumpChatID
		if m.uploader != nil {
			m.uploader.SetDumpChatID(dID)
		}
		if m.leechMod != nil {
			m.leechMod.SetDumpChatID(dID)
		}
		if page == -2 {
			text, btn := m.buildSessionMenu()
			text += fmt.Sprintf("\n\n✓ <b>Dump Channel set to:</b> <code>%d</code>", dID)
			if m.sender != nil {
				_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
			}
			return
		}
	}

	if key == "TOKEN_TIMEOUT" && page == -1 {
		sec := parseDurationSec(val)
		if sec > 0 {
			_ = config.UpdateConfigVar("TOKEN_TIMEOUT", strconv.FormatInt(sec, 10))
			_ = config.UpdateConfigVar("SHORTENER_ENABLED", "true")
		} else {
			_ = config.UpdateConfigVar("TOKEN_TIMEOUT", val)
		}
	} else {
		_ = config.UpdateConfigVar(key, val)
	}

	if page == -1 || key == "SHORTENER" || key == "SHORTENER_API" {
		text, btn := m.buildShortenerMenu()
		text += fmt.Sprintf("\n\n✓ <i>%s updated successfully!</i>", key)
		if m.sender != nil {
			_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
		}
		return
	}

	text, btn := m.buildVarDetailMenu(key, page, false, isPrivatePeer(peer))
	text += "\n\n✓ <i>Value updated successfully!</i>"
	if m.sender != nil {
		_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
	}
}

func (m *BotSettingsModule) HandleSessionCancelled(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key string, page int) {
	if key == ImportSettingsKey {
		text, btn := m.buildIEMenu("× <i>Import cancelled.</i>")
		if m.sender != nil {
			_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
		}
		return
	}
	if page == -2 {
		text, btn := m.buildSessionMenu()
		text += "\n\n× <i>Edit cancelled.</i>"
		if m.sender != nil {
			_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
		}
		return
	}
	if page == -1 || key == "SHORTENER" || key == "SHORTENER_API" {
		text, btn := m.buildShortenerMenu()
		text += "\n\n× <i>Edit cancelled.</i>"
		if m.sender != nil {
			_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
		}
		return
	}
	text, btn := m.buildVarDetailMenu(key, page, false, isPrivatePeer(peer))
	text += "\n\n× <i>Edit cancelled.</i>"
	if m.sender != nil {
		_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
	}
}

func (m *BotSettingsModule) buildAriaMenu() (string, *telegram.ButtonMaker) {
	text := "㊂ <b><u>Aria2c Settings :</u></b>\n\n" +
		"<blockquote><b>╭</b> <b>max-connection-per-server:</b> <code>16</code> (Max)\n" +
		"┠ <b>split:</b> <code>16</code> (Max)\n" +
		"┠ <b>min-split-size:</b> <code>1M</code>\n" +
		"┠ <b>bt-max-peers:</b> <code>128</code>\n" +
		"┠ <b>disk-cache:</b> <code>64M</code>\n" +
		"<b>╰</b> <b>allow-overwrite:</b> <code>true</code></blockquote>\n\n" +
		"<i>Optimized for peak network throughput on free tier VPS servers.</i>"

	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("◀️", "botset back", "primary")
	btn.IButtonStyled("❌", "botset close", "danger")
	btn.Row()
	return text, btn
}

func (m *BotSettingsModule) buildPrivateMenu() (string, *telegram.ButtonMaker) {
	text := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
		"<i>Send any of these private files as a document directly to the bot:</i>\n\n"+
		"<blockquote><b>╭</b> <code>token.pickle</code> — Google Drive OAuth token (automatically loaded)\n"+
		"┠ <code>config.env</code> — Bot environment variables (auto reloaded)\n"+
		"┠ <code>cookies.txt</code> — Netscape cookies for yt-dlp & aria2c\n"+
		"┠ <code>shorteners.txt</code> — Multi-shortener routing file (auto reloaded)\n"+
		"<b>╰</b> <code>accounts.zip</code> — Service accounts archive</blockquote>\n\n"+
		"<b>Automatic Processing:</b>\n"+
		"When you share <code>token.pickle</code> or <code>shorteners.txt</code> to the bot, they are automatically saved and reloaded immediately.",
		"Private Files")

	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("◀️", "botset back", "primary")
	btn.IButtonStyled("❌", "botset close", "danger")
	btn.Row()
	return text, btn
}

func (m *BotSettingsModule) HandleCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	if !telegram.IsOwner(fromUID) && !telegram.IsSudo(fromUID, m.userMap) {
		_ = m.sender.AnswerCallback(ctx, queryID, "Only Owner/Sudo can access Bot Settings!", true)
		return
	}

	parts := strings.Fields(data)
	if len(parts) < 2 {
		return
	}

	action := parts[1]
	switch action {
	case "close":
		ext.ClearPendingBotSettingSession(fromUID)
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed", false)
		m.sender.DeleteWithCommand(ctx, peer, msgID)

	case "ie":
		ext.ClearPendingBotSettingSession(fromUID)
		text, btn := m.buildIEMenu("")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "export":
		ext.ClearPendingBotSettingSession(fromUID)
		// the file holds API keys and channel IDs: only in PM
		note := "✓ <i>Exported: the file is below.</i>"
		if !isPrivatePeer(peer) {
			note = "× <i>Export works only in the bot PM: open /bs there.</i>"
		} else if err := m.sendExport(ctx, peer, msgID, ext.BotVersion()); err != nil {
			note = "× <i>Export failed: " + telegram.EscapeHTML(err.Error()) + "</i>"
		}
		text, btn := m.buildIEMenu(note)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "prompt_import":
		ext.SetPendingBotSettingSession(fromUID, ImportSettingsKey, 0, msgID, -3, 60*time.Second)
		text, _ := m.buildIEMenu("")
		text += "\n\n<i>Send the file</i> <code>" + SettingsFileName + "</code> <i>(from Export).</i> <b>Timeout:</b> 60 sec\n<i>Send /cancel to abort.</i>"
		btn := telegram.NewButtonMaker()
		btn.IButtonStyled("Stop Edit", "botset ie", "danger")
		btn.Row()
		btn.IButtonStyled("◀️", "botset back", "primary")
		btn.IButtonStyled("❌", "botset close", "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "back":
		ext.ClearPendingBotSettingSession(fromUID)
		text, btn := m.buildMainMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 1)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "var":
		ext.ClearPendingBotSettingSession(fromUID)
		page := 0
		if len(parts) >= 3 {
			page, _ = strconv.Atoi(parts[2])
		}
		text, btn := m.buildVarsMenu(page)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "editvar":
		ext.ClearPendingBotSettingSession(fromUID)
		if len(parts) >= 3 {
			key := parts[2]
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			text, btn := m.buildVarDetailMenu(key, page, false, isPrivatePeer(peer))
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
			_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		}

	case "editvar_prompt":
		if len(parts) >= 3 {
			key := parts[2]
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			ext.SetPendingBotSettingSession(fromUID, key, 0, msgID, page, 60*time.Second)
			text, btn := m.buildVarDetailMenu(key, page, true, isPrivatePeer(peer))
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
			_ = m.sender.AnswerCallback(ctx, queryID, "Send new value within 60s...", false)
		}

	case "boolvar":
		if len(parts) >= 4 {
			key := parts[2]
			val := parts[3]
			page := 0
			if len(parts) >= 5 {
				page, _ = strconv.Atoi(parts[4])
			}
			_ = config.UpdateConfigVar(key, val)
			text, btn := m.buildVarDetailMenu(key, page, false, isPrivatePeer(peer))
			text += fmt.Sprintf("\n\n✓ <i>%s set to %s!</i>", key, val)
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("%s set to %s", key, val), false)
		}

	case "resetvar":
		if len(parts) >= 3 {
			key := parts[2]
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			// the code default of the variable (what it is when nothing sets it)
			defVal, ok := config.DefaultValue(key)
			if !ok {
				defVal = defaultVarValues[key]
			}
			if key == "USER_SESSION_STRING" && m.userSessionReloader != nil {
				_, _, _, _ = m.userSessionReloader(ctx, "")
			} else {
				_ = config.UpdateConfigVar(key, defVal)
			}
			text, btn := m.buildVarDetailMenu(key, page, false, isPrivatePeer(peer))
			shown := defVal
			if shown == "" {
				shown = "None"
			}
			text += fmt.Sprintf("\n\n• <i>%s reset to default: %s</i>", key, telegram.EscapeHTML(shown))
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("%s reset to default", key), false)
		}

	case "session":
		ext.ClearPendingBotSettingSession(fromUID)
		text, btn := m.buildSessionMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "prompt_session":
		ext.SetPendingBotSettingSession(fromUID, "USER_SESSION_STRING", 0, msgID, -2, 60*time.Second)
		text := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
			"Send your Pyrogram (v1/v2), Pyrofork, Telethon, or gotd session string in chat.\n\n"+
			"<b>Timeout:</b> 60 sec\n<i>Send /cancel to abort.</i>",
			"Set User Session String")
		btn := telegram.NewButtonMaker()
		btn.IButtonStyled("Cancel", "botset session", "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 1)
		_ = m.sender.AnswerCallback(ctx, queryID, "Send session string in chat...", false)

	case "resetsession":
		ext.ClearPendingBotSettingSession(fromUID)
		if m.userSessionReloader != nil {
			_, _, _, _ = m.userSessionReloader(ctx, "")
		} else {
			_ = config.UpdateConfigVar("USER_SESSION_STRING", "")
			config.SetIsPremiumUser(false)
		}
		text, btn := m.buildSessionMenu()
		text += "\n\n• <i>User Session removed. Reverted to standard 2GB limits.</i>"
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "User session reset", false)

	case "prompt_dump":
		ext.SetPendingBotSettingSession(fromUID, "DUMP_CHAT_ID", 0, msgID, -2, 60*time.Second)
		text := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
			"Send the Dump Channel ID (e.g. <code>-100xxxxxxxxxx</code>) where 2GB+ files will be uploaded before forwarding to User DM.\n\n"+
			"<b>Timeout:</b> 60 sec\n<i>Send /cancel to abort.</i>",
			"Set Dump Channel")
		btn := telegram.NewButtonMaker()
		btn.IButtonStyled("Cancel", "botset session", "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 1)
		_ = m.sender.AnswerCallback(ctx, queryID, "Send Dump Channel ID in chat...", false)

	case "aria":
		text, btn := m.buildAriaMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "private":
		text, btn := m.buildPrivateMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "shortener":
		text, btn := m.buildShortenerMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "toggleshrt":
		cfg := config.Get()
		if cfg.ShortenerEnabled && cfg.TokenTimeout > 0 {
			_ = config.UpdateConfigVar("SHORTENER_ENABLED", "false")
			_ = m.sender.AnswerCallback(ctx, queryID, "Shortener Disabled!", false)
		} else {
			_ = config.UpdateConfigVar("SHORTENER_ENABLED", "true")
			if cfg.TokenTimeout <= 0 {
				_ = config.UpdateConfigVar("TOKEN_TIMEOUT", "86400")
			}
			_ = m.sender.AnswerCallback(ctx, queryID, "Shortener Enabled (24 Hours)!", false)
		}
		text, btn := m.buildShortenerMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "setshrt":
		if len(parts) >= 3 {
			sec := parts[2]
			_ = config.UpdateConfigVar("TOKEN_TIMEOUT", sec)
			_ = config.UpdateConfigVar("SHORTENER_ENABLED", "true")
			valInt, _ := strconv.ParseInt(sec, 10, 64)
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Validity set to %s", ext.GetReadableTime(valInt)), false)
		}
		text, btn := m.buildShortenerMenu()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "prompt_shrt_domain":
		cfg := config.Get()
		cur := cfg.Shortener
		if cur == "" {
			cur = "Not Set"
		}
		ext.SetPendingBotSettingSession(fromUID, "SHORTENER", 0, msgID, -1, 60*time.Second)
		promptText := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
			"<blockquote>◈ <b>Current Domain:</b> <code>%s</code></blockquote>\n\n"+
			"<i>Send the shortener domain (e.g. <code>droplink.co</code> or <code>https://api.shareus.io/short?api=%%s&url=%%s</code>).</i>\n"+
			"<b>Timeout:</b> 60 sec\n"+
			"<i>Send /cancel to abort.</i>",
			"Set Shortener Domain", cur)
		btn := telegram.NewButtonMaker()
		btn.IButtonStyled("Cancel", "botset shortener", "danger")
		btn.IButtonStyled("◀️", "botset shortener", "primary")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, promptText, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Send shortener domain within 60s...", false)

	case "prompt_shrt_api":
		cfg := config.Get()
		cur := maskAPI(cfg.ShortenerAPI)
		ext.SetPendingBotSettingSession(fromUID, "SHORTENER_API", 0, msgID, -1, 60*time.Second)
		promptText := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
			"<blockquote>◈ <b>Current API Key:</b> <code>%s</code></blockquote>\n\n"+
			"<i>Send your shortener API key in chat.</i>\n"+
			"<b>Timeout:</b> 60 sec\n"+
			"<i>Send /cancel to abort.</i>",
			"Set Shortener API Key", cur)
		btn := telegram.NewButtonMaker()
		btn.IButtonStyled("Cancel", "botset shortener", "danger")
		btn.IButtonStyled("◀️", "botset shortener", "primary")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, promptText, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Send shortener API key within 60s...", false)

	case "prompt_shrt_val":
		cfg := config.Get()
		cur := ext.GetReadableTime(cfg.TokenTimeout)
		ext.SetPendingBotSettingSession(fromUID, "TOKEN_TIMEOUT", 0, msgID, -1, 60*time.Second)
		promptText := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
			"<blockquote>◈ <b>Current Validity:</b> <code>%s</code></blockquote>\n\n"+
			"<i>Send custom validity duration in seconds (e.g. <code>86400</code>) or with suffix (e.g. <code>24h</code>, <code>12h</code>, <code>2d</code>).</i>\n"+
			"<b>Timeout:</b> 60 sec\n"+
			"<i>Send /cancel to abort.</i>",
			"Custom Token Validity", cur)
		btn := telegram.NewButtonMaker()
		btn.IButtonStyled("Cancel", "botset shortener", "danger")
		btn.IButtonStyled("◀️", "botset shortener", "primary")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, promptText, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Send validity duration within 60s...", false)
	}
}

func (m *BotSettingsModule) buildShortenerMenu() (string, *telegram.ButtonMaker) {
	cfg := config.Get()
	isActive := cfg.ShortenerEnabled && cfg.TokenTimeout > 0
	valStr := ext.GetReadableTime(cfg.TokenTimeout)
	if !isActive {
		valStr = "Disabled (None)"
	}

	stEmoji := "Disabled"
	if isActive {
		stEmoji = "Enabled"
	}

	shDomain := cfg.Shortener
	if shDomain == "" {
		shDomain = "Not Set"
	}
	shAPI := maskAPI(cfg.ShortenerAPI)

	shorteners := ext.GetShorteners()
	shrtsCount := len(shorteners)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
		"<blockquote><b>╭</b> <b>Shortener Access :</b> <code>%s</code>\n"+
		"┠ <b>Token Validity :</b> <code>%s</code>\n"+
		"┠ <b>Default Domain :</b> <code>%s</code>\n"+
		"<b>╰</b> <b>Default API Key :</b> <code>%s</code></blockquote>\n\n",
		"Shortener Settings", stEmoji, valStr, shDomain, shAPI))

	if shrtsCount > 0 {
		sb.WriteString(fmt.Sprintf("➲ <b><u>%s (%d) :</u></b>\n", "Multi Shorteners", shrtsCount))
		for i, sh := range shorteners {
			if i >= 5 {
				sb.WriteString(fmt.Sprintf("• <i>... and %d more in shorteners.txt</i>\n", shrtsCount-5))
				break
			}
			masked := maskAPI(sh.APIKey)
			sb.WriteString(fmt.Sprintf("• <code>%d. %s</code> | <i>Key:</i> <code>%s</code>\n", i+1, sh.Domain, masked))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("➲ <b>Description:</b> <i>When enabled, users must generate a temporary token via shortener before leeching.</i>")

	btn := telegram.NewButtonMaker()
	if isActive {
		btn.IButtonStyled("Disable Shortener", "botset toggleshrt", "danger")
	} else {
		btn.IButtonStyled("✓ "+"Enable Shortener (24h)", "botset toggleshrt", "success")
	}
	btn.Row()

	btn.IButton("24 Hours", "botset setshrt 86400")
	btn.IButton("12 Hours", "botset setshrt 43200")
	btn.IButton("6 Hours", "botset setshrt 21600")
	btn.Row()

	btn.IButton("Set Domain", "botset prompt_shrt_domain")
	btn.IButton("Set API Key", "botset prompt_shrt_api")
	btn.Row()

	btn.IButton("Custom Validity", "botset prompt_shrt_val")
	btn.Row()

	btn.IButtonStyled("◀️", "botset back", "primary")
	btn.IButtonStyled("❌", "botset close", "danger")
	btn.Row()

	return sb.String(), btn
}

func isTrueValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
