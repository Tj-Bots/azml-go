package modules

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"azml-go/config"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// The usage of /leech, /mirror and /ytdl like the Python bot (help_menu): a short intro and one
// button per argument; a button shows how to use it, ◀️ goes back, ❌ closes.

type helpArg struct {
	Btn, Text, Example string
}

var leechHelpArgs = []helpArg{
	{"Name", "<b>New Name</b>\n\n<blockquote><code>-n name</code> — the new name of the file or folder.</blockquote>", "/cmd link -n My Movie.mkv"},
	{"Zip / Extract", "<b>Zip / Extract</b>\n\n<blockquote><code>-z</code> — zip before upload\n<code>-e</code> — extract an archive\n<code>-p pass</code> — the archive password</blockquote>", "/cmd link -e -p 1234"},
	{"Join Parts", "<b>Join Split Parts</b>\n\n<blockquote><code>-j</code> — joins .001 .002 ... parts back into one file.</blockquote>", "/cmd link -j"},
	{"Multi", "<b>Multi Links</b>\n\n<blockquote><code>-i 5</code> — reply to the first message: it and the next 4 messages are leeched.</blockquote>", "/cmd -i 5"},
	{"Bulk", "<b>Bulk</b>\n\n<blockquote><code>-b</code> — reply to a message or .txt with a list of links (one per line).\n<code>-b 3:8</code> — only links 3 to 8.</blockquote>", "/cmd -b"},
	{"Same Folder", "<b>Same Folder</b>\n\n<blockquote><code>-m name</code> — every file of <code>-i</code> / <code>-b</code> goes to one folder; with <code>-z</code> into one zip.</blockquote>", "/cmd -i 3 -m Season 1 -z"},
	{"Select Files", "<b>Select Files</b>\n\n<blockquote><code>-s</code> — choose the files to download (GoFile, Drive folder, Mega, torrents).</blockquote>", "/cmd link -s"},
	{"Thumbnail", "<b>Thumbnail</b>\n\n<blockquote><code>-t link</code> — a thumbnail for this task only: an image link or a t.me link of a photo.</blockquote>", "/cmd link -t https://t.me/c/123/45"},
	{"Split Size", "<b>Split Size</b>\n\n<blockquote><code>-sp 1.5GB</code> — the split size for this task (e.g. 500MB, 1.5GB).</blockquote>", "/cmd link -sp 1GB"},
	{"Doc / Media", "<b>Document / Media</b>\n\n<blockquote><code>-d</code> — upload as document\n<code>-med</code> — upload as media</blockquote>", "/cmd link -d"},
	{"Dump Chat", "<b>Dump Chat</b>\n\n<blockquote><code>-ud chat_id</code> — upload this task to another chat (the bot must be admin there).</blockquote>", "/cmd link -ud -1001234567890"},
	{"Screenshots", "<b>Screenshots</b>\n\n<blockquote><code>-ss 6</code> — an album of 6 screenshots of every video.</blockquote>", "/cmd link -ss 6"},
	{"Split Tracks", "<b>Split Tracks</b>\n\n<blockquote><code>-st</code> — every video, audio and subtitle track as its own file.</blockquote>", "/cmd link -st"},
	{"Video Tools", "<b>Video Tools</b>\n\n<blockquote><code>-vt</code> — a menu before the task starts: rename, merge, extract, remove streams, trim, convert, sync and more.</blockquote>", "/cmd link -vt"},
	{"Extract Audio", "<b>Extract Audio</b>\n\n<blockquote><code>-ea</code> — only the audio of the video.\n<code>-ea m4a</code> — in a format: m4a flac opus ogg wav mp3.</blockquote>", "/cmd link -ea mp3"},
	{"Convert", "<b>Convert</b>\n\n<blockquote><code>-cv mp4</code> — videos to mp4 (no re-encode)\n<code>-ca mp3</code> — audios to mp3</blockquote>", "/cmd link -cv mp4"},
	{"Login", "<b>Login</b>\n\n<blockquote><code>-u user -p pass</code> — the user and password of a direct link that needs a login.</blockquote>", "/cmd link -u me -p 1234"},
	{"Headers", "<b>Headers</b>\n\n<blockquote><code>-h Key: value</code> — extra headers for a direct link.</blockquote>", "/cmd link -h Referer: https://site.com"},
}

var mirrorHelpArgs = []helpArg{
	{"Upload To", "<b>Upload To</b>\n\n<blockquote><code>-up gd</code> — Google Drive\n<code>-up rc</code> — RClone (your RClone Path)\n<code>-up gofile</code> — a DDL server (gofile, pixeldrain, buzzheavier ...)\n<code>-up mtp:folder_id</code> — your token.pickle, to that Drive folder\n<code>-up remote:path</code> — an RClone remote\nWithout <code>-up</code>: the default of /us → Mirror Settings.</blockquote>", "/cmd link -up gd"},
	{"Name", "<b>New Name</b>\n\n<blockquote><code>-n name</code> — the new name of the file or folder.</blockquote>", "/cmd link -n My File.zip"},
	{"Zip / Extract", "<b>Zip / Extract</b>\n\n<blockquote><code>-z</code> — zip before upload\n<code>-e</code> — extract an archive\n<code>-p pass</code> — the archive password</blockquote>", "/cmd link -z"},
	{"Join Parts", "<b>Join Split Parts</b>\n\n<blockquote><code>-j</code> — joins .001 .002 ... parts back into one file.</blockquote>", "/cmd link -j"},
	{"Select Files", "<b>Select Files</b>\n\n<blockquote><code>-s</code> — choose the files to download (GoFile, Drive folder, Mega, torrents).</blockquote>", "/cmd link -s"},
	{"Multi", "<b>Multi Links</b>\n\n<blockquote><code>-i 5</code> — reply to the first message: it and the next 4 messages are mirrored.</blockquote>", "/cmd -i 5"},
	{"Bulk", "<b>Bulk</b>\n\n<blockquote><code>-b</code> — reply to a message or .txt with a list of links (one per line).\n<code>-b 3:8</code> — only links 3 to 8.</blockquote>", "/cmd -b"},
	{"Same Folder", "<b>Same Folder</b>\n\n<blockquote><code>-m name</code> — every file of <code>-i</code> / <code>-b</code> goes to one folder.</blockquote>", "/cmd -i 3 -m Season 1"},
	{"Login", "<b>Login</b>\n\n<blockquote><code>-u user -p pass</code> — the user and password of a direct link that needs a login.</blockquote>", "/cmd link -u me -p 1234"},
	{"Headers", "<b>Headers</b>\n\n<blockquote><code>-h Key: value</code> — extra headers for a direct link.</blockquote>", "/cmd link -h Referer: https://site.com"},
	{"Rename", "<b>Mirror Rename</b>\n\n<blockquote>Prefix, suffix and remname of mirrored files: /us → Mirror Settings → Rename.</blockquote>", ""},
}

var ytdlHelpArgs = []helpArg{
	{"Quality", "<b>Quality</b>\n\n<blockquote>Without a quality the bot shows buttons.\n<code>720p</code> — a direct quality (144p - 2160p)\n<code>best</code> — best video + audio\n<code>audio</code> / <code>mp3</code> — audio only</blockquote>", "/cmd link 1080p"},
	{"Format", "<b>Custom Format</b>\n\n<blockquote><code>-q format</code> — a yt-dlp format string.</blockquote>", "/cmd link -q bv*+ba/b"},
	{"Name", "<b>New Name</b>\n\n<blockquote><code>-n name</code> — the new name of the file.</blockquote>", "/cmd link -n Clip.mp4"},
	{"Zip", "<b>Zip</b>\n\n<blockquote><code>-z</code> — zip before upload. (yt-dlp gives videos, not archives: there is nothing to extract.)</blockquote>", "/cmd link -z"},
	{"Doc / Media", "<b>Document / Media</b>\n\n<blockquote><code>-d</code> — upload as document\n<code>-med</code> — upload as media</blockquote>", "/cmd link -d"},
	{"Thumbnail", "<b>Thumbnail</b>\n\n<blockquote><code>-t link</code> — a thumbnail for this task only: an image link or a t.me link of a photo.</blockquote>", "/cmd link -t https://t.me/c/123/45"},
	{"Screenshots", "<b>Screenshots</b>\n\n<blockquote><code>-ss 6</code> — an album of 6 screenshots of the video.</blockquote>", "/cmd link -ss 6"},
	{"Mirror", "<b>Mirror</b>\n\n<blockquote><code>-up gofile</code> — upload to a host instead of Telegram.</blockquote>", "/cmd link -up gofile"},
	{"Cookies", "<b>Cookies</b>\n\n<blockquote>Your own cookies.txt: /us → Universal → YT-DLP Cookies. Without it the bot's cookies.txt is used.</blockquote>", ""},
}

func helpKind(kind string) (title, intro string, args []helpArg) {
	switch kind {
	case "mirror":
		return "Mirror", "Download and upload to Google Drive, RClone or a DDL host.\n➲ <code>/cmd link</code>\n➲ Reply to a link / file with <code>/cmd</code>", mirrorHelpArgs
	case "ytdl":
		return "YT-DLP", "Download from YouTube and 1000+ sites (Instagram, TikTok, X, Facebook ...).\n➲ <code>/cmd link</code>\n➲ <code>/cmd link 720p</code>", ytdlHelpArgs
	}
	return "Leech", "Download and upload to Telegram.\n➲ <code>/cmd link</code>\n➲ Reply to a link / file with <code>/cmd</code>", leechHelpArgs
}

func helpCmd(kind string) string {
	c := "leech"
	if kind == "mirror" || kind == "ytdl" {
		c = kind
	}
	return "/" + c + config.Get().CmdSuffix
}

const helpPerPage = 30 // every argument on one page, 3 per row

// cmdHelpMenu: the intro and the buttons of one page.
func cmdHelpMenu(kind string, uid int64, page int) (string, *telegram.ButtonMaker) {
	title, intro, args := helpKind(kind)
	pages := (len(args) + helpPerPage - 1) / helpPerPage
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	text := "㊂ <b><u>" + title + " Command Usage :</u></b>\n\n<blockquote>" + intro + "</blockquote>\n<i>Tap an argument to see how to use it:</i>"
	text = strings.ReplaceAll(text, "/cmd", helpCmd(kind))

	btn := telegram.NewButtonMaker()
	n := 0
	for i := page * helpPerPage; i < len(args) && i < (page+1)*helpPerPage; i++ {
		btn.IButton(args[i].Btn, fmt.Sprintf("chelp %d %s i %d %d", uid, kind, i, page))
		n++
		if n%3 == 0 {
			btn.Row()
		}
	}
	btn.Row()
	if pages > 1 {
		if page > 0 {
			btn.IButton("⫷", fmt.Sprintf("chelp %d %s p %d", uid, kind, page-1))
		}
		btn.IButton(fmt.Sprintf("%d/%d", page+1, pages), fmt.Sprintf("chelp %d %s p %d", uid, kind, page))
		if page < pages-1 {
			btn.IButton("⫸", fmt.Sprintf("chelp %d %s p %d", uid, kind, page+1))
		}
		btn.Row()
	}
	btn.IButtonStyled("❌", fmt.Sprintf("start_close %d", uid), "danger")
	btn.Row()
	return text, btn
}

// cmdHelpItem: one argument, with ◀️ back to its page and ❌.
func cmdHelpItem(kind string, uid int64, item, page int) (string, *telegram.ButtonMaker) {
	_, _, args := helpKind(kind)
	if item < 0 || item >= len(args) {
		return cmdHelpMenu(kind, uid, page)
	}
	a := args[item]
	text := a.Text
	if a.Example != "" {
		text += "\n\n<b>Example:</b> <code>" + a.Example + "</code>"
	}
	text = strings.ReplaceAll(text, "/cmd", helpCmd(kind))
	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("◀️", fmt.Sprintf("chelp %d %s p %d", uid, kind, page), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("start_close %d", uid), "danger")
	btn.Row()
	return text, btn
}

// sendCmdHelp: the usage menu in answer to the command; with AUTO_DELETE_MESSAGE_DURATION both go.
func (m *LeechModule) sendCmdHelp(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, kind string) {
	text, btn := cmdHelpMenu(kind, msg.UserID, 0)
	id, _ := m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 2)
	autoDeletePair(m.sender, peer, id, msg.MessageID)
}

// HandleHelpCallback: "chelp <uid> <kind> p <page>" / "chelp <uid> <kind> i <item> <page>".
func (m *LeechModule) HandleHelpCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 5 {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	uid, _ := strconv.ParseInt(parts[1], 10, 64)
	if uid != fromUID {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	kind := parts[2]
	var text string
	var btn *telegram.ButtonMaker
	if parts[3] == "i" && len(parts) >= 6 {
		item, _ := strconv.Atoi(parts[4])
		page, _ := strconv.Atoi(parts[5])
		text, btn = cmdHelpItem(kind, uid, item, page)
	} else {
		page, _ := strconv.Atoi(parts[4])
		text, btn = cmdHelpMenu(kind, uid, page)
	}
	_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
	_ = m.sender.AnswerCallback(ctx, queryID, "", false)
}
