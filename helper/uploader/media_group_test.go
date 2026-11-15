package uploader

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestSentMessageIDs(t *testing.T) {
	res := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateMessageID{ID: 5},
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 11}},
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 12}},
	}}
	ids := sentMessageIDs(res)
	if len(ids) != 2 || ids[0] != 11 || ids[1] != 12 {
		t.Fatalf("got %v", ids)
	}
}

func TestAlbumChunks(t *testing.T) {
	var parts []UploadResult
	for i := 0; i < 12; i++ {
		parts = append(parts, UploadResult{ChatID: -100, MsgID: i + 1})
	}
	parts = append(parts, UploadResult{ChatID: -200, MsgID: 50})
	c := albumChunks(parts)
	if len(c) != 3 || len(c[0]) != 10 || len(c[1]) != 2 || c[2][0].ChatID != -200 {
		t.Fatalf("chunks: %d", len(c))
	}
}
