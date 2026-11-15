package uploader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"azml-go/config"
	"azml-go/helper/telegram"

	"github.com/gotd/td/tg"
)

// botPeer builds the main bot's peer for a chat id.
func (u *TelegramUploader) botPeer(ctx context.Context, chatID int64, userAccessHash int64) tg.InputPeerClass {
	if chatID < 0 {
		chID := getChannelID(chatID)
		hash := telegram.ResolveChannelAccessHash(ctx, u.botClient, chatID)
		if hash == 0 {
			hash = telegram.GetChannelAccessHash(chID)
		}
		return &tg.InputPeerChannel{ChannelID: chID, AccessHash: hash}
	}
	if userAccessHash == 0 {
		userAccessHash = telegram.GetUserAccessHash(chatID)
	}
	return &tg.InputPeerUser{UserID: chatID, AccessHash: userAccessHash}
}

// sentMessageIDs collects the ids of the messages created by a send request, in order.
func sentMessageIDs(res tg.UpdatesClass) []int {
	var ids []int
	add := func(list []tg.UpdateClass) {
		for _, upd := range list {
			switch v := upd.(type) {
			case *tg.UpdateNewChannelMessage:
				if m, ok := v.Message.(*tg.Message); ok {
					ids = append(ids, m.ID)
				}
			case *tg.UpdateNewMessage:
				if m, ok := v.Message.(*tg.Message); ok {
					ids = append(ids, m.ID)
				}
			}
		}
	}
	switch r := res.(type) {
	case *tg.Updates:
		add(r.Updates)
	case *tg.UpdatesCombined:
		add(r.Updates)
	}
	return ids
}

func copyMessagesViaBotAPI(botToken string, toChatID, fromChatID int64, msgIDs []int) bool {
	if botToken == "" || toChatID == 0 || fromChatID == 0 || len(msgIDs) == 0 {
		return false
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"chat_id":      toChatID,
		"from_chat_id": fromChatID,
		"message_ids":  msgIDs,
	})
	resp, err := botAPICopyClient.Post(fmt.Sprintf("https://api.telegram.org/bot%s/copyMessages", botToken), "application/json", bytes.NewReader(payload))
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	var res struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if json.NewDecoder(resp.Body).Decode(&res) == nil && res.Ok {
		return true
	}
	log.Printf("[Media Group] copyMessages to %d failed: %s", toChatID, res.Description)
	return false
}

// SendAlbum turns the uploaded parts of one file into a media group (up to 10 per album),
// like the Python bot: the single part messages are replaced by the album, then the album
// is copied to the user's PM, the user dump and the leech log.
func (u *TelegramUploader) SendAlbum(ctx context.Context, parts []UploadResult, opt UploadOptions) {
	if len(parts) == 0 {
		return
	}
	cfg := config.Get()
	botToken := cfg.BotToken
	if botToken == "" {
		botToken = os.Getenv("BOT_TOKEN")
	}
	logChat := opt.LogChatID
	if logChat == 0 {
		logChat = cfg.LeechLogID
	}

	for start := 0; start < len(parts); start += 10 {
		end := start + 10
		if end > len(parts) {
			end = len(parts)
		}
		chunk := parts[start:end]
		destChat := chunk[0].ChatID
		ids := make([]int, 0, len(chunk))
		for _, p := range chunk {
			ids = append(ids, p.MsgID)
		}

		albumIDs := u.makeAlbum(ctx, destChat, chunk, opt.UserAccessHash)
		deliverIDs := ids
		if len(albumIDs) > 0 {
			deliverIDs = albumIDs
			for _, id := range ids {
				if !deleteMessageViaBotAPI(botToken, destChat, id) {
					log.Printf("[Media Group] could not delete part message %d in %d", id, destChat)
				}
			}
		} else {
			log.Printf("[Media Group] album failed in %d, delivering the parts one by one", destChat)
		}

		sent := map[int64]bool{destChat: true}
		for _, target := range []int64{opt.DumpChatID, opt.UserID, logChat} {
			if target == 0 || sent[target] {
				continue
			}
			sent[target] = true
			if copyMessagesViaBotAPI(botToken, target, destChat, deliverIDs) {
				continue
			}
			// fallback: forward (keeps the album when all ids go together)
			randomIDs := make([]int64, len(deliverIDs))
			for i := range randomIDs {
				randomIDs[i] = time.Now().UnixNano() + int64(i)
			}
			if _, err := u.botClient.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
				FromPeer:   u.botPeer(ctx, destChat, opt.UserAccessHash),
				ToPeer:     u.botPeer(ctx, target, opt.UserAccessHash),
				ID:         deliverIDs,
				RandomID:   randomIDs,
				DropAuthor: true,
			}); err != nil {
				log.Printf("[Media Group] delivery to %d failed: %v", target, err)
			}
		}
	}
}

// makeAlbum sends the parts again as one media group in destChat and returns the new message ids.
func (u *TelegramUploader) makeAlbum(ctx context.Context, destChat int64, chunk []UploadResult, userAccessHash int64) []int {
	if len(chunk) < 2 {
		return nil
	}
	peer := u.botPeer(ctx, destChat, userAccessHash)
	media := make([]tg.InputSingleMedia, 0, len(chunk))
	for i, p := range chunk {
		msg, err := telegram.FetchMessage(ctx, u.botClient, peer, p.MsgID)
		if err != nil || msg == nil {
			return nil
		}
		md, ok := msg.Media.(*tg.MessageMediaDocument)
		if !ok {
			return nil
		}
		doc, ok := md.Document.(*tg.Document)
		if !ok {
			return nil
		}
		media = append(media, tg.InputSingleMedia{
			Media:    &tg.InputMediaDocument{ID: &tg.InputDocument{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}},
			RandomID: time.Now().UnixNano() + int64(i),
			Message:  msg.Message,
			Entities: msg.Entities,
		})
	}
	res, err := u.botClient.MessagesSendMultiMedia(ctx, &tg.MessagesSendMultiMediaRequest{Peer: peer, MultiMedia: media})
	if err != nil {
		log.Printf("[Media Group] sendMultiMedia failed in %d: %v", destChat, err)
		return nil
	}
	return sentMessageIDs(res)
}
