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

func TestStoreLink(t *testing.T) {
	telegram.CurrentBotUsername = "TestBot"
	l := storeLink(-1002472959232, 345)
	arg := strings.TrimPrefix(l, "https://t.me/TestBot?start=")
	if len(arg) > 64 {
		t.Fatal("start payload too long")
	}
	c, m, ok := parseStoreArg(arg)
	if !ok || c != -1002472959232 || m != 345 {
		t.Fatalf("%d %d %v", c, m, ok)
	}
	if c2, m2, ok := parseChannelLink("https://t.me/c/2472959232/345"); !ok || c2 != -1002472959232 || m2 != 345 {
		t.Fatalf("channel link %d %d", c2, m2)
	}
	out := finishFileList(withStoreLinks([]string{"a.mkv"}, []string{"https://t.me/c/2472959232/345"}), []string{"https://t.me/c/2472959232/345"})
	if !strings.Contains(out, ">Store</a>") || strings.Contains(out, "\x00") {
		t.Fatalf("%s", out)
	}
}
