package modules

import (
	"strings"
	"testing"
)

func TestRichStatus(t *testing.T) {
	plain := "<blockquote>╭ <b>Name:</b> <code>a</code>\n┠ x\n╰ /cancel2_abc123</blockquote>\n\n<blockquote>╭ b\n╰ /cancel_zz9</blockquote>\n\n⌬ <b><i>Bot Stats</i></b>"
	r := richStatus(plain)
	if !strings.Contains(r, `data="status cancel abc123"`) || !strings.Contains(r, `data="status cancel zz9"`) {
		t.Fatalf("buttons missing: %s", r)
	}
	if strings.Contains(r, "/cancel") || strings.Count(r, "<hr/>") != 1 || strings.Contains(r, "\n") {
		t.Fatalf("bad rich: %s", r)
	}
}
