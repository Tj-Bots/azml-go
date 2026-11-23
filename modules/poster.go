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

// /poster <name> (or a reply to a file): the TMDB images of a movie / show, like the poster command
// of TJ-Rename-Bot: a preview with its title (year), size, type and language, ⫷ ⫸ to browse,
// Poster / Backdrop / Logo, a language filter and the original image.

type posterSession struct {
	uid     int64
	titles  []ext.TMDBTitle
	title   ext.TMDBTitle
	images  map[string][]ext.TMDBImage // poster / backdrop / logo
	kind    string
	lang    string // "*" = all languages
	idx     int
	photoID int // the preview message (0 while the title list is shown)
	at      time.Time
}

var (
	posterMu       sync.Mutex
	posterSessions = map[string]*posterSession{}
	posterSeq      int
)

var posterKinds = []struct{ key, name string }{{"poster", "Poster"}, {"backdrop", "Backdrop"}, {"logo", "Logo"}}

func posterKindName(k string) string {
	for _, x := range posterKinds {
		if x.key == k {
			return x.name
		}
	}
	return k
}

func newPosterSession(s *posterSession) string {
	posterMu.Lock()
	defer posterMu.Unlock()
	for k, v := range posterSessions { // 30 minutes, then the buttons expire
		if time.Since(v.at) > 30*time.Minute {
			delete(posterSessions, k)
		}
	}
	posterSeq++
	id := strconv.Itoa(posterSeq)
	s.at = time.Now()
	posterSessions[id] = s
	return id
}

// HandlePoster: /poster <name>, or a reply to a video / file with /poster (its name is searched).
func (m *LeechModule) HandlePoster(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if msgText, btn := ext.CheckingAccess(msg.UserID, false, telegram.CurrentBotUsername, m.userMap); msgText != "" {
		var markup tg.ReplyMarkupClass
		if btn != nil {
			markup = btn.BuildMenu(1)
		}
		_, _ = m.sender.SendMessage(ctx, peer, msgText, msg.MessageID, markup)
		return
	}
	if !m.forceSubOK(ctx, peer, msg, func() { m.HandlePoster(context.Background(), peer, msg) }) {
		return
	}
	fail := func(text string) { m.replyTemp(ctx, peer, msg, text) }
	if !ext.TMDBEnabled() {
		fail("<i>Posters need TMDB_ACCESS_TOKEN (Bot Settings).</i>")
		return
	}

	query := ""
	if parts := strings.SplitN(strings.TrimSpace(msg.Text), " ", 2); len(parts) == 2 {
		query = strings.TrimSpace(parts[1])
	}
	if query == "" && msg.IsReply && msg.ReplyToID > 0 {
		query = m.repliedName(ctx, peer, msg.ReplyToID)
	}
	if query == "" {
		c := telegram.Cmd("poster")
		fail("<blockquote><b>╭</b> <code>" + c + " Oppenheimer 2023</code>\n" +
			"<b>╰</b> or reply to a video / file with <code>" + c + "</code></blockquote>\n" +
			"<i>Browse the posters, backdrops and logos of a movie or show (TMDB).</i>")
		return
	}
	title, _, year := ext.FormatCleanPosterTitle(query, "")
	if strings.TrimSpace(title) == "" {
		title = query
	}
	titles, err := ext.SearchTMDBTitles(ctx, title, year, msg.LangCode, 8)
	if err != nil || len(titles) == 0 {
		fail("<i>Nothing found on TMDB for</i> <code>" + telegram.EscapeHTML(query) + "</code>")
		return
	}
	s := &posterSession{uid: msg.UserID, titles: titles, kind: "poster", lang: "*"}
	sid := newPosterSession(s)
	if len(titles) == 1 {
		m.posterOpen(ctx, peer, sid, s, 0, msg.MessageID, 0)
		return
	}
	text, btn := posterTitlesView(sid, s, query)
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 1)
}

// repliedName: the file name (or the caption / text) of the replied message.
func (m *LeechModule) repliedName(ctx context.Context, peer tg.InputPeerClass, id int) string {
	src, err := telegram.FetchMessage(ctx, m.rawAPI, peer, id)
	if err != nil || src == nil {
		return ""
	}
	if md, ok := src.Media.(*tg.MessageMediaDocument); ok {
		if doc, ok := md.Document.(*tg.Document); ok {
			for _, a := range doc.Attributes {
				if f, ok := a.(*tg.DocumentAttributeFilename); ok && f.FileName != "" {
					return f.FileName
				}
			}
		}
	}
	return strings.SplitN(strings.TrimSpace(src.Message), "\n", 2)[0]
}

func posterTitlesView(sid string, s *posterSession, query string) (string, *telegram.ButtonMaker) {
	text := "⌬ <b><u>TMDB Search :</u></b>\n\n<blockquote><b>╰</b> <code>" + telegram.EscapeHTML(query) +
		"</code></blockquote>\n\n<i>Tap a title to browse its images 👇</i>"
	btn := telegram.NewButtonMaker()
	for i, t := range s.titles {
		icon := "🎬"
		if t.MediaType == "tv" {
			icon = "📺"
		}
		label := icon + " " + t.Name
		if t.Year != "" {
			label += " (" + t.Year + ")"
		}
		btn.IButton(label, fmt.Sprintf("pstr %s t %d", sid, i))
		btn.Row()
	}
	btn.IButtonStyled("❌", fmt.Sprintf("pstr %s x", sid), "danger")
	btn.Row()
	return text, btn
}

// posterOpen loads the images of title i and shows the first one (as a new photo message).
func (m *LeechModule) posterOpen(ctx context.Context, peer tg.InputPeerClass, sid string, s *posterSession, i, replyTo, listMsg int) {
	t := s.titles[i]
	p, b, l, err := ext.TMDBTitleImages(ctx, t.MediaType, t.ID)
	if err != nil || len(p)+len(b)+len(l) == 0 {
		_, _ = m.sender.SendMessage(ctx, peer, "<i>No images on TMDB for</i> <b>"+telegram.EscapeHTML(t.Name)+"</b>", replyTo, telegram.CloseMarkup(s.uid))
		return
	}
	posterMu.Lock()
	s.title, s.images, s.idx, s.lang = t, map[string][]ext.TMDBImage{"poster": p, "backdrop": b, "logo": l}, 0, "*"
	s.kind = "poster"
	for _, k := range posterKinds { // the first kind that has images
		if len(s.images[k.key]) > 0 {
			s.kind = k.key
			break
		}
	}
	posterMu.Unlock()
	caption, url, btn := posterView(sid, s)
	id, sErr := m.sender.SendPhotoURL(ctx, peer, url, caption, replyTo, btn, 5)
	if sErr != nil {
		_, _ = m.sender.SendMessage(ctx, peer, "<i>Can't show the image: "+telegram.EscapeHTML(sErr.Error())+"</i>", replyTo, telegram.CloseMarkup(s.uid))
		return
	}
	posterMu.Lock()
	s.photoID = id
	posterMu.Unlock()
	if listMsg > 0 {
		_ = m.sender.DeleteMessage(ctx, peer, []int{listMsg})
	}
}

// posterShown: the images of the current kind and language.
func posterShown(s *posterSession) []ext.TMDBImage {
	all := s.images[s.kind]
	if s.lang == "*" {
		return all
	}
	var out []ext.TMDBImage
	for _, im := range all {
		if im.Lang == s.lang {
			out = append(out, im)
		}
	}
	return out
}

// posterView: the caption, the preview URL and the buttons of the current image.
func posterView(sid string, s *posterSession) (string, string, *telegram.ButtonMaker) {
	shown := posterShown(s)
	if s.idx >= len(shown) {
		s.idx = 0
	}
	im := shown[s.idx]
	name := s.title.Name
	if s.title.Year != "" {
		name += " (" + s.title.Year + ")"
	}
	caption := "⌬ <b>" + telegram.EscapeHTML(name) + "</b>\n\n" +
		"<blockquote><b>╭</b> <b>Type:</b> " + posterKindName(s.kind) + "\n" +
		fmt.Sprintf("┠ <b>Quality:</b> %d × %d\n", im.Width, im.Height) +
		"┠ <b>Language:</b> " + ext.TMDBLangName(im.Lang) + "\n" +
		fmt.Sprintf("<b>╰</b> <b>Image:</b> %d / %d</blockquote>", s.idx+1, len(shown))

	size := "w780"
	switch s.kind {
	case "backdrop":
		size = "w1280"
	case "logo":
		size = "w500"
	}
	btn := telegram.NewButtonMaker()
	if len(shown) > 1 {
		btn.IButtonStyled("⫷", fmt.Sprintf("pstr %s p", sid), "primary")
		btn.IButton(fmt.Sprintf("%d / %d", s.idx+1, len(shown)), fmt.Sprintf("pstr %s noop", sid))
		btn.IButtonStyled("⫸", fmt.Sprintf("pstr %s n", sid), "primary")
		btn.Row()
	}
	for _, k := range posterKinds {
		n := len(s.images[k.key])
		if n == 0 {
			continue
		}
		label := fmt.Sprintf("%s (%d)", k.name, n)
		if k.key == s.kind {
			btn.IButtonStyled("✓ "+label, fmt.Sprintf("pstr %s noop", sid), "success")
		} else {
			btn.IButton(label, fmt.Sprintf("pstr %s k %s", sid, k.key))
		}
	}
	btn.Row()
	langLabel := "All"
	if s.lang != "*" {
		langLabel = ext.TMDBLangName(s.lang)
	}
	btn.IButton("🌐 Language: "+langLabel, fmt.Sprintf("pstr %s l", sid))
	btn.UButton("📥 Original", ext.TMDBImageURL(im.Path, "original"))
	btn.Row()
	if len(s.titles) > 1 {
		btn.IButtonStyled("◀️", fmt.Sprintf("pstr %s b", sid), "primary")
	}
	btn.IButtonStyled("❌", fmt.Sprintf("pstr %s x", sid), "danger")
	btn.Row()
	return caption, ext.TMDBImageURL(im.Path, size), btn
}

// posterLangMenu: the languages of the current kind, with how many images each has.
func posterLangMenu(sid string, s *posterSession) *telegram.ButtonMaker {
	counts, order := map[string]int{}, []string{}
	for _, im := range s.images[s.kind] {
		if counts[im.Lang] == 0 {
			order = append(order, im.Lang)
		}
		counts[im.Lang]++
	}
	btn := telegram.NewButtonMaker()
	label := fmt.Sprintf("All (%d)", len(s.images[s.kind]))
	if s.lang == "*" {
		label = "✓ " + label
	}
	btn.IButton(label, fmt.Sprintf("pstr %s L *", sid))
	btn.Row()
	for i, l := range order {
		label := fmt.Sprintf("%s (%d)", ext.TMDBLangName(l), counts[l])
		if l == s.lang {
			label = "✓ " + label
		}
		code := l
		if code == "" {
			code = "-"
		}
		btn.IButton(label, fmt.Sprintf("pstr %s L %s", sid, code))
		if i%2 == 1 {
			btn.Row()
		}
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("pstr %s v", sid), "primary")
	btn.Row()
	return btn
}

// HandlePosterCallback: "pstr <session> <action> [arg]".
func (m *LeechModule) HandlePosterCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	sid, action, arg := parts[1], parts[2], ""
	if len(parts) > 3 {
		arg = parts[3]
	}
	posterMu.Lock()
	s := posterSessions[sid]
	if s != nil {
		s.at = time.Now()
	}
	posterMu.Unlock()
	if s == nil {
		_ = m.sender.AnswerCallback(ctx, queryID, "This search expired, send the command again.", true)
		return
	}
	if fromUID != s.uid {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	refresh := func() {
		posterMu.Lock()
		caption, url, btn := posterView(sid, s)
		posterMu.Unlock()
		if err := m.sender.EditPhotoURL(ctx, peer, msgID, url, caption, btn, 5); err != nil {
			_ = m.sender.AnswerCallback(ctx, queryID, "Can't load this image, try the next one.", true)
			return
		}
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	}
	switch action {
	case "noop":
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "x":
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed!", false)
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		posterMu.Lock()
		delete(posterSessions, sid)
		posterMu.Unlock()
	case "t": // a title of the list
		i, err := strconv.Atoi(arg)
		if err != nil || i < 0 || i >= len(s.titles) {
			_ = m.sender.AnswerCallback(ctx, queryID, "", false)
			return
		}
		_ = m.sender.AnswerCallback(ctx, queryID, "Loading images...", false)
		m.posterOpen(ctx, peer, sid, s, i, 0, msgID)
	case "b": // back to the title list (a text message again)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		text, btn := posterTitlesView(sid, s, s.title.Name)
		if _, err := m.sender.SendMessageWithButtons(ctx, peer, text, 0, btn, 1); err == nil {
			_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		}
	case "n", "p":
		posterMu.Lock()
		if n := len(posterShown(s)); n > 0 {
			if action == "n" {
				s.idx = (s.idx + 1) % n
			} else {
				s.idx = (s.idx - 1 + n) % n
			}
		}
		posterMu.Unlock()
		refresh()
	case "k": // Poster / Backdrop / Logo
		posterMu.Lock()
		if len(s.images[arg]) > 0 {
			s.kind, s.idx, s.lang = arg, 0, "*"
		}
		posterMu.Unlock()
		refresh()
	case "l": // the language menu (only the buttons change)
		posterMu.Lock()
		btn := posterLangMenu(sid, s)
		posterMu.Unlock()
		_ = m.sender.EditButtons(ctx, peer, msgID, btn, 2)
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	case "L":
		if arg == "-" {
			arg = ""
		}
		posterMu.Lock()
		s.lang, s.idx = arg, 0
		if len(posterShown(s)) == 0 {
			s.lang = "*"
		}
		posterMu.Unlock()
		refresh()
	case "v": // back from the language menu
		refresh()
	default:
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	}
}
