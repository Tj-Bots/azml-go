package modules

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"
)

func styleMark(s string) string {
	switch s {
	case "primary":
		return "🟦"
	case "danger":
		return "🟥"
	case "success":
		return "🟩"
	}
	return "⬜"
}

func render(name, text string, btn *telegram.ButtonMaker, cols int) string {
	var sb strings.Builder
	plain, ents := telegram.ParseFormatting(text)
	ok := "OK"
	if plain == text && strings.Contains(text, "<") {
		ok = "HTML-PARSE-FAILED"
	}
	sb.WriteString(fmt.Sprintf("==== %s [%s, %d entities]\n%s\n", name, ok, len(ents), plain))
	if btn != nil {
		for _, row := range btn.Rows(cols) {
			var cells []string
			for _, b := range row {
				cells = append(cells, styleMark(b.Style)+" "+b.Text)
			}
			sb.WriteString("  | " + strings.Join(cells, " | ") + "\n")
		}
	}
	return sb.String()
}

func TestRenderAllMenus(t *testing.T) {
	var out strings.Builder
	sm := &SettingsModule{}
	for _, u := range []*types.UserSettings{
		{UserID: 1, AsDoc: true, Autorename: true, RenameMethod: "auto"},
		{UserID: 1, Prefix: "[Tj] <x>", Caption: "cap", AutoThumb: true, SplitSize: 1 << 30},
	} {
		tx, b := sm.buildLeechMenu(u, "Boss")
		out.WriteString(render("us main", tx, b, 2))
		for name, fn := range map[string]func(*types.UserSettings) (string, *telegram.ButtonMaker){
			"split": sm.buildSplitSizeMenu, "caption": sm.buildCaptionMenu, "prefix": sm.buildPrefixMenu,
			"suffix": sm.buildSuffixMenu, "autorename": sm.buildAutoRenameMenu, "regex": sm.buildRegexMenu,
			"dump": sm.buildDumpMenu, "metadata": sm.buildMetadataMenu,
		} {
			tx, b := fn(u)
			out.WriteString(render("us "+name, tx, b, 2))
		}
		tx, b = sm.buildThumbnailMenu(u)
		out.WriteString(render("us thumb", tx, b, 2))
	}
	bm := &BotSettingsModule{}
	tx, b := bm.buildMainMenu()
	out.WriteString(render("bs main", tx, b, 1))
	tx, b = bm.buildVarsMenu(0)
	out.WriteString(render("bs vars", tx, b, 2))
	tx, b = bm.buildVarDetailMenu("STATUS_LIMIT", 0, false)
	out.WriteString(render("bs var", tx, b, 2))
	tx, b = bm.buildVarDetailMenu("AS_DOCUMENT", 0, false)
	out.WriteString(render("bs boolvar", tx, b, 2))
	tx, b = bm.buildAriaMenu()
	out.WriteString(render("bs aria", tx, b, 2))
	tx, b = bm.buildPrivateMenu()
	out.WriteString(render("bs private", tx, b, 2))
	tx, b = bm.buildShortenerMenu()
	out.WriteString(render("bs shortener", tx, b, 2))
	tx, b = bm.buildSessionMenu()
	out.WriteString(render("bs session", tx, b, 2))
	hm := &HelpModule{}
	out.WriteString(render("start", hm.getStartText(), hm.getStartButtons(), 2))
	out.WriteString(render("help", hm.getHelpGuideText(), nil, 1))
	for _, k := range []string{"home", "stbot", "stsys", "strepo", "botlimits"} {
		tx, b := hm.getStatsView(k, 1)
		out.WriteString(render("stats "+k, tx, b, 2))
	}
	tm := ext.GetTaskManager()
	tx, b = tm.BuildStatusMessage(1)
	out.WriteString(render("status empty", tx, b, 3))
	if strings.Contains(out.String(), "HTML-PARSE-FAILED") {
		t.Errorf("a menu has broken HTML:\n%s", out.String())
	}
	if p := os.Getenv("RENDER_OUT"); p != "" {
		_ = os.WriteFile(p, []byte(out.String()), 0o644)
	}
}
