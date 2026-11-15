package telegram

import "testing"

func TestEveryCommandIsKnown(t *testing.T) {
	InitCommands()
	for _, cmd := range []string{"/mirror", "/m", "/users", "/authlist", "/sudolist", "/bl", "/rbl", "/l", "/us", "/bs", "/log"} {
		if !BotCommands.IsAnyCommand(cmd + " x") {
			t.Errorf("%s is not routed", cmd)
		}
	}
}
