package telegram

import (
	"strings"
	"testing"
)

func TestEveryCommandIsKnown(t *testing.T) {
	InitCommands()
	for _, cmd := range []string{"/mirror", "/m", "/users", "/authlist", "/sudolist", "/bl", "/rbl", "/l", "/us", "/bs", "/log"} {
		if !BotCommands.IsAnyCommand(cmd + " x") {
			t.Errorf("%s is not routed", cmd)
		}
	}
}

func TestBotAPIKeyboardKeepsColors(t *testing.T) {
	btn := NewButtonMaker()
	btn.IButtonStyled("Back", "a", "primary")
	btn.IButtonStyled("Close", "b", "danger")
	btn.Row()
	raw, err := buildBotAPIKeyboard(btn.BuildMenu(2))
	if err != nil || !strings.Contains(string(raw), `"style":"primary"`) || !strings.Contains(string(raw), `"style":"danger"`) {
		t.Fatalf("styles lost: %s %v", raw, err)
	}
}
