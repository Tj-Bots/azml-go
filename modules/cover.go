package modules

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// HandleCover: /cover in reply to a video. Like the Python bot: the video is sent again (by
// its file, no download, no upload) to the user's PM with his thumbnail as the video cover;
// without a thumbnail the TMDB poster when Auto Thumbnail is on.
func (m *LeechModule) HandleCover(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if msgText, btn := ext.CheckingAccess(msg.UserID, false, telegram.CurrentBotUsername, m.userMap); msgText != "" {
		var markup tg.ReplyMarkupClass
		if btn != nil {
			markup = btn.BuildMenu(1)
		}
		_, _ = m.sender.SendMessage(ctx, peer, msgText, msg.MessageID, markup)
		return
	}
	if !m.forceSubOK(ctx, peer, msg) {
		return
	}
	fail := func(text string) { m.replyTemp(ctx, peer, msg, text) }

	if !msg.IsReply || msg.ReplyToID <= 0 {
		fail("<i>Reply to a video with</i> <code>/cover" + config.Get().CmdSuffix + "</code> <i>to send it again with your thumbnail as its cover.</i>")
		return
	}
	src, err := telegram.FetchMessage(ctx, m.rawAPI, peer, msg.ReplyToID)
	if err != nil || src == nil {
		fail("<i>Can't read the replied message.</i>")
		return
	}
	media, _ := src.Media.(*tg.MessageMediaDocument)
	var doc *tg.Document
	if media != nil {
		doc, _ = media.Document.(*tg.Document)
	}
	name, isVideo := "", false
	if doc != nil {
		isVideo = strings.HasPrefix(doc.MimeType, "video/")
		for _, a := range doc.Attributes {
			switch at := a.(type) {
			case *tg.DocumentAttributeVideo:
				isVideo = true
			case *tg.DocumentAttributeFilename:
				name = at.FileName
			}
		}
	}
	if !isVideo {
		fail("<i>Reply to a Video to set its Cover!</i>")
		return
	}

	// the cover: the user's thumbnail, else the TMDB poster (Auto Thumbnail)
	cfg := config.Get()
	user := m.userMap.GetOrCreate(msg.UserID)
	cover := ""
	if user.CustomThumb != "" {
		if _, sErr := os.Stat(user.CustomThumb); sErr == nil {
			cover = user.CustomThumb
		}
	}
	if cover == "" && user.AutoThumbOn(cfg.AutoThumbnail) && name != "" {
		if p, tErr := ext.GetAutoThumbnail(ctx, name, false, cfg.LeechFilenameRemnameRegex, msg.LangCode); tErr == nil && p != "" {
			cover = p
			defer os.Remove(p)
		}
	}
	if cover == "" {
		fail("<i>Set a Custom Thumbnail or enable Auto Thumbnail in /us to use Cover!</i>")
		return
	}

	// always to the PM, like the Python bot
	pm := &tg.InputPeerUser{UserID: msg.UserID, AccessHash: telegram.GetUserAccessHash(msg.UserID)}
	photo, err := m.uploader.UploadCover(ctx, pm, cover)
	if err != nil || photo == nil {
		fail(fmt.Sprintf("<i>Cover upload failed: %v</i>", err))
		return
	}
	in := &tg.InputMediaDocument{ID: &tg.InputDocument{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}}
	in.SetVideoCover(photo)
	caption, entities := src.Message, src.Entities
	_, err = m.rawAPI.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer:     pm,
		Media:    in,
		Message:  caption,
		Entities: entities,
		RandomID: rand.Int63(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "PEER_ID_INVALID") || strings.Contains(err.Error(), "USER_IS_BLOCKED") {
			fail("<i>Start the bot in PM first: the video with its cover is sent there.</i>")
			return
		}
		fail(fmt.Sprintf("<i>Cover failed: %v</i>", err))
		return
	}
	if msg.ChatID == msg.UserID {
		m.deleteCommandLinks(peer, msg)
		return
	}
	m.replyTemp(ctx, peer, msg, "✓ <i>The video with its cover was sent to your PM.</i>")
}
