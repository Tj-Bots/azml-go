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
