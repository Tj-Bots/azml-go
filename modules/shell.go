package modules

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// HandleShell: /shell <command> (also /exec), owner only, like the Python bot. Runs it with
// sh -c in the bot folder (5 minutes at most); a long output comes as shell.txt.
func HandleShell(sender *telegram.MessageSender) func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	return func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
		if !telegram.IsOwner(msg.UserID) {
			return // not even an answer: nobody else should know it exists
		}
		cmdLine := ""
		if parts := strings.SplitN(strings.TrimSpace(msg.Text), " ", 2); len(parts) == 2 {
			cmdLine = strings.TrimSpace(parts[1])
		}
		if cmdLine == "" {
			_, _ = sender.SendMessage(ctx, peer, "<i>Usage:</i> <code>/shell ls -la</code>", msg.MessageID, telegram.CloseMarkup(msg.UserID))
			return
		}
		go func() {
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			var out bytes.Buffer
			c := exec.CommandContext(rctx, "sh", "-c", cmdLine)
			c.Stdout, c.Stderr = &out, &out
			err := c.Run()
			res := strings.TrimSpace(out.String())
			if err != nil {
				res = strings.TrimSpace(res + "\n\n" + err.Error())
			}
			if res == "" {
				res = "(no output)"
			}
			sctx, scancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer scancel()
			if len(res) > 3500 {
				_, _ = sender.SendDocumentBytes(sctx, peer, "shell.txt", []byte(res), "<code>"+telegram.EscapeHTML(cmdLine)+"</code>", msg.MessageID, telegram.CloseMarkup(msg.UserID))
				return
			}
			_, _ = sender.SendMessage(sctx, peer, "<b>$</b> <code>"+telegram.EscapeHTML(cmdLine)+"</code>\n\n<pre>"+telegram.EscapeHTML(res)+"</pre>", msg.MessageID, telegram.CloseMarkup(msg.UserID))
		}()
	}
}
