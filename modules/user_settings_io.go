package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// UserSettingsFileName: the file /us Export sends and Import takes back, so a user can move
// his settings to another bot (several bots in one group).
const UserSettingsFileName = "azml_user_settings.json"

// userPrefs: the settings a user chose in /us. Not included: what the bot decides about him
// (auth, sudo, premium, blacklist, tokens), his history and the topics of this group.
type userPrefs struct {
	Prefix         string            `json:"prefix,omitempty"`
	Suffix         string            `json:"suffix,omitempty"`
	RemnameAuto    string            `json:"remname_auto,omitempty"`
	RemnameRegex   string            `json:"remname_regex,omitempty"`
	RenameMethod   string            `json:"rename_method,omitempty"`
	Autorename     bool              `json:"autorename,omitempty"`
	Caption        string            `json:"caption,omitempty"`
	CapFont        string            `json:"cap_font,omitempty"`
	SplitSize      int64             `json:"split_size,omitempty"`
	EqualSplits    bool              `json:"equal_splits,omitempty"`
	MediaGroup     bool              `json:"media_group,omitempty"`
	AsDoc          bool              `json:"as_doc"`
	DumpChatID     int64             `json:"dump_chat_id,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	FFmpegCommands map[string]string `json:"ffmpeg_cmds,omitempty"`
	AutoThumb      bool              `json:"auto_thumb,omitempty"`
	AutoThumbSet   bool              `json:"auto_thumb_set,omitempty"`
	BotPM          bool              `json:"bot_pm,omitempty"`
	MirrorHost     string            `json:"mirror_host,omitempty"`
	ExcludedExts   string            `json:"excluded_exts,omitempty"`
	MediaInfo      bool              `json:"mediainfo,omitempty"`
	MediaInfoSet   bool              `json:"mediainfo_set,omitempty"`
	MediaStore     bool              `json:"media_store,omitempty"`
	MediaStoreSet  bool              `json:"media_store_set,omitempty"`
	Screenshots    int               `json:"screenshots,omitempty"`
	SampleSec      int               `json:"sample_sec,omitempty"`
	YtOpt          string            `json:"yt_opt,omitempty"`
	YtCookies      string            `json:"yt_cookies,omitempty"`
	MegaEmail      string            `json:"mega_email,omitempty"`
	MegaPass       string            `json:"mega_pass,omitempty"`
	DDLKeys        map[string]string `json:"ddl_keys,omitempty"`
	Thumb          []byte            `json:"thumb,omitempty"` // the thumbnail image itself
}

type userSettingsFile struct {
	Bot      string    `json:"bot"`
	Kind     string    `json:"kind"`
	Exported string    `json:"exported"`
	Settings userPrefs `json:"settings"`
}

func prefsOf(u *types.UserSettings) userPrefs {
	p := userPrefs{
		Prefix: u.Prefix, Suffix: u.Suffix, RemnameAuto: u.RemnameAuto, RemnameRegex: u.RemnameRegex,
		RenameMethod: u.RenameMethod, Autorename: u.Autorename, Caption: u.Caption, CapFont: u.CapFont,
		SplitSize: u.SplitSize, EqualSplits: u.EqualSplits, MediaGroup: u.MediaGroup, AsDoc: u.AsDoc,
		DumpChatID: u.DumpChatID, Metadata: u.Metadata, FFmpegCommands: u.FFmpegCommands,
		AutoThumb: u.AutoThumb, AutoThumbSet: u.AutoThumbSet, BotPM: u.BotPM, MirrorHost: u.MirrorHost,
		ExcludedExts: u.ExcludedExts, MediaInfo: u.MediaInfo, MediaInfoSet: u.MediaInfoSet,
		MediaStore: u.MediaStore, MediaStoreSet: u.MediaStoreSet, Screenshots: u.Screenshots,
		SampleSec: u.SampleSec, YtOpt: u.YtOpt, YtCookies: u.YtCookies, MegaEmail: u.MegaEmail,
		MegaPass: u.MegaPass, DDLKeys: u.DDLKeys,
	}
	if u.CustomThumb != "" {
		if b := u.ThumbData; len(b) > 0 {
			p.Thumb = b
		} else if b, err := os.ReadFile(u.CustomThumb); err == nil {
			p.Thumb = b
		}
	}
	return p
}

func applyPrefs(u *types.UserSettings, p userPrefs) {
	u.Prefix, u.Suffix, u.RemnameAuto, u.RemnameRegex = p.Prefix, p.Suffix, p.RemnameAuto, p.RemnameRegex
	u.RenameMethod, u.Autorename, u.Caption, u.CapFont = p.RenameMethod, p.Autorename, p.Caption, p.CapFont
	u.SplitSize, u.EqualSplits, u.MediaGroup, u.AsDoc = p.SplitSize, p.EqualSplits, p.MediaGroup, p.AsDoc
	u.DumpChatID, u.Metadata, u.FFmpegCommands = p.DumpChatID, p.Metadata, p.FFmpegCommands
	u.AutoThumb, u.AutoThumbSet, u.BotPM, u.MirrorHost = p.AutoThumb, p.AutoThumbSet, p.BotPM, p.MirrorHost
	u.ExcludedExts, u.MediaInfo, u.MediaInfoSet = p.ExcludedExts, p.MediaInfo, p.MediaInfoSet
	u.MediaStore, u.MediaStoreSet, u.Screenshots, u.SampleSec = p.MediaStore, p.MediaStoreSet, p.Screenshots, p.SampleSec
	u.YtOpt, u.YtCookies, u.MegaEmail, u.MegaPass, u.DDLKeys = p.YtOpt, p.YtCookies, p.MegaEmail, p.MegaPass, p.DDLKeys
	if len(p.Thumb) > 0 {
		dest := fmt.Sprintf("Thumbnails/%d.jpg", u.UserID)
		_ = os.MkdirAll(filepath.Dir(dest), 0o755)
		if err := os.WriteFile(dest, p.Thumb, 0o644); err == nil {
			u.CustomThumb = dest
			u.ThumbData = p.Thumb
		}
	}
}

func (m *SettingsModule) sendUserExport(ctx context.Context, peer tg.InputPeerClass, replyTo int, u *types.UserSettings) error {
	data, _ := json.MarshalIndent(userSettingsFile{
		Bot:      "azml-go",
		Kind:     "user",
		Exported: time.Now().Format("2006-01-02 15:04:05 MST"),
		Settings: prefsOf(u),
	}, "", "  ")
	caption := "⌬ <b>User Settings Export</b>\n\n" +
		"<i>Prefix, suffix, caption, rename, thumbnail, dump channel, split, metadata, mirror host, keys and the rest of /us.</i>\n\n" +
		"<i>To import: /us → Import/Export Settings → Import, then send this file.</i>\n" +
		"⚠️ <i>The file has your cookies and API keys: do not share it.</i>"
	_, err := m.sender.SendDocumentBytes(ctx, peer, UserSettingsFileName, data, caption, replyTo, nil)
	return err
}

// buildIEMenu: /us → Import/Export Settings.
func (m *SettingsModule) buildIEMenu(u *types.UserSettings, note string) (string, *telegram.ButtonMaker) {
	text := "㊂ <b><u>Import/Export Settings :</u></b>\n\n" +
		"<blockquote><b>╭</b> <b>Export :</b> <i>a file with your /us settings (thumbnail included), sent here.</i>\n" +
		"<b>╰</b> <b>Import :</b> <i>send that file to this bot (or another one) and the settings come back.</i></blockquote>"
	if note != "" {
		text += "\n\n" + note
	}
	btn := telegram.NewButtonMaker()
	btn.IButton("📤 Export", fmt.Sprintf("userset %d export", u.UserID))
	btn.IButton("📥 Import", fmt.Sprintf("userset %d edit_importset", u.UserID))
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("userset %d back", u.UserID), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("userset %d close", u.UserID), "danger")
	btn.Row()
	return text, btn
}

// ImportUserSettings: the file of /us Export, applied to the user who sent it; the prompt
// message goes back to the Import/Export menu with the result.
func (m *SettingsModule) ImportUserSettings(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, userID int64, data []byte) {
	u := m.userMap.GetOrCreate(userID)
	var f userSettingsFile
	note := "✓ <i>Settings imported.</i>"
	if err := json.Unmarshal(data, &f); err != nil || f.Kind != "user" {
		note = "× <i>This is not a file of /us Export.</i>"
	} else {
		applyPrefs(u, f.Settings)
		_ = ext.DB.SaveUser(u)
	}
	text, btn := m.buildIEMenu(u, note)
	if promptMsgID > 0 {
		_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
	} else {
		_, _ = m.sender.SendMessageWithButtons(ctx, peer, text, 0, btn, 2)
	}
}
