package modules

import (
	"strings"
	"testing"
	"unicode/utf8"

	"azml-go/helper/telegram"
)

func TestFinishFileList(t *testing.T) {
	var names, links []string
	for i := 0; i < 5; i++ {
		names = append(names, "File <"+string(rune('a'+i))+">.mkv")
		links = append(links, "https://t.me/c/123/1")
	}
	out := finishFileList(names, links)
	if strings.Count(out, "<blockquote expandable>") != 1 || strings.Count(out, "<a href") != 5 {
		t.Fatalf("not one quote with 5 links: %s", out)
	}
	if plain, ents := telegram.ParseFormatting(out); plain == out || len(ents) < 5 {
		t.Fatalf("bad html: %s", out)
	}
	// a long list still fits a message
	names, links = nil, nil
	for i := 0; i < 200; i++ {
		names = append(names, strings.Repeat("x", 130))
		links = append(links, "https://t.me/c/1234567890/12345")
	}
	plain, _ := telegram.ParseFormatting(finishFileList(names, links))
	if utf8.RuneCountInString(plain) > 3800 {
		t.Fatalf("too long: %d", utf8.RuneCountInString(plain))
	}
}
