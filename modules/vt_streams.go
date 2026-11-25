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

func vtLangName(code string) string {
	for _, l := range allTrLangs() {
		if l[0] == code {
			return l[1]
		}
	}
	return strings.ToUpper(code)
}

// allTrLangs: the spoken languages Google Translate and Gemini both know, by name (A-Z).
func allTrLangs() [][2]string {
	l := [][2]string{
		{"af", "Afrikaans"}, {"sq", "Albanian"}, {"am", "Amharic"}, {"ar", "Arabic"}, {"hy", "Armenian"}, {"az", "Azerbaijani"},
		{"eu", "Basque"}, {"be", "Belarusian"}, {"bn", "Bengali"}, {"bs", "Bosnian"}, {"bg", "Bulgarian"}, {"my", "Burmese"},
		{"ca", "Catalan"}, {"zh-CN", "Chinese (Simplified)"}, {"zh-TW", "Chinese (Traditional)"}, {"hr", "Croatian"}, {"cs", "Czech"},
		{"da", "Danish"}, {"nl", "Dutch"}, {"en", "English"}, {"et", "Estonian"}, {"tl", "Filipino"}, {"fi", "Finnish"},
		{"fr", "French"}, {"gl", "Galician"}, {"ka", "Georgian"}, {"de", "German"}, {"el", "Greek"}, {"gu", "Gujarati"},
		{"ht", "Haitian Creole"}, {"ha", "Hausa"}, {"he", "Hebrew"}, {"hi", "Hindi"}, {"hu", "Hungarian"}, {"is", "Icelandic"},
		{"ig", "Igbo"}, {"id", "Indonesian"}, {"ga", "Irish"}, {"it", "Italian"}, {"ja", "Japanese"}, {"jv", "Javanese"},
		{"kn", "Kannada"}, {"kk", "Kazakh"}, {"km", "Khmer"}, {"ko", "Korean"}, {"ku", "Kurdish"}, {"ky", "Kyrgyz"},
		{"lo", "Lao"}, {"lv", "Latvian"}, {"lt", "Lithuanian"}, {"mk", "Macedonian"}, {"mg", "Malagasy"}, {"ms", "Malay"},
		{"ml", "Malayalam"}, {"mt", "Maltese"}, {"mr", "Marathi"}, {"mn", "Mongolian"}, {"ne", "Nepali"}, {"no", "Norwegian"},
		{"ps", "Pashto"}, {"fa", "Persian"}, {"pl", "Polish"}, {"pt", "Portuguese"}, {"pa", "Punjabi"}, {"ro", "Romanian"},
		{"ru", "Russian"}, {"sr", "Serbian"}, {"si", "Sinhala"}, {"sk", "Slovak"}, {"sl", "Slovenian"}, {"so", "Somali"},
		{"es", "Spanish"}, {"sw", "Swahili"}, {"sv", "Swedish"}, {"tg", "Tajik"}, {"ta", "Tamil"}, {"te", "Telugu"},
		{"th", "Thai"}, {"tr", "Turkish"}, {"uk", "Ukrainian"}, {"ur", "Urdu"}, {"uz", "Uzbek"}, {"vi", "Vietnamese"},
		{"cy", "Welsh"}, {"yi", "Yiddish"}, {"yo", "Yoruba"}, {"zu", "Zulu"},
	}
	return l
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
	pickPage := func(kind, action, cur, title, about string) string {
		n := len(st.tracks(kind))
		note := ""
		switch {
		case st.probing:
			note = "<i>Reading the tracks of the file...</i>"
		case n > 1:
			list(kind, action, cur, false)
		case n == 1:
			note = "<i>The file has only one track of this kind: nothing to choose.</i>"
		default:
			note = "<i>The tracks could not be read before the download.</i>"
			st.btn(b, action, "Pick After The Download", cur == "ask", "ask")
			b.Row()
		}
		back()
		text := "㊂ <b><u>Video Tools : " + title + "</u></b>\n\n<blockquote>" + about + "</blockquote>"
		if note != "" {
			text += "\n" + note
		}
		return text
	}
	switch st.page {
	case "pick_daudio":
		return pickPage("audio", "das", st.sel.DefAudio, "Default Audio", "The track that should play first. It becomes the first and default one, nothing is re-encoded."), true
	case "pick_dsubs":
		return pickPage("subtitle", "dss", st.sel.DefSubs, "Default Subtitle", "The subtitle that is on by default. Nothing is re-encoded."), true
	case "trlangs":
		langs := allTrLangs()
		const per = 30
		pages := (len(langs) + per - 1) / per
		pg := st.langPage
		if pg < 0 || pg >= pages {
			pg = 0
		}
		for i := pg * per; i < len(langs) && i < (pg+1)*per; i++ {
			st.btn(b, "trl", langs[i][1], st.sel.Translate == langs[i][0], langs[i][0])
			if (i-pg*per)%3 == 2 {
				b.Row()
			}
		}
		b.Row()
		if pages > 1 {
			b.IButton("⫷", fmt.Sprintf("vtool %d trlp %d", st.user, (pg-1+pages)%pages))
			b.IButton(fmt.Sprintf("%d/%d", pg+1, pages), fmt.Sprintf("vtool %d trlp %d", st.user, pg))
			b.IButton("⫸", fmt.Sprintf("vtool %d trlp %d", st.user, (pg+1)%pages))
			b.Row()
		}
		b.IButtonStyled("◀️", fmt.Sprintf("vtool %d translate", st.user), "primary")
		b.Row()
		return "㊂ <b><u>Video Tools : Translate To</u></b>\n\n<i>Choose the language of the translation.</i>", true
	case "translate":
		if st.sel.Translate != "" {
			b.IButtonStyled("✓ Language: "+vtLangName(st.sel.Translate), fmt.Sprintf("vtool %d trlp 0", st.user), "success")
		} else {
			b.IButtonStyled("Choose Language", fmt.Sprintf("vtool %d trlp 0", st.user), "primary")
		}
		b.Row()
		st.btn(b, "tre", "Google", !st.sel.TrAI, "google")
		st.btn(b, "tre", "AI (Gemini)", st.sel.TrAI, "ai")
		b.Row()
		st.btn(b, "trk", "Keep Original Subtitle", !st.sel.TrDrop, "")
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
	keepTr := func() { // one tool at a time: the other tools go, the translate options stay
		if st.sel.Translate == "" {
			ai, stream, drop := st.sel.TrAI, st.sel.TrStream, st.sel.TrDrop
			st.sel.clearTool()
			st.sel.TrAI, st.sel.TrStream, st.sel.TrDrop = ai, stream, drop
		}
	}
	switch action {
	case "translate":
		st.page = "translate"
	case "trl":
		keepTr()
		st.sel.Translate = arg
		st.page = "translate"
	case "trlp": // the language list, a page of it
		st.langPage, _ = strconv.Atoi(arg)
		st.page = "trlangs"
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
