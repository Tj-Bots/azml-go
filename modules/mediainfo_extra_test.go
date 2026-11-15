package modules

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestTgDocument(t *testing.T) {
	doc := &tg.Document{ID: 1, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "mix.video.mp4"}}}
	got := tgDocument(&tg.MessageMediaDocument{Document: doc})
	if got == nil || docFileName(got) != "mix.video.mp4" {
		t.Fatal("document not found")
	}
	if tgDocument(&tg.MessageMediaWebPage{}) != nil || tgDocument(nil) != nil {
		t.Fatal("not a file")
	}
}
