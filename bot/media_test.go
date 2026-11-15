package bot

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestIsFileMedia(t *testing.T) {
	if isFileMedia(nil) || isFileMedia(&tg.MessageMediaEmpty{}) || isFileMedia(&tg.MessageMediaWebPage{}) {
		t.Fatal("a link preview is not a file")
	}
	if !isFileMedia(&tg.MessageMediaDocument{}) || !isFileMedia(&tg.MessageMediaPhoto{}) {
		t.Fatal("documents and photos are files")
	}
}
