package modules

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// Video Tools → Default Audio / Default Subtitle: the menu comes before the download, when the
// tracks are not known yet. After the download the bot shows the real tracks of the video as
// buttons and the user picks one (no language code to type).

func vtPickText(v string) string {
	if v == "ask" {
		return "pick a track after the download"
	}
	return telegram.EscapeHTML(v)
}

type vtPickWait struct {
	uid int64
	ch  chan string
}

var (
	vtPickMu    sync.Mutex
	vtPickWaits = map[string]*vtPickWait{}
)

var langNames3 = map[string]string{
	"eng": "English", "heb": "Hebrew", "ara": "Arabic", "rus": "Russian", "hin": "Hindi", "tam": "Tamil",
	"tel": "Telugu", "mal": "Malayalam", "kan": "Kannada", "ben": "Bengali", "mar": "Marathi", "fre": "French",
	"fra": "French", "ger": "German", "deu": "German", "spa": "Spanish", "ita": "Italian", "por": "Portuguese",
	"jpn": "Japanese", "kor": "Korean", "chi": "Chinese", "zho": "Chinese", "tur": "Turkish", "pol": "Polish",
	"ukr": "Ukrainian", "dut": "Dutch", "nld": "Dutch", "swe": "Swedish", "tha": "Thai", "vie": "Vietnamese",
	"ind": "Indonesian", "per": "Persian", "fas": "Persian", "urd": "Urdu", "und": "Unknown",
}

// trackLabel: "Hebrew · AAC 5.1 · Dubbed" from the tags of a stream.
func trackLabel(s probeStream) string {
	var parts []string
	l := strings.ToLower(s.Tags["language"])
	if n, ok := langNames3[l]; ok {
		parts = append(parts, n)
	} else if l != "" {
		parts = append(parts, strings.ToUpper(l))
	}
	codec := strings.ToUpper(s.CodecName)
	if s.CodecType == "audio" && s.Channels > 0 {
		ch := map[int]string{1: "1.0", 2: "2.0", 6: "5.1", 8: "7.1"}[s.Channels]
		if ch == "" {
			ch = strconv.Itoa(s.Channels) + "ch"
		}
		codec += " " + ch
	}
	if codec != "" {
		parts = append(parts, codec)
	}
	if t := strings.TrimSpace(s.Tags["title"]); t != "" {
		parts = append(parts, t)
	}
	if len(parts) == 0 {
		return "Track"
	}
	return strings.Join(parts, " · ")
}

// askTrack: the tracks of the first video of path as buttons; the chosen number ("1", "2" ...)
// or "" (skipped, timed out, or only one track).
func (m *LeechModule) askTrack(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, path, kind string) string {
	videos := vtVideos(path)
	if len(videos) == 0 {
		return ""
	}
	want := "audio"
	title := "Default Audio"
	if kind == "subs" {
		want, title = "subtitle", "Default Subtitle"
	}
	var tracks []probeStream
	for _, s := range probeStreams(videos[0]) {
		if s.CodecType == want {
			tracks = append(tracks, s)
		}
	}
	if len(tracks) < 2 { // nothing to choose
		return ""
	}
	token := ext.RandomHex(3)
	w := &vtPickWait{uid: msg.UserID, ch: make(chan string, 1)}
	vtPickMu.Lock()
	vtPickWaits[token] = w
	vtPickMu.Unlock()
	defer func() {
		vtPickMu.Lock()
		delete(vtPickWaits, token)
		vtPickMu.Unlock()
	}()

	btn := telegram.NewButtonMaker()
	for i, t := range tracks {
		label := fmt.Sprintf("%d · %s", i+1, trackLabel(t))
		if t.Tags["DISPOSITION_DEFAULT"] == "1" {
			label += " ✓"
		}
		btn.IButton(label, fmt.Sprintf("vtool %d pick %s %d", msg.UserID, token, i+1))
		btn.Row()
	}
	btn.IButtonStyled("Skip", fmt.Sprintf("vtool %d pick %s skip", msg.UserID, token), "danger")
	btn.Row()
	text := fmt.Sprintf("㊂ <b><u>%s :</u></b>\n\n<blockquote>%d tracks in <code>%s</code>.\nPress the one to play first (2 minutes).</blockquote>",
		title, len(tracks), telegram.EscapeHTML(shortName(videos[0])))
	id, _ := m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 1)
	defer func() {
		if id > 0 {
			_ = m.sender.DeleteMessage(context.Background(), peer, []int{id})
		}
	}()
	select {
	case v := <-w.ch:
		return v
	case <-time.After(2 * time.Minute):
	case <-ctx.Done():
	}
	return ""
}

func shortName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// handleVTPick: "vtool <uid> pick <token> <n|skip>".
func (m *LeechModule) handleVTPick(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, parts []string, fromUID int64) {
	if len(parts) < 5 {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	vtPickMu.Lock()
	w := vtPickWaits[parts[3]]
	vtPickMu.Unlock()
	if w == nil {
		_ = m.sender.AnswerCallback(ctx, queryID, "This choice is closed!", true)
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}
	if w.uid != fromUID {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	v := parts[4]
	if v == "skip" {
		v = ""
		_ = m.sender.AnswerCallback(ctx, queryID, "Skipped", false)
	} else {
		_ = m.sender.AnswerCallback(ctx, queryID, "Track "+v, false)
	}
	select {
	case w.ch <- v:
	default:
	}
}
