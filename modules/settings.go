package modules

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	tdDownloader "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

type SettingsModule struct {
	rawAPI  *tg.Client
	sender  *telegram.MessageSender
	userMap *types.SafeUserMap
}

func NewSettingsModule(rawAPI *tg.Client, sender *telegram.MessageSender, userMap *types.SafeUserMap) *SettingsModule {
	return &SettingsModule{
		rawAPI:  rawAPI,
		sender:  sender,
		userMap: userMap,
	}
}

func (m *SettingsModule) HandleSettings(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	u := m.userMap.GetOrCreate(msg.UserID)
	parts := strings.Fields(msg.Text)

	// Check if this was a thumbnail command (/setthumb, /t, /thumb, or /us thumb)
	isThumbCmd := false
	if len(parts) >= 1 {
		cmd := strings.ToLower(parts[0])
		if strings.Contains(cmd, "setthumb") || strings.Contains(cmd, "thumb") || cmd == "/t" || strings.HasPrefix(cmd, "/t@") {
			isThumbCmd = true
		}
	}
	if len(parts) >= 2 {
		subCmd := strings.ToLower(parts[1])
		if subCmd == "thumb" || subCmd == "setthumb" || subCmd == "t" {
			isThumbCmd = true
		}
	}

	if isThumbCmd {
		if msg.IsReply && msg.ReplyToID != 0 && m.rawAPI != nil {
			res, err := m.rawAPI.MessagesGetMessages(ctx, []tg.InputMessageClass{
				&tg.InputMessageID{ID: msg.ReplyToID},
			})
			var targetMsg *tg.Message
			if err == nil {
				switch mRes := res.(type) {
				case *tg.MessagesMessages:
					if len(mRes.Messages) > 0 {
						targetMsg, _ = mRes.Messages[0].(*tg.Message)
					}
				case *tg.MessagesMessagesSlice:
					if len(mRes.Messages) > 0 {
						targetMsg, _ = mRes.Messages[0].(*tg.Message)
					}
				case *tg.MessagesChannelMessages:
					if len(mRes.Messages) > 0 {
						targetMsg, _ = mRes.Messages[0].(*tg.Message)
					}
				}
			}
			if targetMsg != nil && targetMsg.Media != nil {
				if photoMedia, ok := targetMsg.Media.(*tg.MessageMediaPhoto); ok {
					if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
						_ = os.MkdirAll("Thumbnails", 0755)
						destThumb := fmt.Sprintf("Thumbnails/%d.jpg", msg.UserID)
						thumbSize := "m"
						if len(photo.Sizes) > 0 {
							switch s := photo.Sizes[len(photo.Sizes)-1].(type) {
							case *tg.PhotoSize:
								thumbSize = s.Type
							case *tg.PhotoSizeProgressive:
								thumbSize = s.Type
							}
						}
						dl := tdDownloader.NewDownloader()
						loc := photo.AsInputPhotoFileLocation(thumbSize)
						if loc != nil {
							_, err := dl.Download(m.rawAPI, loc).ToPath(ctx, destThumb)
							if err == nil {
								u.CustomThumb = destThumb
								u.ThumbData = ext.ThumbBytes(destThumb)
								_ = ext.DB.SaveUser(u)
								ext.ClearPendingThumbSession(msg.UserID)
								m.thumbNotice(ctx, peer, msg, "✓ <b>Custom thumbnail saved from the replied photo!</b>")
								return
							}
						}
					}
				} else if docMedia, ok := targetMsg.Media.(*tg.MessageMediaDocument); ok {
					if doc, ok := docMedia.Document.(*tg.Document); ok {
						_ = os.MkdirAll("Thumbnails", 0755)
						destThumb := fmt.Sprintf("Thumbnails/%d.jpg", msg.UserID)
						dl := tdDownloader.NewDownloader()
						loc := doc.AsInputDocumentFileLocation("")
						if loc != nil {
							_, err := dl.Download(m.rawAPI, loc).ToPath(ctx, destThumb)
							if err == nil {
								u.CustomThumb = destThumb
								u.ThumbData = ext.ThumbBytes(destThumb)
								_ = ext.DB.SaveUser(u)
								ext.ClearPendingThumbSession(msg.UserID)
								m.thumbNotice(ctx, peer, msg, "✓ <b>Custom thumbnail saved from the replied file!</b>")
								return
							}
						}
					}
				}
			}
		}

		// /t <image link>: the thumbnail from the link
		if len(parts) >= 2 && (strings.HasPrefix(parts[1], "http://") || strings.HasPrefix(parts[1], "https://")) {
			_ = os.MkdirAll("Thumbnails", 0755)
			dest := fmt.Sprintf("Thumbnails/%d.jpg", msg.UserID)
			tmp := dest + ".dl"
			note := "✓ <b>Custom thumbnail saved from the link!</b>"
			if err := ext.DownloadImage(ctx, parts[1], tmp); err != nil || !isImageFile(tmp) {
				_ = os.Remove(tmp)
				note = "× <i>That link is not an image.</i>"
			} else if err := os.Rename(tmp, dest); err == nil {
				u.CustomThumb = dest
				u.ThumbData = ext.ThumbBytes(dest)
				_ = ext.DB.SaveUser(u)
			}
			ext.ClearPendingThumbSession(msg.UserID)
			m.thumbNotice(ctx, peer, msg, note)
			return
		}

		// wait 60s for a photo; the prompt is edited with the result (see bot.thumbSaved)
		suffix := config.Get().CmdSuffix
		promptText := "㊂ <b><u>Custom Thumbnail :</u></b>\n\n" +
			"<blockquote>Send a <b>photo</b> or an image file within <b>60 seconds</b>.\n" +
			"Also: reply to a photo with <code>/t" + suffix + "</code>, or <code>/t" + suffix + " image-link</code></blockquote>\n" +
			"<i>Send</i> /cancel" + suffix + " <i>to abort.</i>"
		promptID, _ := m.sender.SendMessage(ctx, peer, promptText, msg.MessageID, nil)
		if promptID <= 0 {
			return
		}
		ext.SetThumbCommandSession(msg.UserID, msg.ChatID, promptID, msg.MessageID, 60*time.Second)
		uid, cmdID := msg.UserID, msg.MessageID
		time.AfterFunc(61*time.Second, func() {
			// still waiting (not saved, not cancelled): time out
			if !ext.TakeThumbPrompt(uid, promptID) {
				return
			}
			c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = m.sender.EditMessage(c, peer, promptID, "㊂ <b><u>Custom Thumbnail :</u></b>\n\n× <i>Timed out.</i>", nil)
			time.Sleep(3 * time.Second)
			_ = m.sender.DeleteMessage(c, peer, []int{promptID, cmdID})
		})
		return
	}

	// Direct command arguments e.g. /us lprefix <text> or /us doc
	if len(parts) >= 2 {
		subCmd := strings.ToLower(parts[1])
		switch subCmd {
		case "doc", "asdoc":
			u.AsDoc = !u.AsDoc
			_ = ext.DB.SaveUser(u)
			st := "Disabled"
			if u.AsDoc {
				st = "Enabled"
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Upload As Document</b> is now: <b>%s</b>", st), msg.MessageID, nil)
			return
		case "autorename":
			if len(parts) >= 3 {
				u.RemnameAuto = strings.Join(parts[2:], " ")
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>AutoRename Template</b> set to: <code>%s</code>", u.RemnameAuto), msg.MessageID, nil)
				return
			}
			u.Autorename = !u.Autorename
			_ = ext.DB.SaveUser(u)
			st := "Disabled"
			if u.Autorename {
				st = "Enabled"
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>AutoRename</b> is now: <b>%s</b>", st), msg.MessageID, nil)
			return
		case "autothumb", "auto_thumb":
			u.AutoThumb, u.AutoThumbSet = !u.AutoThumbOn(config.Get().AutoThumbnail), true
			_ = ext.DB.SaveUser(u)
			st := "Disabled"
			if u.AutoThumb {
				st = "Enabled"
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Auto Thumbnail</b> is now: <b>%s</b>", st), msg.MessageID, nil)
			return
		case "method", "rename_method":
			if u.RenameMethod == "auto" {
				u.RenameMethod = "regex"
			} else {
				u.RenameMethod = "auto"
			}
			_ = ext.DB.SaveUser(u)
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("• <b>Rename Method</b> is now: <b>%s</b>", strings.Title(u.RenameMethod)), msg.MessageID, nil)
			return
		case "equalsplits", "esplits":
			u.EqualSplits = !u.EqualSplits
			_ = ext.DB.SaveUser(u)
			st := "Disabled"
			if u.EqualSplits {
				st = "Enabled"
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Equal Splits</b> is now: <b>%s</b>", st), msg.MessageID, nil)
			return
		case "mediagroup", "mgroup":
			u.MediaGroup = !u.MediaGroup
			_ = ext.DB.SaveUser(u)
			st := "Disabled"
			if u.MediaGroup {
				st = "Enabled"
			}
			_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("◈ <b>Media Group</b> is now: <b>%s</b>", st), msg.MessageID, nil)
			return
		case "prefix", "lprefix":
			if len(parts) >= 3 {
				val := strings.Join(parts[2:], " ")
				if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
					u.Prefix = ""
					val = "Cleared"
				} else {
					u.Prefix = val
				}
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>Leech Prefix</b> set to: <code>%s</code>", val), msg.MessageID, nil)
				return
			}
		case "suffix", "lsuffix":
			if len(parts) >= 3 {
				val := strings.Join(parts[2:], " ")
				if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
					u.Suffix = ""
					val = "Cleared"
				} else {
					u.Suffix = val
				}
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>Leech Suffix</b> set to: <code>%s</code>", val), msg.MessageID, nil)
				return
			}
		case "caption", "lcaption":
			if len(parts) >= 3 {
				val := strings.Join(parts[2:], " ")
				if strings.EqualFold(val, "clear") || strings.EqualFold(val, "default") {
					u.Caption = ""
					val = "Default"
				} else {
					u.Caption = val
				}
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>Leech Caption</b> set to: <code>%s</code>", val), msg.MessageID, nil)
				return
			}
		case "dump", "ldump":
			if len(parts) >= 3 {
				id, _ := strconv.ParseInt(parts[2], 10, 64)
				u.DumpChatID = id
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>Leech Dump Channel ID</b> set to: <code>%d</code>", u.DumpChatID), msg.MessageID, nil)
				return
			}
		case "meta", "lmeta", "metadata":
			if len(parts) >= 3 {
				if u.Metadata == nil {
					u.Metadata = make(map[string]string)
				}
				val := strings.Join(parts[2:], " ")
				if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
					delete(u.Metadata, "all")
					val = "Cleared"
				} else {
					u.Metadata["all"] = val
				}
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>Leech Metadata</b> set to: <code>%s</code>", val), msg.MessageID, nil)
				return
			}
		case "split", "splitsize", "split_size":
			if len(parts) >= 3 {
				sizeStr := strings.ToUpper(parts[2])
				maxSplit := config.GetMaxSplitSize()
				var bytes int64
				if strings.HasSuffix(sizeStr, "GB") {
					val, _ := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "GB"), 64)
					bytes = int64(val * 1024 * 1024 * 1024)
				} else if strings.HasSuffix(sizeStr, "MB") {
					val, _ := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "MB"), 64)
					bytes = int64(val * 1024 * 1024)
				} else {
					bytes, _ = strconv.ParseInt(sizeStr, 10, 64)
				}
				if bytes > maxSplit || bytes <= 0 {
					bytes = maxSplit
				}
				u.SplitSize = bytes
				_ = ext.DB.SaveUser(u)
				_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>Leech Split Size</b> set to: <code>%s</code>", ext.GetReadableFileSize(u.SplitSize)), msg.MessageID, nil)
				return
			}
		case "leech":
			text, btn := m.buildLeechMenu(u, msg.SenderName)
			_, _ = m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 2)
			return
		}
	}

	// Default: main menu (Mirror / Leech)
	if msg.SenderName != "" && u.Name == "" {
		u.Name = msg.SenderName
	}
	text, btn := m.buildMainMenu(u, msg.SenderName)
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 1)
}

func (m *SettingsModule) buildMainMenu(u *types.UserSettings, name string) (string, *telegram.ButtonMaker) {
	if name == "" {
		name = u.Name
	}
	if name == "" {
		name = fmt.Sprintf("%d", u.UserID)
	}
	text := fmt.Sprintf("㊂ <b><u>User Settings :</u></b>\n\n"+
		"<blockquote><b>╭</b> <b>Name :</b> <a href=\"tg://user?id=%d\">%s</a>\n"+
		"<b>╰</b> <b>User ID :</b> <code>%d</code></blockquote>",
		u.UserID, telegram.EscapeHTML(name), u.UserID)

	btn := telegram.NewButtonMaker()
	btn.IButton("Universal Settings", fmt.Sprintf("userset %d universal", u.UserID))
	btn.Row()
	btn.IButton("Mirror Settings", fmt.Sprintf("userset %d mirror", u.UserID))
	btn.Row()
	btn.IButton("Leech Settings", fmt.Sprintf("userset %d leech", u.UserID))
	btn.Row()
	if m.settingsChanged(u) {
		btn.IButtonStyled("Reset Setting", fmt.Sprintf("userset %d reset_all", u.UserID), "danger")
		btn.Row()
	}
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	return text, btn
}

// settingsChanged reports whether any value covered by "reset_all" differs from the default.
func (m *SettingsModule) settingsChanged(u *types.UserSettings) bool {
	return u.Prefix != "" || u.Suffix != "" || u.Caption != "" ||
		u.RemnameAuto != "" || u.RemnameRegex != "" || u.SplitSize != 0 ||
		u.Autorename || (u.RenameMethod != "" && u.RenameMethod != "auto") ||
		u.AutoThumbSet || u.EqualSplits || u.MediaGroup ||
		u.AsDoc != config.Get().AsDocument || u.CustomThumb != "" ||
		u.CapFont != "" || u.ExcludedExts != "" || u.MirrorHost != "" ||
		u.Screenshots != 0 || u.SampleSec != 0 || u.MediaInfoSet || u.MediaStoreSet
}

func (m *SettingsModule) buildLeechMenu(u *types.UserSettings, userName string) (string, *telegram.ButtonMaker) {
	if userName == "" {
		userName = u.Name
	}
	if userName == "" {
		userName = fmt.Sprintf("%d", u.UserID)
	}
	nameTag := fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", u.UserID, telegram.EscapeHTML(userName))

	// Daily leech usage and limit
	_, dailyBytes := ext.GetUserDailyStats(u.UserID)
	dlUsed := ext.GetReadableFileSize(dailyBytes)
	cfg := config.Get()
	dlLimit := "∞"
	if cfg.DailyLeechLimit > 0 {
		dlLimit = ext.GetReadableFileSize(int64(cfg.DailyLeechLimit) * 1024 * 1024 * 1024)
	}
	dlStr := fmt.Sprintf("%s / %s", dlUsed, dlLimit)

	ltype := "MEDIA"
	if u.AsDoc {
		ltype = "DOCUMENT"
	}

	thumbStatus := "Not Exists"
	if u.CustomThumb != "" {
		if _, err := os.Stat(u.CustomThumb); err == nil {
			thumbStatus = "Exists"
		}
	}

	splitSize := ext.GetReadableFileSize(config.GetMaxSplitSize()) + " (Default)"
	if u.SplitSize > 0 {
		splitSize = ext.GetReadableFileSize(u.SplitSize)
	}

	equalSplits := "Disabled"
	if u.EqualSplits {
		equalSplits = "Enabled"
	}

	mediaGroup := "Disabled"
	if u.MediaGroup {
		mediaGroup = "Enabled"
	}

	caption := "Not Exists"
	if u.Caption != "" {
		caption = telegram.EscapeHTML(u.Caption)
	}

	prefix := "Not Exists"
	if u.Prefix != "" {
		prefix = telegram.EscapeHTML(u.Prefix)
	}

	suffix := "Not Exists"
	if u.Suffix != "" {
		suffix = telegram.EscapeHTML(u.Suffix)
	}

	dumps := "Not Exists"
	if u.DumpChatID != 0 {
		dumps = fmt.Sprintf("%d", u.DumpChatID)
	}

	remnameAuto := "Not Exists"
	if u.RemnameAuto != "" {
		remnameAuto = telegram.EscapeHTML(u.RemnameAuto)
	}

	remnameRegex := "Not Exists"
	if u.RemnameRegex != "" {
		remnameRegex = telegram.EscapeHTML(u.RemnameRegex)
	}

	renameMethod := "Auto Rename"
	if u.RenameMethod == "regex" {
		renameMethod = "Regex"
	}

	autorename := "Disabled"
	if u.Autorename {
		autorename = "Enabled"
	}

	autoThumb := "Disabled"
	if u.AutoThumbOn(config.Get().AutoThumbnail) {
		autoThumb = "Enabled"
	}

	metadata := "Not Exists"
	if u.Metadata != nil && u.Metadata["all"] != "" {
		metadata = telegram.EscapeHTML(u.Metadata["all"])
	}

	// Exact AZML text format with small caps header
	text := fmt.Sprintf("㊂ <b><u>%s for %s</u></b>\n\n"+
		"<blockquote><b>╭</b> <b>Daily Leech :</b> <code>%s</code> per day\n"+
		"┠ <b>Leech Type :</b> <i>%s</i>\n"+
		"┠ <b>Custom Thumbnail :</b> <i>%s</i>\n"+
		"┠ <b>Leech Split Size :</b> <code>%s</code>\n"+
		"┠ <b>Equal Splits :</b> <i>%s</i>\n"+
		"┠ <b>Media Group :</b> <i>%s</i>\n"+
		"┠ <b>Leech Caption :</b> <code>%s</code>\n"+
		"┠ <b>Leech Prefix :</b> <code>%s</code>\n"+
		"┠ <b>Leech Suffix :</b> <code>%s</code>\n"+
		"┠ <b>Leech Dumps :</b> <code>%s</code>\n"+
		"┠ <b>AutoRename Template :</b> <code>%s</code>\n"+
		"┠ <b>Regex Remname :</b> <code>%s</code>\n"+
		"┠ <b>Rename Method :</b> <i>%s</i>\n"+
		"┠ <b>AutoRename :</b> <i>%s</i>\n"+
		"┠ <b>Auto Thumbnail :</b> <i>%s</i>\n"+
		"┠ <b>Leech Metadata :</b> <code>%s</code>\n"+
		"┠ <b>Caption Font :</b> <i>%s</i>\n"+
		"┠ <b>Excluded Ext :</b> <code>%s</code>\n"+
		"┠ <b>Screenshots :</b> <i>%s</i>\n"+
		"<b>╰</b> <b>Sample Video :</b> <i>%s</i></blockquote>",
		"Leech Settings",
		nameTag, dlStr, ltype, thumbStatus, splitSize, equalSplits, mediaGroup,
		caption, prefix, suffix, dumps, remnameAuto, remnameRegex, renameMethod,
		autorename, autoThumb, metadata, capFontName(u.CapFont), excludedText(u.ExcludedExts), shotsText(u.Screenshots), sampleText(u.SampleSec))

	btn := telegram.NewButtonMaker()
	docLabel := "Send As Document"
	if u.AsDoc {
		docLabel = "Send As Media"
	}
	btn.IButton(docLabel, fmt.Sprintf("userset %d doc", u.UserID))
	btn.IButton("Thumbnail", fmt.Sprintf("userset %d thumb", u.UserID))
	btn.Row()
	btn.IButton("Leech Splits", fmt.Sprintf("userset %d split_size", u.UserID))
	btn.IButton("Caption", fmt.Sprintf("userset %d lcaption", u.UserID))
	btn.Row()
	btn.IButton("Prefix", fmt.Sprintf("userset %d lprefix", u.UserID))
	btn.IButton("Suffix", fmt.Sprintf("userset %d lsuffix", u.UserID))
	btn.Row()
	btn.IButton("AutoRename Template", fmt.Sprintf("userset %d lremname_auto", u.UserID))
	btn.IButton("Regex Remname", fmt.Sprintf("userset %d lremname_regex", u.UserID))
	btn.Row()
	btn.IButton("Dump", fmt.Sprintf("userset %d ldump", u.UserID))
	btn.IButton("Metadata", fmt.Sprintf("userset %d lmeta", u.UserID))
	btn.Row()
	toggle := func(label string, on bool, data string) {
		if on {
			btn.IButtonStyled("✓ "+label, data, "success")
		} else {
			btn.IButton(label, data)
		}
	}
	toggle("AutoRename", u.Autorename, fmt.Sprintf("userset %d autorename", u.UserID))
	toggle("Auto Thumbnail", u.AutoThumbOn(config.Get().AutoThumbnail), fmt.Sprintf("userset %d auto_thumb", u.UserID))
	btn.Row()
	btn.IButton("Caption Font", fmt.Sprintf("userset %d capfont", u.UserID))
	btn.IButton("Excluded Ext", fmt.Sprintf("userset %d lexcluded", u.UserID))
	btn.Row()
	btn.IButton("Screenshots", fmt.Sprintf("userset %d shots", u.UserID))
	btn.IButton("Sample Video", fmt.Sprintf("userset %d sample", u.UserID))
	btn.Row()
	toggle("Media Store", u.MediaStoreOn(config.Get().MediaStore), fmt.Sprintf("userset %d media_store", u.UserID))
	if u.RenameMethod == "regex" {
		btn.IButton("Rename Method: Regex", fmt.Sprintf("userset %d rename_method", u.UserID))
	} else {
		btn.IButton("Rename Method: Auto", fmt.Sprintf("userset %d rename_method", u.UserID))
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d main", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()

	return text, btn
}

func (m *SettingsModule) buildThumbnailMenu(u *types.UserSettings, banner ...string) (string, *telegram.ButtonMaker) {
	status := "Not Exists"
	exists := false
	if u.CustomThumb != "" {
		if _, err := os.Stat(u.CustomThumb); err == nil {
			status = "Exists"
			exists = true
		}
	}

	bannerText := ""
	if len(banner) > 0 && banner[0] != "" {
		bannerText = banner[0] + "\n\n"
	}

	text := fmt.Sprintf("%s㊂ <b><u>Thumbnail Settings :</u></b>\n\n"+
		"➲ <b>Custom Thumbnail :</b> <i>%s</i>\n\n"+
		"➲ <b>Description :</b> <i>Custom Thumbnail to appear on the Leeched files uploaded by the bot</i>",
		bannerText, status)

	btn := telegram.NewButtonMaker()
	if exists {
		btn.IButton("View Thumbnail", fmt.Sprintf("userset %d vthumb", u.UserID))
		btn.Row()
	}
	stdMenuButtons(btn, u.UserID, "Thumbnail", "cthumb", "dthumb", exists)
	return text, btn
}

func (m *SettingsModule) buildSplitSizeMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	splitSize := ext.GetReadableFileSize(config.GetMaxSplitSize()) + " (Default)"
	if u.SplitSize > 0 {
		splitSize = ext.GetReadableFileSize(u.SplitSize)
	}

	text := fmt.Sprintf("㊂ <b><u>Leech Splits Settings :</u></b>\n\n"+
		"➲ <b>Leech Split Size :</b> <i>%s</i>\n\n"+
		"➲ <b>Description :</b> <i>Leech Splits Size is the size to split the Leeched File before uploading</i>",
		splitSize)

	btn := telegram.NewButtonMaker()
	if u.EqualSplits {
		btn.IButton("Disable Equal Splits", fmt.Sprintf("userset %d esplits", u.UserID))
	} else {
		btn.IButton("Enable Equal Splits", fmt.Sprintf("userset %d esplits", u.UserID))
	}
	if u.MediaGroup {
		btn.IButton("Disable Media Group", fmt.Sprintf("userset %d mgroup", u.UserID))
	} else {
		btn.IButton("Enable Media Group", fmt.Sprintf("userset %d mgroup", u.UserID))
	}
	btn.Row()
	stdMenuButtons(btn, u.UserID, "Leech Splits", "edit_split", "split_default", u.SplitSize > 0)
	return text, btn
}

func (m *SettingsModule) buildCaptionMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	caption := "Not Exists (Default)"
	if u.Caption != "" {
		caption = telegram.EscapeHTML(u.Caption)
	}

	text := fmt.Sprintf("㊂ <b><u>Caption Settings :</u></b>\n\n"+
		"➲ <b>Leech Filename Caption :</b> %s\n\n"+
		"➲ <b>Description :</b> <i>Leech Caption is the Custom Caption on the Leech Files Uploaded by the bot</i>",
		caption)

	btn := telegram.NewButtonMaker()
	stdMenuButtons(btn, u.UserID, "Caption", "edit_lcaption", "del_lcaption", u.Caption != "")
	return text, btn
}

func (m *SettingsModule) buildPrefixMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	prefix := "Not Exists"
	if u.Prefix != "" {
		prefix = telegram.EscapeHTML(u.Prefix)
	}

	text := fmt.Sprintf("㊂ <b><u>Prefix Settings :</u></b>\n\n"+
		"➲ <b>Leech Filename Prefix :</b> %s\n\n"+
		"➲ <b>Description :</b> <i>Leech Filename Prefix is the Front Part attacted with the Filename of the Leech Files.</i>",
		prefix)

	btn := telegram.NewButtonMaker()
	stdMenuButtons(btn, u.UserID, "Prefix", "edit_lprefix", "del_lprefix", u.Prefix != "")
	return text, btn
}

func (m *SettingsModule) buildSuffixMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	suffix := "Not Exists"
	if u.Suffix != "" {
		suffix = telegram.EscapeHTML(u.Suffix)
	}

	text := fmt.Sprintf("㊂ <b><u>Suffix Settings :</u></b>\n\n"+
		"➲ <b>Leech Filename Suffix :</b> %s\n\n"+
		"➲ <b>Description :</b> <i>Leech Filename Suffix is the End Part attached with the Filename of the Leech Files</i>",
		suffix)

	btn := telegram.NewButtonMaker()
	stdMenuButtons(btn, u.UserID, "Suffix", "edit_lsuffix", "del_lsuffix", u.Suffix != "")
	return text, btn
}

func (m *SettingsModule) buildAutoRenameMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	tmpl := "Not Exists"
	if u.RemnameAuto != "" {
		tmpl = telegram.EscapeHTML(u.RemnameAuto)
	}

	text := fmt.Sprintf("㊂ <b><u>AutoRename Settings :</u></b>\n\n"+
		"➲ <b>AutoRename Template :</b> %s\n\n"+
		"➲ <b>Description :</b> <i>AutoRename Template builds a new Filename for the Leech Files from {title} {season} {episode} {quality}</i>",
		tmpl)

	btn := telegram.NewButtonMaker()
	stdMenuButtons(btn, u.UserID, "AutoRename", "edit_lremname_auto", "del_lremname_auto", u.RemnameAuto != "")
	return text, btn
}

func (m *SettingsModule) buildRegexMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	regexVal := "Not Exists"
	if u.RemnameRegex != "" {
		regexVal = telegram.EscapeHTML(u.RemnameRegex)
	}

	text := fmt.Sprintf("㊂ <b><u>Remname Settings :</u></b>\n\n"+
		"➲ <b>Leech Filename Remname :</b> %s\n\n"+
		"➲ <b>Description :</b> <i>Leech Filename Remname is combination of Regex(s) used for removing or manipulating Filename of the Leech Files</i>",
		regexVal)

	btn := telegram.NewButtonMaker()
	stdMenuButtons(btn, u.UserID, "Remname", "edit_lremname_regex", "del_lremname_regex", u.RemnameRegex != "")
	return text, btn
}

func (m *SettingsModule) buildDumpMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	dumpStr := "Not Exists"
	if u.DumpChatID != 0 {
		dumpStr = fmt.Sprintf("%d", u.DumpChatID)
	}

	text := fmt.Sprintf("㊂ <b><u>User Dump Settings :</u></b>\n\n"+
		"➲ <b>Leech Filename User Dump :</b> %s\n\n"+
		"➲ <b>Description :</b> <i>Leech Files User Dump for Personal Use as a Storage.</i>",
		dumpStr)

	btn := telegram.NewButtonMaker()
	stdMenuButtons(btn, u.UserID, "User Dump", "edit_ldump", "del_ldump", u.DumpChatID != 0)
	return text, btn
}

func (m *SettingsModule) buildMetadataMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	metaAll := "Not Exists"
	if u.Metadata != nil && u.Metadata["all"] != "" {
		metaAll = telegram.EscapeHTML(u.Metadata["all"])
	}

	text := fmt.Sprintf("㊂ <b><u>Metadata Settings :</u></b>\n\n"+
		"➲ <b>Leech Filename Metadata :</b> %s\n\n"+
		"➲ <b>Description :</b> <i>Your Channel Name that will be used while editing metadata of the Video File</i>",
		metaAll)

	btn := telegram.NewButtonMaker()
	stdMenuButtons(btn, u.UserID, "Metadata", "edit_lmeta", "del_lmeta", u.Metadata != nil && u.Metadata["all"] != "")
	return text, btn
}

func (m *SettingsModule) HandleCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUserID int64) {
	parts := strings.Fields(data)
	if len(parts) >= 2 {
		targetUID, err := strconv.ParseInt(parts[1], 10, 64)
		if err == nil && targetUID != fromUserID {
			_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
			return
		}
	}
	if len(parts) < 3 {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}

	targetUID, _ := strconv.ParseInt(parts[1], 10, 64)
	if targetUID != fromUserID {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}

	action := parts[2]
	u := m.userMap.GetOrCreate(targetUID)

	// like the Python bot (handler_dict[user_id] = False): any other button ends the waiting for
	// a value (◀️, ❌, Stop Change, Delete...), only the button that asks for one starts it
	if !strings.HasPrefix(action, "edit_") && action != "cthumb" {
		if s, ok := ext.GetPendingSettingSession(targetUID); ok && s != nil && !strings.HasPrefix(s.Key, "vt_") {
			ext.ClearPendingSettingSession(targetUID)
		}
		ext.ClearPendingThumbSession(targetUID)
	}

	switch action {
	// 1. Navigation
	case "main", "back":
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		text, btn := m.buildMainMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 1)
	case "iesettings":
		text, btn := m.buildIEMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "export":
		// the file has the user's cookies and keys: only in PM
		if _, ok := peer.(*tg.InputPeerUser); !ok {
			text, btn := m.buildIEMenu(u, "× <i>Export works only in the bot PM: open /us there.</i>")
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
			_ = m.sender.AnswerCallback(ctx, queryID, "", false)
			return
		}
		note := "✓ <i>Exported: the file is below.</i>"
		if err := m.sendUserExport(ctx, peer, msgID, u); err != nil {
			note = "× <i>Export failed: " + telegram.EscapeHTML(err.Error()) + "</i>"
		}
		text, btn := m.buildIEMenu(u, note)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "edit_importset":
		ext.SetPendingSettingSession(targetUID, "importset", 0, msgID, 60*time.Second)
		text, _ := m.buildIEMenu(u, "")
		btn := telegram.NewButtonMaker()
		btn.IButton("Stop Change", fmt.Sprintf("userset %d iesettings", u.UserID))
		btn.Row()
		btn.IButtonStyled("◀️", fmt.Sprintf("userset %d universal", u.UserID), "primary")
		btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text+"\n\n➲ <i>Send the file <code>"+UserSettingsFileName+"</code> (from Export of any bot) within 60 seconds.</i>", btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "leech":
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		text, btn := m.buildLeechMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
	case "mirror":
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		text, btn := m.buildMirrorHostMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
	case "reset_all":
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		btn := telegram.NewButtonMaker()
		btn.IButtonStyled("✓", fmt.Sprintf("userset %d reset_now y", u.UserID), "success")
		btn.IButtonStyled("✘", fmt.Sprintf("userset %d reset_now n", u.UserID), "danger")
		btn.Row()
		btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, "Do you want to Reset Settings ?", btn, 2)
	case "reset_now":
		if len(parts) < 4 || parts[3] != "y" {
			_ = m.sender.AnswerCallback(ctx, queryID, "", false)
			text, btn := m.buildMainMenu(u, "")
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 1)
			return
		}
		u.Prefix = ""
		u.Suffix = ""
		u.Caption = ""
		u.RemnameAuto = ""
		u.RemnameRegex = ""
		u.SplitSize = 0
		u.Autorename = false
		u.RenameMethod = "auto"
		u.AutoThumb, u.AutoThumbSet = false, false
		u.EqualSplits = false
		u.MediaGroup = false
		u.AsDoc = config.Get().AsDocument
		u.CapFont = ""
		u.ExcludedExts = ""
		u.MirrorHost = ""
		u.BotPM = false
		u.Screenshots = 0
		u.SampleSec = 0
		u.MediaInfo = false
		u.MediaInfoSet = false
		u.MediaStore, u.MediaStoreSet = false, false
		if u.CustomThumb != "" {
			_ = os.Remove(u.CustomThumb)
			u.CustomThumb = ""
			u.ThumbData = nil
		}
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		_ = m.sender.AnswerCallback(ctx, queryID, "All user settings reset to default!", true)
		text, btn := m.buildMainMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 1)
	case "close":
		ext.ClearPendingSettingSession(targetUID)
		ext.ClearPendingThumbSession(targetUID)
		_ = m.sender.AnswerCallback(ctx, queryID, "Settings closed", false)
		m.sender.DeleteWithCommand(ctx, peer, msgID)

	// 2. Direct Toggles (Edit message in-place without glitch)
	case "doc":
		u.AsDoc = !u.AsDoc
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		st := "MEDIA"
		if u.AsDoc {
			st = "DOCUMENT"
		}
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Upload Type set to %s", st), false)
		text, btn := m.buildLeechMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "autorename":
		u.Autorename = !u.Autorename
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		st := "Disabled"
		if u.Autorename {
			st = "Enabled"
		}
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("AutoRename is now %s", st), false)
		text, btn := m.buildLeechMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "auto_thumb":
		u.AutoThumb, u.AutoThumbSet = !u.AutoThumbOn(config.Get().AutoThumbnail), true
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		st := "Disabled"
		if u.AutoThumb {
			st = "Enabled"
		}
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Auto Thumbnail is now %s", st), false)
		text, btn := m.buildLeechMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "rename_method":
		if u.RenameMethod == "auto" {
			u.RenameMethod = "regex"
		} else {
			u.RenameMethod = "auto"
		}
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Method: %s", strings.Title(u.RenameMethod)), false)
		text, btn := m.buildLeechMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "esplits":
		u.EqualSplits = !u.EqualSplits
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		st := "Disabled"
		if u.EqualSplits {
			st = "Enabled"
		}
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Equal Splits is now %s", st), false)
		text, btn := m.buildSplitSizeMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "mgroup":
		u.MediaGroup = !u.MediaGroup
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		st := "Disabled"
		if u.MediaGroup {
			st = "Enabled"
		}
		_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Media Group is now %s", st), false)
		text, btn := m.buildSplitSizeMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	// 3. Thumbnail Submenu
	case "thumb":
		ext.ClearPendingThumbSession(targetUID)
		text, btn := m.buildThumbnailMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "cthumb":
		// the page stays, the request comes under it (like the other settings and the Python bot)
		ext.SetPendingThumbSession(targetUID, telegram.PeerToChatID(peer), msgID, 60*time.Second)
		text, _ := m.buildThumbnailMenu(u)
		exists := u.CustomThumb != ""
		btn := telegram.NewButtonMaker()
		if exists {
			btn.IButton("View Thumbnail", fmt.Sprintf("userset %d vthumb", u.UserID))
			btn.Row()
		}
		btn.IButton("Stop Change", fmt.Sprintf("userset %d thumb", u.UserID))
		if exists {
			btn.IButton("↻ Delete", fmt.Sprintf("userset %d dthumb", u.UserID))
		}
		btn.Row()
		btn.IButtonStyled("◀️", fmt.Sprintf("userset %d leech", u.UserID), "primary")
		btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text+"\n\nSend a photo to save it as custom thumbnail.\n<b>Timeout:</b> 60 sec", btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "vthumb":
		if u.CustomThumb == "" {
			_ = m.sender.AnswerCallback(ctx, queryID, "No thumbnail configured!", true)
			return
		}
		if _, err := os.Stat(u.CustomThumb); err != nil {
			_ = m.sender.AnswerCallback(ctx, queryID, "Thumbnail file not found on disk!", true)
			return
		}
		closeBtn := telegram.NewButtonMaker()
		closeBtn.IButtonStyled("Close", fmt.Sprintf("start_close %d", fromUserID), "danger") // deletes only this photo
		_, err := m.sender.SendLocalPhoto(ctx, peer, u.CustomThumb, "⌬ <b>Your Custom Thumbnail</b>", 0, closeBtn.BuildMenu(1))
		if err != nil {
			_ = m.sender.AnswerCallback(ctx, queryID, fmt.Sprintf("Error sending thumbnail: %v", err), true)
		} else {
			_ = m.sender.AnswerCallback(ctx, queryID, "Thumbnail sent!", false)
		}

	case "dthumb":
		ext.ClearPendingThumbSession(targetUID)
		if u.CustomThumb != "" {
			_ = os.Remove(u.CustomThumb)
		}
		dest := fmt.Sprintf("Thumbnails/%d.jpg", targetUID)
		_ = os.Remove(dest)
		u.CustomThumb = ""
		u.ThumbData = nil
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildThumbnailMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "✓ Custom thumbnail deleted!", false)

	// 4. Split Size Submenu
	case "split_size":
		text, btn := m.buildSplitSizeMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "split_2gb":
		u.SplitSize = 2000 * 1024 * 1024
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildSplitSizeMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Split size set to 2GB", false)

	case "split_4gb":
		u.SplitSize = 4000 * 1024 * 1024
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildSplitSizeMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Split size set to 4GB", false)

	case "split_default":
		u.SplitSize = 0
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildSplitSizeMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Reset to default limit", false)

	case "edit_split":
		m.showEditPrompt(ctx, peer, queryID, msgID, u, "edit_split")

	// 5. Caption Submenu
	case "lcaption":
		text, btn := m.buildCaptionMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_lcaption":
		m.showEditPrompt(ctx, peer, queryID, msgID, u, "edit_lcaption")

	case "del_lcaption":
		u.Caption = ""
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildCaptionMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Caption reset to default", false)

	// 6. Prefix Submenu
	case "lprefix":
		text, btn := m.buildPrefixMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_lprefix":
		m.showEditPrompt(ctx, peer, queryID, msgID, u, "edit_lprefix")

	case "del_lprefix":
		u.Prefix = ""
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildPrefixMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Prefix deleted", false)

	// 7. Suffix Submenu
	case "lsuffix":
		text, btn := m.buildSuffixMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_lsuffix":
		m.showEditPrompt(ctx, peer, queryID, msgID, u, "edit_lsuffix")

	case "del_lsuffix":
		u.Suffix = ""
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildSuffixMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Suffix deleted", false)

	// 8. AutoRename Template Submenu
	case "lremname_auto":
		text, btn := m.buildAutoRenameMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_lremname_auto":
		m.showEditPrompt(ctx, peer, queryID, msgID, u, "edit_lremname_auto")

	case "del_lremname_auto":
		u.RemnameAuto = ""
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildAutoRenameMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "AutoRename template deleted", false)

	// 9. Regex Remname Submenu
	case "lremname_regex":
		text, btn := m.buildRegexMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_lremname_regex":
		m.showEditPrompt(ctx, peer, queryID, msgID, u, "edit_lremname_regex")

	case "del_lremname_regex":
		u.RemnameRegex = ""
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildRegexMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Regex rule deleted", false)

	// 10. Dump Channel Submenu
	case "ldump":
		text, btn := m.buildDumpMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_ldump":
		m.showEditPrompt(ctx, peer, queryID, msgID, u, "edit_ldump")

	case "del_ldump":
		u.DumpChatID = 0
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildDumpMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Dump channel cleared", false)

	// 11. Metadata Submenu
	case "lmeta", "metadata":
		text, btn := m.buildMetadataMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "botpm":
		u.BotPM = !u.BotPM
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		_ = m.sender.AnswerCallback(ctx, queryID, "Bot PM is now "+onOff(u.BotPM), false)
		text, btn := m.buildLeechMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "capfont":
		text, btn := m.buildCapFontMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "setfont":
		if len(parts) > 3 {
			if parts[3] == "none" {
				u.CapFont = "" // follow the owner's CAP_FONT
			} else if parts[3] == "regular" {
				u.CapFont = "regular"
			} else if _, ok := captionFonts[parts[3]]; ok {
				u.CapFont = parts[3]
			}
			go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		}
		_ = m.sender.AnswerCallback(ctx, queryID, "Caption Font: "+capFontName(u.CapFont), false)
		text, btn := m.buildCapFontMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "mhost":
		text, btn := m.buildMirrorHostMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "sethost":
		if len(parts) > 3 {
			if parts[3] == "none" {
				u.MirrorHost = ""
			} else if h, ok := ext.FindMirrorHost(parts[3]); ok {
				u.MirrorHost = h.ID
			}
			go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		}
		_ = m.sender.AnswerCallback(ctx, queryID, "Upload To: "+mirrorHostText(u.MirrorHost), false)
		var text string
		var btn *telegram.ButtonMaker
		if u.MirrorHost != "" {
			text, btn = m.buildDDLHostMenu(u, u.MirrorHost)
		} else {
			text, btn = m.buildMirrorHostMenu(u)
		}
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "universal":
		text, btn := m.buildUniversalMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "megalogin", "ffcmds":
		text, btn := m.buildExtraMenu(u, action, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_megalogin", "edit_ffcmds":
		key := strings.TrimPrefix(action, "edit_")
		ext.SetPendingSettingSession(targetUID, key, 0, msgID, 60*time.Second)
		prompt := "➲ <i>Send your MEGA email and password separated by a space within 60 seconds:</i>\n<code>email@example.com password</code>\n<i>The message is deleted right away.</i>"
		if key == "ffcmds" {
			prompt = "➲ <i>Send a command as</i> <code>name: ffmpeg args</code> <i>within 60 seconds, e.g.</i>\n<code>tomkv: -map 0 -c copy</code>\n<i>Use it with</i> <code>-ff name</code>. <i>Send</i> <code>name:</code> <i>alone to delete one.</i>"
		}
		text, _ := m.buildExtraMenu(u, key, "")
		btn := telegram.NewButtonMaker()
		btn.IButton("Stop Change", fmt.Sprintf("userset %d %s", u.UserID, key))
		btn.Row()
		btn.IButtonStyled("◀️", fmt.Sprintf("userset %d universal", u.UserID), "primary")
		btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text+"\n\n"+prompt, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "del_megalogin", "del_ffcmds":
		key := strings.TrimPrefix(action, "del_")
		if key == "megalogin" {
			u.MegaEmail, u.MegaPass = "", ""
		} else {
			u.FFmpegCommands = nil
		}
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildExtraMenu(u, key, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Deleted", false)

	case "ytopt", "ycookies":
		text, btn := m.buildYtdlSettingMenu(u, action, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_ytopt", "edit_ycookies":
		key := strings.TrimPrefix(action, "edit_")
		ext.SetPendingSettingSession(targetUID, key, 0, msgID, 60*time.Second)
		prompt := "➲ <i>Send yt-dlp options (command line style) within 60 seconds, e.g.</i>\n<code>--embed-subs --sub-langs en,he --embed-thumbnail</code>"
		if key == "ycookies" {
			prompt = "➲ <i>Send your <code>cookies.txt</code> file (Netscape format) within 60 seconds.</i>"
		}
		text, _ := m.buildYtdlSettingMenu(u, key, "")
		btn := telegram.NewButtonMaker()
		btn.IButton("Stop Change", fmt.Sprintf("userset %d %s", u.UserID, key))
		btn.Row()
		btn.IButtonStyled("◀️", fmt.Sprintf("userset %d universal", u.UserID), "primary")
		btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text+"\n\n"+prompt, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "del_ytopt", "del_ycookies":
		key := strings.TrimPrefix(action, "del_")
		if key == "ytopt" {
			u.YtOpt = ""
		} else {
			u.YtCookies = ""
		}
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildYtdlSettingMenu(u, key, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Deleted", false)

	case "media_store":
		u.MediaStore = !u.MediaStoreOn(config.Get().MediaStore)
		u.MediaStoreSet = true
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		_ = m.sender.AnswerCallback(ctx, queryID, "Media Store is now "+onOff(u.MediaStore), false)
		text, btn := m.buildLeechMenu(u, "")
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "mediainfo":
		u.MediaInfo = !u.MediaInfoOn(config.Get().ShowMediaInfo)
		u.MediaInfoSet = true
		go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
		_ = m.sender.AnswerCallback(ctx, queryID, "MediaInfo is now "+onOff(u.MediaInfo), false)
		text, btn := m.buildUniversalMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)

	case "shots", "setshots":
		if action == "setshots" && len(parts) > 3 {
			if n, err := strconv.Atoi(parts[3]); err == nil && n >= 0 && n <= 10 {
				u.Screenshots = n
				go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
			}
		}
		text, btn := m.buildChoiceMenu(u, "Screenshots", "Screenshots", shotsText(u.Screenshots),
			"Screenshots of every leeched video, posted as a link under the file.", "setshots", "shots", u.Screenshots, []int{0, 3, 5, 8, 10})
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 3)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "sample", "setsample":
		if action == "setsample" && len(parts) > 3 {
			if n, err := strconv.Atoi(parts[3]); err == nil && n >= 0 && n <= 60 {
				u.SampleSec = n
				go func(usr *types.UserSettings) { _ = ext.DB.SaveUser(usr) }(u)
			}
		}
		text, btn := m.buildChoiceMenu(u, "Sample Video", "Sample Video", sampleText(u.SampleSec),
			"A short sample from the middle of every leeched video, sent after the file.", "setsample", "sample", u.SampleSec, []int{0, 10, 20, 30, 60})
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 3)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "ddlservers":
		text, btn := m.buildMirrorHostMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "ddlhost":
		if len(parts) > 3 {
			text, btn := m.buildDDLHostMenu(u, parts[3])
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		}
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_ddlkey":
		if len(parts) < 4 {
			_ = m.sender.AnswerCallback(ctx, queryID, "", false)
			return
		}
		h, ok := ext.FindMirrorHost(parts[3])
		if !ok {
			_ = m.sender.AnswerCallback(ctx, queryID, "", false)
			return
		}
		ext.SetPendingSettingSession(targetUID, "ddlkey_"+h.ID, 0, msgID, 60*time.Second)
		text, _ := m.buildDDLHostMenu(u, h.ID)
		btn := telegram.NewButtonMaker()
		btn.IButton("Stop Change", fmt.Sprintf("userset %d ddlhost %s", u.UserID, h.ID))
		btn.Row()
		btn.IButtonStyled("◀️", fmt.Sprintf("userset %d mirror", u.UserID), "primary")
		btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
		btn.Row()
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text+"\n\n"+h.Prompt+"\n<b>Timeout:</b> 60 sec", btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Send the key...", false)

	case "del_ddlkey":
		if len(parts) > 3 && u.DDLKeys != nil {
			keys := maps.Clone(u.DDLKeys)
			delete(keys, parts[3])
			u.DDLKeys = keys
			_ = ext.DB.SaveUser(u)
			text, btn := m.buildDDLHostMenu(u, parts[3])
			_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		}
		_ = m.sender.AnswerCallback(ctx, queryID, "Key deleted", false)

	case "lexcluded":
		text, btn := m.buildExcludedMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)

	case "edit_lexcluded":
		m.showEditPrompt(ctx, peer, queryID, msgID, u, "edit_lexcluded")

	case "del_lexcluded":
		u.ExcludedExts = ""
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildExcludedMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Excluded extensions deleted", false)

	case "edit_lmeta":
		m.showEditPrompt(ctx, peer, queryID, msgID, u, "edit_lmeta")

	case "del_lmeta":
		if u.Metadata != nil {
			delete(u.Metadata, "all")
		}
		_ = ext.DB.SaveUser(u)
		text, btn := m.buildMetadataMenu(u)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "Metadata cleared", false)

	default:
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	}
}

// HandleSettingUpdated refreshes and edits the prompt message back to the corresponding submenu
func (m *SettingsModule) HandleSettingUpdated(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, u *types.UserSettings, key string) {
	if promptMsgID <= 0 || u == nil {
		return
	}
	var text string
	var btn *telegram.ButtonMaker
	if hostID := strings.TrimPrefix(key, "ddlkey_"); hostID != key {
		text, btn = m.buildDDLHostMenu(u, hostID)
		_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
		return
	}
	switch key {
	case "megalogin", "ffcmds":
		text, btn = m.buildExtraMenu(u, key, ext.TakeSettingNote(u.UserID))
	case "ytopt", "ycookies":
		text, btn = m.buildYtdlSettingMenu(u, key, "")
	case "importset", "iesettings":
		text, btn = m.buildIEMenu(u, ext.TakeSettingNote(u.UserID))
	case "lexcluded":
		text, btn = m.buildExcludedMenu(u)
	case "lprefix":
		text, btn = m.buildPrefixMenu(u)
	case "lsuffix":
		text, btn = m.buildSuffixMenu(u)
	case "lcaption":
		text, btn = m.buildCaptionMenu(u)
	case "lremname_auto":
		text, btn = m.buildAutoRenameMenu(u)
	case "lremname_regex":
		text, btn = m.buildRegexMenu(u)
	case "ldump":
		text, btn = m.buildDumpMenu(u)
	case "lmeta", "metadata":
		text, btn = m.buildMetadataMenu(u)
	case "split_size", "edit_split":
		text, btn = m.buildSplitSizeMenu(u)
	case "thumb", "cthumb":
		text, btn = m.buildThumbnailMenu(u, "✓ <b>Custom thumbnail saved successfully!</b>")
	default:
		text, btn = m.buildLeechMenu(u, "")
	}
	_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
}

func (m *SettingsModule) HandleAutoRename(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	u := m.userMap.GetOrCreate(msg.UserID)
	parts := strings.SplitN(strings.TrimSpace(msg.Text), " ", 2)

	// --- No argument: show current template or help ---
	if len(parts) == 1 || strings.TrimSpace(parts[1]) == "" {
		current := u.RemnameAuto
		if current == "" {
			current = config.Get().LeechFilenameRemnameAuto
		}

		if current != "" {
			text := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
				"<code>%s</code>\n\n"+
				"<b>Usage:</b> <code>"+telegram.Cmd("ar")+" [template]</code>\n"+
				"<b>Clear:</b> <code>"+telegram.Cmd("ar")+" off</code>",
				telegram.ToSmallCaps("Current AutoRename Template"),
				telegram.EscapeHTML(current))
			_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, telegram.CloseMarkup(msg.UserID))
		} else {
			text := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n"+
				"<b>No AutoRename template set.</b>\n\n"+
				"<b>Usage:</b> <code>"+telegram.Cmd("ar")+" [animeshrine.xyz] S0{season}E{episode} Dr. Stone {quality}.mkv</code>\n\n"+
				"<b>Available tags:</b>\n"+
				"  • <code>{title}</code> — parsed title\n"+
				"  • <code>{season}</code> — season number\n"+
				"  • <code>{episode}</code> — episode number\n"+
				"  • <code>{quality}</code> — video quality (e.g. 1080p)\n"+
				"  • <code>{codec}</code> — video codec\n"+
				"  • <code>{audio}</code> — audio codec\n"+
				"  • <code>{chapter}</code> — chapter number\n"+
				"  • <code>{episode:+N}</code> — episode with offset\n\n"+
				"<b>Clear:</b> <code>"+telegram.Cmd("ar")+" off</code>",
				telegram.ToSmallCaps("AutoRename Guide"))
			_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, telegram.CloseMarkup(msg.UserID))
		}
		return
	}

	template := strings.TrimSpace(parts[1])

	// --- Clear template ---
	switch strings.ToLower(template) {
	case "off", "clear", "reset", "del", "none":
		u.RemnameAuto = ""
		_ = ext.DB.SaveUser(u)
		_, _ = m.sender.SendMessage(ctx, peer, "✓ <b>AutoRename template cleared!</b>", msg.MessageID, nil)
		return

	default:
		u.RemnameAuto = template
		u.Autorename = true
		u.RenameMethod = "auto"
		_ = ext.DB.SaveUser(u)
		text := fmt.Sprintf("✓ <b>AutoRename template saved!</b>\n\n<code>%s</code>", telegram.EscapeHTML(template))
		_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, nil)
		return
	}
}

// captionFonts are the Caption Font choices (same as the Python bot).
var captionFonts = map[string]string{
	"b": "Bold", "i": "Italic", "u": "Underline", "s": "Strike", "code": "Mono", "spoiler": "Spoiler",
}

var captionFontOrder = []string{"b", "i", "u", "s", "code", "spoiler"}

func capFontName(f string) string {
	if n, ok := captionFonts[f]; ok {
		return n
	}
	if f == "regular" {
		return "Regular"
	}
	// no choice of his own: the owner's CAP_FONT from /bs
	if n, ok := captionFonts[config.Get().CapFont]; ok {
		return "Default (" + n + ")"
	}
	return "Default (Regular)"
}

func onOff(b bool) string {
	if b {
		return "Enabled"
	}
	return "Disabled"
}

func excludedText(s string) string {
	if strings.TrimSpace(s) == "" {
		return "Not Exists"
	}
	return telegram.EscapeHTML(s)
}

func mirrorHostText(id string) string {
	if id == "" {
		return "Ask every time"
	}
	if h, ok := ext.FindMirrorHost(id); ok {
		return h.Name
	}
	return id
}

func (m *SettingsModule) buildCapFontMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	text := fmt.Sprintf("㊂ <b><u>Caption Font Settings :</u></b>\n\n"+
		"➲ <b>Caption Font :</b> <i>%s</i>\n\n"+
		"➲ <b>Description :</b> <i>Font of the caption of your leeched files</i>",
		capFontName(u.CapFont))
	btn := telegram.NewButtonMaker()
	for i, f := range append([]string{"none", "regular"}, captionFontOrder...) {
		label := capFontName(f)
		if f == "none" {
			label = capFontName("")
		}
		if (f == "none" && u.CapFont == "") || f == u.CapFont {
			btn.IButtonStyled("✓ "+label, fmt.Sprintf("userset %d setfont %s", u.UserID, f), "success")
		} else {
			btn.IButton(label, fmt.Sprintf("userset %d setfont %s", u.UserID, f))
		}
		if i%2 == 1 {
			btn.Row()
		}
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d leech", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	return text, btn
}

func (m *SettingsModule) buildMirrorHostMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	name := u.Name
	if name == "" {
		name = fmt.Sprintf("%d", u.UserID)
	}
	var ready []string
	for _, h := range ext.AvailableMirrorHostsFor(u.DDLKeys) {
		ready = append(ready, h.Name)
	}
	text := fmt.Sprintf("㊂ <b><u>Mirror Settings for <a href=\"tg://user?id=%d\">%s</a></u></b>\n\n"+
		"<blockquote><b>╭</b> <b>Upload To :</b> <i>%s</i>\n"+
		"<b>╰</b> <b>DDL Server(s) :</b> <i>%s</i></blockquote>\n\n"+
		"➲ <b>Description :</b> <i>Tap a server to make it your default for /mirror or to add your own API key. 🔑 = your key is set.</i>",
		u.UserID, telegram.EscapeHTML(name), mirrorHostText(u.MirrorHost), strings.Join(ready, ", "))
	btn := telegram.NewButtonMaker()
	if u.MirrorHost == "" {
		btn.IButtonStyled("✓ Ask every time", fmt.Sprintf("userset %d sethost none", u.UserID), "success")
	} else {
		btn.IButton("Ask every time", fmt.Sprintf("userset %d sethost none", u.UserID))
	}
	btn.Row()
	for i, h := range ext.MirrorHosts {
		label := h.Name
		if u.DDLKeys[h.ID] != "" {
			label += " 🔑"
		}
		if h.ID == u.MirrorHost {
			btn.IButtonStyled("✓ "+label, fmt.Sprintf("userset %d ddlhost %s", u.UserID, h.ID), "success")
		} else {
			btn.IButton(label, fmt.Sprintf("userset %d ddlhost %s", u.UserID, h.ID))
		}
		if i%2 == 1 {
			btn.Row()
		}
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d main", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	return text, btn
}

func (m *SettingsModule) buildExcludedMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	text := fmt.Sprintf("㊂ <b><u>Excluded Extensions Settings :</u></b>\n\n"+
		"➲ <b>Excluded Extensions :</b> <code>%s</code>\n\n"+
		"➲ <b>Description :</b> <i>Files with these extensions are not uploaded</i>",
		excludedText(u.ExcludedExts))
	btn := telegram.NewButtonMaker()
	stdMenuButtons(btn, u.UserID, "Excluded Ext", "edit_lexcluded", "del_lexcluded", u.ExcludedExts != "")
	return text, btn
}

// stdMenuButtons adds the buttons of a setting page like the Python bot:
// Set / Change, ↻ Delete when set, then ◀️ and ❌.
// editPrompt: what Change / Set of a Leech setting asks for (like the Python bot the page stays,
// the request and the timeout come under it).
type editPrompt struct {
	key, page, del string
	ask            string
}

var leechEditPrompts = map[string]editPrompt{
	"edit_split":          {"split_size", "split_size", "split_default", "Send Leech split size in any comfortable size, like <code>2GB</code>, <code>500MB</code> or <code>1.46GB</code>."},
	"edit_lcaption":       {"lcaption", "lcaption", "del_lcaption", "Send Leech Caption. You can add HTML tags and <code>{filename}</code> <code>{size}</code> <code>{duration}</code> <code>{quality}</code> <code>{languages}</code>."},
	"edit_lprefix":        {"lprefix", "lprefix", "del_lprefix", "Send Leech Filename Prefix, e.g. <code>[MyChannel] </code>"},
	"edit_lsuffix":        {"lsuffix", "lsuffix", "del_lsuffix", "Send Leech Filename Suffix, e.g. <code> @MyChannel</code>"},
	"edit_lremname_auto":  {"lremname_auto", "lremname_auto", "del_lremname_auto", "Send the AutoRename template, e.g. <code>{title} S{season}E{episode} [{quality}]</code>"},
	"edit_lremname_regex": {"lremname_regex", "lremname_regex", "del_lremname_regex", "Send Leech Filename Remname as <code>pattern:replacement</code>, e.g. <code>\\[TG\\]:</code>"},
	"edit_ldump":          {"ldump", "ldump", "del_ldump", "Send Leech Dump Channel ID, e.g. <code>-1001234567890</code>\n<b>NOTE:</b> Make the bot admin in the channel, else it will not accept."},
	"edit_lexcluded":      {"lexcluded", "lexcluded", "del_lexcluded", "Send the extensions separated by space, e.g. <code>.txt .nfo .jpg</code>"},
	"edit_lmeta":          {"lmeta", "lmeta", "del_lmeta", "Send Leech Metadata, e.g. <code>Encoded by MyChannel</code>"},
}

// showEditPrompt shows the page of the setting with the request under it and waits for the value.
func (m *SettingsModule) showEditPrompt(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, u *types.UserSettings, action string) {
	p := leechEditPrompts[action]
	ext.SetPendingSettingSession(u.UserID, p.key, 0, msgID, 60*time.Second)
	var text string
	var isSet bool
	switch p.key {
	case "split_size":
		text, _ = m.buildSplitSizeMenu(u)
		isSet = u.SplitSize > 0
		p.ask += fmt.Sprintf("\n<b>Max:</b> <code>%s</code>", ext.GetReadableFileSize(config.GetMaxSplitSize()))
	case "lcaption":
		text, _ = m.buildCaptionMenu(u)
		isSet = u.Caption != ""
	case "lprefix":
		text, _ = m.buildPrefixMenu(u)
		isSet = u.Prefix != ""
	case "lsuffix":
		text, _ = m.buildSuffixMenu(u)
		isSet = u.Suffix != ""
	case "lremname_auto":
		text, _ = m.buildAutoRenameMenu(u)
		isSet = u.RemnameAuto != ""
	case "lremname_regex":
		text, _ = m.buildRegexMenu(u)
		isSet = u.RemnameRegex != ""
	case "ldump":
		text, _ = m.buildDumpMenu(u)
		isSet = u.DumpChatID != 0
	case "lexcluded":
		text, _ = m.buildExcludedMenu(u)
		isSet = u.ExcludedExts != ""
	case "lmeta":
		text, _ = m.buildMetadataMenu(u)
		isSet = u.Metadata != nil && u.Metadata["all"] != ""
	}
	btn := telegram.NewButtonMaker()
	btn.IButton("Stop Change", fmt.Sprintf("userset %d %s", u.UserID, p.page))
	if isSet {
		btn.IButton("↻ Delete", fmt.Sprintf("userset %d %s", u.UserID, p.del))
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d leech", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text+"\n\n"+p.ask+"\n<b>Timeout:</b> 60 sec", btn, 2)
	_ = m.sender.AnswerCallback(ctx, queryID, "", false)
}

func stdMenuButtons(btn *telegram.ButtonMaker, uid int64, name, editAction, delAction string, isSet bool) {
	label := "Set " + name
	if isSet {
		label = "Change " + name
	}
	btn.IButton(label, fmt.Sprintf("userset %d %s", uid, editAction))
	if isSet {
		btn.IButton("↻ Delete", fmt.Sprintf("userset %d %s", uid, delAction))
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d leech", uid), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", uid), "danger")
	btn.Row()
}

// buildDDLHostMenu is the page of one host: the user's key, Set / Change, ↻ Delete.
func (m *SettingsModule) buildDDLHostMenu(u *types.UserSettings, hostID string) (string, *telegram.ButtonMaker) {
	h, ok := ext.FindMirrorHost(hostID)
	btn := telegram.NewButtonMaker()
	if !ok {
		btn.IButtonStyled("◀️", fmt.Sprintf("userset %d mirror", u.UserID), "primary")
		return "Unknown host", btn
	}
	has := u.DDLKeys[h.ID] != ""
	keyText := "Not Exists"
	if has {
		keyText = "Exists"
	} else if h.Key(config.Get()) != "" {
		keyText = "Not Exists (the owner's key is used)"
	} else if h.NoKey {
		keyText = "Not needed"
	} else if !h.NeedsKey {
		keyText = "Not Exists (Guest upload)"
	}
	text := fmt.Sprintf("㊂ <b><u>%s Settings :</u></b>\n\n"+
		"➲ <b>%s's API Key :</b> <i>%s</i>\n\n"+
		"➲ <b>Description :</b> <i>%s</i>",
		h.Name, h.Name, keyText, telegram.EscapeHTML(h.Desc))
	label := "Set " + h.Name
	if has {
		label = "Change " + h.Name
	}
	usable := !h.NeedsKey || ext.HostKey(h, u.DDLKeys) != ""
	if u.MirrorHost == h.ID {
		btn.IButtonStyled("✓ Default", fmt.Sprintf("userset %d sethost none", u.UserID), "success")
		btn.Row()
	} else if usable {
		btn.IButton("Use as default", fmt.Sprintf("userset %d sethost %s", u.UserID, h.ID))
		btn.Row()
	}
	if !h.NoKey {
		btn.IButton(label, fmt.Sprintf("userset %d edit_ddlkey %s", u.UserID, h.ID))
		if has {
			btn.IButton("↻ Delete", fmt.Sprintf("userset %d del_ddlkey %s", u.UserID, h.ID))
		}
		btn.Row()
	}
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d mirror", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	return text, btn
}

func shotsText(n int) string {
	if n <= 0 {
		return "Disabled"
	}
	return fmt.Sprintf("%d per video", n)
}

func sampleText(sec int) string {
	if sec <= 0 {
		return "Disabled"
	}
	return fmt.Sprintf("%d sec", sec)
}

// buildChoiceMenu is a setting page with a few fixed values (0 = off).
func (m *SettingsModule) buildChoiceMenu(u *types.UserSettings, title, label, current, desc, setAction, _ string, value int, choices []int) (string, *telegram.ButtonMaker) {
	text := fmt.Sprintf("㊂ <b><u>%s Settings :</u></b>\n\n"+
		"➲ <b>%s :</b> <i>%s</i>\n\n"+
		"➲ <b>Description :</b> <i>%s</i>", title, label, current, desc)
	btn := telegram.NewButtonMaker()
	for i, c := range choices {
		name := strconv.Itoa(c)
		if c == 0 {
			name = "Off"
		} else if setAction == "setsample" {
			name += "s"
		}
		if c == value {
			btn.IButtonStyled("✓ "+name, fmt.Sprintf("userset %d %s %d", u.UserID, setAction, c), "success")
		} else {
			btn.IButton(name, fmt.Sprintf("userset %d %s %d", u.UserID, setAction, c))
		}
		if i%3 == 2 {
			btn.Row()
		}
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d leech", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	return text, btn
}

// buildUniversalMenu: settings for leech and mirror together, like the Python bot.
func (m *SettingsModule) buildUniversalMenu(u *types.UserSettings) (string, *telegram.ButtonMaker) {
	name := u.Name
	if name == "" {
		name = fmt.Sprintf("%d", u.UserID)
	}
	tasks, dailyBytes := ext.GetUserDailyStats(u.UserID)
	text := fmt.Sprintf("㊂ <b><u>Universal Settings : <a href=\"tg://user?id=%d\">%s</a></u></b>\n\n"+
		"<blockquote><b>╭</b> <b>Daily Tasks :</b> <code>%d</code> today\n"+
		"┠ <b>Daily Leech :</b> <code>%s</code> today\n"+
		"┠ <b>YT-DLP Options :</b> <i>%s</i>\n"+
		"┠ <b>YT-DLP Cookies :</b> <i>%s</i>\n"+
		"<b>╰</b> <b>MediaInfo Mode :</b> <i>%s</i></blockquote>",
		u.UserID, telegram.EscapeHTML(name), tasks, ext.GetReadableFileSize(dailyBytes), existsText(u.YtOpt != ""), existsText(u.YtCookies != ""), onOff(u.MediaInfoOn(config.Get().ShowMediaInfo)))
	btn := telegram.NewButtonMaker()
	if u.YtOpt != "" {
		btn.IButtonStyled("✓ YT-DLP Options", fmt.Sprintf("userset %d ytopt", u.UserID), "success")
	} else {
		btn.IButton("YT-DLP Options", fmt.Sprintf("userset %d ytopt", u.UserID))
	}
	if u.YtCookies != "" {
		btn.IButtonStyled("✓ YT-DLP Cookies", fmt.Sprintf("userset %d ycookies", u.UserID), "success")
	} else {
		btn.IButton("YT-DLP Cookies", fmt.Sprintf("userset %d ycookies", u.UserID))
	}
	btn.Row()
	if u.MegaEmail != "" {
		btn.IButtonStyled("✓ Mega Login", fmt.Sprintf("userset %d megalogin", u.UserID), "success")
	} else {
		btn.IButton("Mega Login", fmt.Sprintf("userset %d megalogin", u.UserID))
	}
	if config.Get().EnableFFmpegCmds {
		if len(u.FFmpegCommands) > 0 {
			btn.IButtonStyled("✓ FFmpeg Cmds", fmt.Sprintf("userset %d ffcmds", u.UserID), "success")
		} else {
			btn.IButton("FFmpeg Cmds", fmt.Sprintf("userset %d ffcmds", u.UserID))
		}
	}
	btn.Row()
	if u.MediaInfoOn(config.Get().ShowMediaInfo) {
		btn.IButtonStyled("✓ MediaInfo", fmt.Sprintf("userset %d mediainfo", u.UserID), "success")
	} else {
		btn.IButton("MediaInfo", fmt.Sprintf("userset %d mediainfo", u.UserID))
	}
	btn.IButton("Import/Export", fmt.Sprintf("userset %d iesettings", u.UserID))
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d main", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	return text, btn
}

func existsText(ok bool) string {
	if ok {
		return "Exists"
	}
	return "Not Exists"
}

// buildYtdlSettingMenu: YT-DLP Options / YT-DLP Cookies pages (Universal Settings).
func (m *SettingsModule) buildYtdlSettingMenu(u *types.UserSettings, key, note string) (string, *telegram.ButtonMaker) {
	btn := telegram.NewButtonMaker()
	var text string
	if key == "ycookies" {
		val := "Not Exists"
		if u.YtCookies != "" {
			val = fmt.Sprintf("Exists (%d lines)", strings.Count(u.YtCookies, "\n")+1)
		}
		text = "㊂ <b><u>YT-DLP Cookies :</u></b>\n\n" +
			"<blockquote><b>╭</b> <b>Cookies :</b> <i>" + val + "</i>\n" +
			"<b>╰</b> <b>Description :</b> <i>Your own cookies.txt for /ytdl (YouTube sign-in, age limits, private videos). Without it the bot's cookies.txt is used.</i></blockquote>"
		btn.IButton(map[bool]string{true: "Change Cookies", false: "Set Cookies"}[u.YtCookies != ""], fmt.Sprintf("userset %d edit_ycookies", u.UserID))
		if u.YtCookies != "" {
			btn.IButton("↻ Delete", fmt.Sprintf("userset %d del_ycookies", u.UserID))
		}
	} else {
		val := "Not Exists"
		if u.YtOpt != "" {
			val = "<code>" + telegram.EscapeHTML(u.YtOpt) + "</code>"
		}
		text = "㊂ <b><u>YT-DLP Options :</u></b>\n\n" +
			"<blockquote><b>╭</b> <b>Options :</b> " + val + "\n" +
			"<b>╰</b> <b>Description :</b> <i>Extra yt-dlp options added to every /ytdl task.</i></blockquote>"
		btn.IButton(map[bool]string{true: "Change Options", false: "Set Options"}[u.YtOpt != ""], fmt.Sprintf("userset %d edit_ytopt", u.UserID))
		if u.YtOpt != "" {
			btn.IButton("↻ Delete", fmt.Sprintf("userset %d del_ytopt", u.UserID))
		}
	}
	if note != "" {
		text += "\n\n" + note
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d universal", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	return text, btn
}

// buildExtraMenu: Mega Login and FFmpeg Cmds pages (Universal Settings).
func (m *SettingsModule) buildExtraMenu(u *types.UserSettings, key, note string) (string, *telegram.ButtonMaker) {
	btn := telegram.NewButtonMaker()
	var text string
	if key == "megalogin" {
		val := "Not Exists"
		if u.MegaEmail != "" {
			val = "<code>" + telegram.EscapeHTML(u.MegaEmail) + "</code>"
		}
		text = "㊂ <b><u>Mega Login :</u></b>\n\n" +
			"<blockquote><b>╭</b> <b>Account :</b> " + val + "\n" +
			"<b>╰</b> <b>Description :</b> <i>Your MEGA account for your Mega downloads: the quota is your account's, not the server's (Pro accounts are much bigger). Accounts with 2FA are not supported.</i></blockquote>"
		btn.IButton(map[bool]string{true: "Change Login", false: "Set Login"}[u.MegaEmail != ""], fmt.Sprintf("userset %d edit_megalogin", u.UserID))
		if u.MegaEmail != "" {
			btn.IButton("↻ Delete", fmt.Sprintf("userset %d del_megalogin", u.UserID))
		}
	} else {
		var lines []string
		names := make([]string, 0, len(u.FFmpegCommands))
		for n := range u.FFmpegCommands {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			lines = append(lines, fmt.Sprintf("┠ <code>%s</code>: <code>%s</code>", telegram.EscapeHTML(n), telegram.EscapeHTML(u.FFmpegCommands[n])))
		}
		list := "┠ <i>None</i>"
		if len(lines) > 0 {
			list = strings.Join(lines, "\n")
		}
		text = "㊂ <b><u>FFmpeg Cmds :</u></b>\n\n<blockquote><b>╭</b> <b>Saved :</b>\n" + list + "\n" +
			"<b>╰</b> <b>Use :</b> <code>" + telegram.Cmd("l") + " link -ff name</code></blockquote>"
		btn.IButton("Add Command", fmt.Sprintf("userset %d edit_ffcmds", u.UserID))
		if len(names) > 0 {
			btn.IButton("↻ Delete All", fmt.Sprintf("userset %d del_ffcmds", u.UserID))
		}
	}
	if note != "" {
		text += "\n\n" + note
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d universal", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	return text, btn
}

// thumbNotice: the result of /t, deleted with the /t command after 5 seconds.
func (m *SettingsModule) thumbNotice(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, text string) {
	id, _ := m.sender.SendMessage(ctx, peer, "㊂ <b><u>Custom Thumbnail :</u></b>\n\n"+text, msg.MessageID, nil)
	ids := []int{msg.MessageID}
	if id > 0 {
		ids = append([]int{id}, ids...)
	}
	go func() {
		time.Sleep(5 * time.Second)
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.sender.DeleteMessage(c, peer, ids)
	}()
}

// isImageFile: JPEG, PNG or WEBP by its first bytes.
func isImageFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := make([]byte, 12)
	n, _ := f.Read(h)
	h = h[:n]
	return bytes.HasPrefix(h, []byte{0xFF, 0xD8, 0xFF}) || bytes.HasPrefix(h, []byte("\x89PNG")) ||
		(len(h) >= 12 && string(h[:4]) == "RIFF" && string(h[8:12]) == "WEBP")
}
