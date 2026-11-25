package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// The tracks of the file before the download (like /mediainfo): a direct link is read by ffprobe
// itself, a Telegram file from its first 8 MB. When that works the -vt menu shows the real
// tracks (Default Audio / Subtitle, Translate); else the user picks them after the download.

func ffprobeStreams(ctx context.Context, src string) []probeStream {
	out, err := exec.CommandContext(ctx, ext.FFprobeBin(), "-v", "error", "-print_format", "json", "-show_streams", src).Output()
	if err != nil {
		return nil
	}
	var pr struct {
		Streams []probeStream `json:"streams"`
	}
	_ = json.Unmarshal(out, &pr)
	return pr.Streams
}

// vtProbe: the tracks of the link / Telegram file of the task, nil when they can't be read.
func (m *LeechModule) vtProbe(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, link string) []probeStream {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	low := strings.ToLower(link)
	if ext.IsURL(link) && !ext.IsMagnet(link) && !strings.Contains(low, ".torrent") && !ext.IsMegaLink(link) {
		return ffprobeStreams(ctx, link)
	}
	if m.rawAPI == nil {
		return nil
	}
	var doc *tg.Document
	if md, ok := msg.RawMedia.(*tg.MessageMediaDocument); ok {
		doc, _ = md.Document.(*tg.Document)
	}
	if doc == nil && msg.IsReply && msg.ReplyToID > 0 {
		if src, err := telegram.FetchMessage(ctx, m.rawAPI, peer, msg.ReplyToID); err == nil && src != nil {
			if md, ok := src.Media.(*tg.MessageMediaDocument); ok {
				doc, _ = md.Document.(*tg.Document)
			}
		}
	}
	if doc == nil {
		return nil
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("vtprobe_%d_%s", msg.UserID, ext.RandomHex(3)))
	defer os.Remove(tmp)
	if err := readTelegramHead(ctx, m.rawAPI, doc, tmp, 8<<20); err != nil {
		return nil
	}
	return ffprobeStreams(ctx, tmp)
}

func (st *vtState) tracks(kind string) []probeStream {
	var out []probeStream
	for _, s := range st.streams {
		if s.CodecType == kind {
			out = append(out, s)
		}
	}
	return out
}

var vtTrLangs = [][2]string{{"he", "Hebrew"}, {"en", "English"}, {"ar", "Arabic"}, {"ru", "Russian"}, {"es", "Spanish"}, {"fr", "French"}}

func vtLangName(code string) string {
	for _, l := range vtTrLangs {
		if l[0] == code {
			return l[1]
		}
	}
	return strings.ToUpper(code)
}

// trackText: "2 · Hebrew · AAC 5.1" of a picked track number, or the text of "ask".
func (st *vtState) trackText(kind, v string) string {
	if v == "ask" {
		return "pick after the download"
	}
	if v == "all" {
		return "all"
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return telegram.EscapeHTML(v)
	}
	if t := st.tracks(kind); n >= 1 && n <= len(t) {
		return fmt.Sprintf("%d · %s", n, telegram.EscapeHTML(trackLabel(t[n-1])))
	}
	return "track " + v
}

// streamPage: the pages that list the real tracks; ok false = not one of them.
func (st *vtState) streamPage(b *telegram.ButtonMaker) (string, bool) {
	back := func() {
		b.Row()
		b.IButtonStyled("◀️", fmt.Sprintf("vtool %d back", st.user), "primary")
		b.Row()
	}
	list := func(kind, action, cur string, allBtn bool) {
		for i, t := range st.tracks(kind) {
			label := fmt.Sprintf("%d · %s", i+1, trackLabel(t))
			if kind == "subtitle" && !textSubCodecs[t.CodecName] && action == "trs" {
				continue // a picture subtitle (PGS) can't be translated
			}
			st.btn(b, action, label, cur == strconv.Itoa(i+1), strconv.Itoa(i+1))
			b.Row()
		}
		if allBtn {
			st.btn(b, action, "All Subtitles", cur == "all", "all")
			b.Row()
		}
	}
	switch st.page {
	case "pick_daudio":
		list("audio", "das", st.sel.DefAudio, false)
		back()
		return "㊂ <b><u>Video Tools : Default Audio</u></b>\n\n<i>The track that should play first. It becomes the first and default one, nothing is re-encoded.</i>", true
	case "pick_dsubs":
		list("subtitle", "dss", st.sel.DefSubs, false)
		back()
		return "㊂ <b><u>Video Tools : Default Subtitle</u></b>\n\n<i>The subtitle that is on by default. Nothing is re-encoded.</i>", true
	case "translate":
		for i, l := range vtTrLangs {
			st.btn(b, "trl", l[1], st.sel.Translate == l[0], l[0])
			if i%3 == 2 {
				b.Row()
			}
		}
		b.Row()
		other := "Other Language"
		if st.sel.Translate != "" && vtLangName(st.sel.Translate) == strings.ToUpper(st.sel.Translate) {
			other = "✓ " + strings.ToUpper(st.sel.Translate)
		}
		b.IButton(other, fmt.Sprintf("vtool %d trlo", st.user))
		b.Row()
		st.btn(b, "tre", "Google", !st.sel.TrAI, "google")
		st.btn(b, "tre", "AI (Gemini)", st.sel.TrAI, "ai")
		b.Row()
		st.btn(b, "trk", "Keep Original", !st.sel.TrDrop, "")
		b.Row()
		subs := 0
		for _, t := range st.tracks("subtitle") {
			if textSubCodecs[t.CodecName] {
				subs++
			}
		}
		var which string
		switch {
		case st.probing:
			which = "<i>reading the file...</i>"
		case subs > 1:
			list("subtitle", "trs", st.sel.TrStream, true)
			which = st.trackText("subtitle", st.sel.TrStream)
			if st.sel.TrStream == "" {
				which = "the first one"
			}
		case subs == 1:
			which = "the only one"
		default:
			which = "pick after the download"
		}
		back()
		lang := "<i>choose</i>"
		if st.sel.Translate != "" {
			lang = vtLangName(st.sel.Translate)
		}
		engine := "Google Translate"
		if st.sel.TrAI {
			engine = "AI (your Gemini key)"
		}
		keep := "Yes (the translation is added)"
		if st.sel.TrDrop {
			keep = "No (the translation replaces it)"
		}
		return "㊂ <b><u>Video Tools : Translate Subtitles</u></b>\n\n" +
			"<blockquote><b>╭</b> <b>To :</b> " + lang + "\n" +
			"┠ <b>Engine :</b> " + engine + "\n" +
			"┠ <b>Subtitle :</b> " + which + "\n" +
			"<b>╰</b> <b>Keep Original :</b> " + keep + "</blockquote>", true
	}
	return "", false
}

// vtStreamAction: the actions of those pages (true = handled).
func (st *vtState) vtStreamAction(action, arg string) bool {
	keepTr := func() { // one tool at a time, but the translate options stay while it is chosen
		if st.sel.Translate == "" {
			st.sel.clearTool()
		}
	}
	switch action {
	case "translate":
		st.page = "translate"
	case "trl":
		keepTr()
		st.sel.Translate = arg
	case "trlo":
		st.wait = "translate"
		ext.SetPendingSettingSession(st.user, "vt_translate", st.chat, st.msgID, vtTimeout)
	case "tre":
		keepTr()
		st.sel.TrAI = arg == "ai"
	case "trk":
		keepTr()
		st.sel.TrDrop = !st.sel.TrDrop
	case "trs":
		keepTr()
		if st.sel.TrStream == arg {
			st.sel.TrStream = ""
		} else {
			st.sel.TrStream = arg
		}
	case "das", "dss":
		if action == "das" {
			was := st.sel.DefAudio
			st.sel.clearTool()
			if was != arg {
				st.sel.DefAudio = arg
			}
		} else {
			was := st.sel.DefSubs
			st.sel.clearTool()
			if was != arg {
				st.sel.DefSubs = arg
			}
		}
		st.page = "main"
	default:
		return false
	}
	return true
}
