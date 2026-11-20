package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"azml-go/config"
	"azml-go/helper/telegram"

	"github.com/gotd/td/tg"
)

// SettingsFileName: the file /bs Export sends and Import takes back.
const SettingsFileName = "azml_settings.json"

// ImportSettingsKey: the /bs session that waits for that file.
const ImportSettingsKey = "IMPORT_SETTINGS"

// noExportKeys: what belongs to this deploy / this bot and must not move to another one:
// the deploy variables (token, API, owner, database), the helper bot tokens, the user session
// and the address of this app.
var noExportKeys = map[string]bool{
	"BOT_TOKENS":          true,
	"USER_SESSION_STRING": true,
	"BASE_URL":            true,
	"BASE_URL_PORT":       true,
	"PORT":                true,
}

func exportableKey(key string) bool {
	return key != "" && !strings.HasPrefix(key, "_") && !config.IsCoreKey(key) && !noExportKeys[key]
}

type settingsFile struct {
	Bot      string            `json:"bot"`
	Version  string            `json:"version"`
	Exported string            `json:"exported"`
	Settings map[string]string `json:"settings"`
}

func varDefault(key string) string {
	if v, ok := config.DefaultValue(key); ok {
		return v
	}
	return defaultVarValues[key]
}

// exportSettings: every variable that is not its default, without the deploy ones.
func exportSettings(version string) ([]byte, int) {
	cfgMap := config.GetConfigMap()
	out := map[string]string{}
	for _, k := range config.AllKeys() {
		if !exportableKey(k) {
			continue
		}
		v := strings.TrimSpace(configValue(cfgMap, k))
		if v == strings.TrimSpace(varDefault(k)) {
			continue
		}
		out[k] = v
	}
	data, _ := json.MarshalIndent(settingsFile{
		Bot:      "azml-go",
		Version:  version,
		Exported: time.Now().Format("2006-01-02 15:04:05 MST"),
		Settings: out,
	}, "", "  ")
	return data, len(out)
}

func (m *BotSettingsModule) sendExport(ctx context.Context, peer tg.InputPeerClass, replyTo int, version string) error {
	data, n := exportSettings(version)
	caption := fmt.Sprintf("⌬ <b>Bot Settings Export</b>\n\n"+
		"┠ <b>Variables:</b> %d (only the changed ones)\n"+
		"╰ <i>Not included: BOT_TOKEN, OWNER_ID, TELEGRAM_API, TELEGRAM_HASH, DATABASE_URL, DATABASE_NAME, "+
		"BOT_TOKENS, USER_SESSION_STRING, BASE_URL</i>\n\n"+
		"<i>To import: /bs → Import/Export Settings → Import, then send this file.</i>", n)
	_, err := m.sender.SendDocumentBytes(ctx, peer, SettingsFileName, data, caption, replyTo, nil)
	return err
}

// ImportSettings: the file of /bs Export. Only known variables that may move between bots;
// each one is saved to the database like an edit in /bs.
func (m *BotSettingsModule) ImportSettings(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, data []byte) {
	var f settingsFile
	if err := json.Unmarshal(data, &f); err != nil || f.Settings == nil {
		text, btn := m.buildIEMenu("× <i>This is not a settings file of /bs Export.</i>")
		_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
		return
	}
	known := map[string]bool{}
	for _, k := range config.AllKeys() {
		known[k] = true
	}
	keys := make([]string, 0, len(f.Settings))
	for k := range f.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var applied, skipped, needRestart []string
	dumpChanged := false
	for _, k := range keys {
		key := strings.ToUpper(strings.TrimSpace(k))
		if !known[key] || !exportableKey(key) {
			skipped = append(skipped, key)
			continue
		}
		if err := config.UpdateConfigVar(key, f.Settings[k]); err != nil {
			skipped = append(skipped, key)
			continue
		}
		applied = append(applied, key)
		if key == "DUMP_CHAT_ID" || key == "LEECH_LOG_ID" {
			dumpChanged = true
		}
		if restartOnlyVar(key) {
			needRestart = append(needRestart, key)
		}
	}
	if dumpChanged { // one channel (see config.syncLogDump)
		dID := config.Get().DumpChatID
		if m.uploader != nil {
			m.uploader.SetDumpChatID(dID)
		}
		if m.leechMod != nil {
			m.leechMod.SetDumpChatID(dID)
		}
	}

	var sb strings.Builder
	sb.WriteString("✓ <b>Imported</b>\n")
	sb.WriteString(fmt.Sprintf("┠ <b>Applied:</b> %d\n", len(applied)))
	if len(skipped) > 0 {
		sb.WriteString(fmt.Sprintf("┠ <b>Skipped:</b> <code>%s</code>\n", telegram.EscapeHTML(strings.Join(skipped, " "))))
	}
	if len(needRestart) > 0 {
		sb.WriteString(fmt.Sprintf("╰ <b>Apply after /restart:</b> <code>%s</code>", strings.Join(needRestart, " ")))
	} else {
		sb.WriteString("╰ <i>Everything applies at once.</i>")
	}
	text, btn := m.buildIEMenu(sb.String())
	_ = m.sender.EditMessageWithButtons(ctx, peer, promptMsgID, text, btn, 2)
}

// buildIEMenu: /bs → Import/Export Settings.
func (m *BotSettingsModule) buildIEMenu(note string) (string, *telegram.ButtonMaker) {
	text := "㊂ <b><u>Import/Export Settings :</u></b>\n\n" +
		"<blockquote><b>╭</b> <b>Export :</b> <i>a file with every changed variable, sent here (PM only).</i>\n" +
		"<b>┠</b> <b>Import :</b> <i>send that file and the variables are set and saved.</i>\n" +
		"<b>╰</b> <b>Never included :</b> <i>BOT_TOKEN, OWNER_ID, TELEGRAM_API, TELEGRAM_HASH, DATABASE_URL, DATABASE_NAME, BOT_TOKENS, USER_SESSION_STRING, BASE_URL</i></blockquote>"
	if note != "" {
		text += "\n\n" + note
	}
	btn := telegram.NewButtonMaker()
	btn.IButton("📤 Export", "botset export")
	btn.IButton("📥 Import", "botset prompt_import")
	btn.Row()
	btn.IButtonStyled("◀️", "botset back", "primary")
	btn.IButtonStyled("❌", "botset close", "danger")
	btn.Row()
	return text, btn
}
