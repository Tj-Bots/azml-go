package modules

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// /p (/poster) <name>, or a reply to a video / file: the TMDB posters and backdrops of a movie or
// show, the same look and buttons as the poster command of TJ-Rename-Bot (MinimalTheme): one text
// message (a reply to the command) with the image as a large preview above it.

const posterPerPage = 8

type posterSession struct {
	uid    int64
	query  string
	userL  string // the user's Telegram language (its images come right after English)
	titles []ext.TMDBTitle
	page   int
	sel    int
	images map[string][]ext.TMDBImage // "p" posters, "b" backdrops
	kind   string
	lang   string // "all", a language code, or "xx" = no text
	idx    int
	at     time.Time
	busy   bool
}

var (
	posterMu       sync.Mutex
	posterSessions = map[string]*posterSession{}
	posterSeq      int
)

var posterKindName = map[string]string{"p": "Poster", "b": "Backdrop"}
var posterKindPlural = map[string]string{"p": "Posters", "b": "Backdrops"}

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

// pagerRow: ⏮ ◀️ x/y ▶️ ⏭ (like pager() of the rename bot); nothing for one page.
func pagerRow(btn *telegram.ButtonMaker, prefix string, page, pages int) {
	if pages <= 1 {
		return
	}
	btn.IButtonStyled("⏮", fmt.Sprintf("%s%d", prefix, 0), "primary")
	btn.IButtonStyled("◀️", fmt.Sprintf("%s%d", prefix, (page-1+pages)%pages), "primary")
	btn.IButtonStyled(fmt.Sprintf("%d/%d", page+1, pages), fmt.Sprintf("%s%d", prefix, page), "primary")
	btn.IButtonStyled("▶️", fmt.Sprintf("%s%d", prefix, (page+1)%pages), "primary")
	btn.IButtonStyled("⏭", fmt.Sprintf("%s%d", prefix, pages-1), "primary")
	btn.Row()
}

// markBtn: "✓ label" in green when active.
func markBtn(btn *telegram.ButtonMaker, on bool, label, data string) {
	if on {
		btn.IButtonStyled("✓ "+label, data, "success")
	} else {
		btn.IButton(label, data)
	}
}

func posterYear(y string) string {
	if y == "" {
		return "????"
	}
	return y
}

// HandlePoster: /p <name>, or a reply to a video / file with /p (its name is searched).
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
		fail("<b>TMDB_ACCESS_TOKEN is not configured.</b>")
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
		c := telegram.Cmd("p")
		fail("㊂ <b><u>Poster Search :</u></b>\n\n<blockquote><b>╭</b> <code>" + c + " Oppenheimer 2023</code>\n" +
			"┠ or reply to a video / file with <code>" + c + "</code>\n" +
			"<b>╰</b> browse posters & backdrops by language or get the original</blockquote>")
		return
	}
	title, _, year := ext.FormatCleanPosterTitle(query, "")
	if strings.TrimSpace(title) == "" {
		title = query
	}
	waitID, _ := m.sender.SendWithPreview(ctx, peer, "<i>Searching TMDB for</i> <code>"+telegram.EscapeHTML(query)+"</code><i>...</i>", "", msg.MessageID, nil, 1)
	titles, err := ext.SearchTMDBTitles(ctx, title, year, msg.LangCode, 20)
	if err != nil || len(titles) == 0 {
		text := "<b>Nothing found on TMDB.</b>"
		if err != nil {
			text = "<b>TMDB search failed, try again later.</b>"
		}
		if waitID > 0 {
			_ = m.sender.EditWithPreview(ctx, peer, waitID, text, "", nil, 1)
			autoDeletePair(m.sender, peer, waitID, msg.MessageID)
		} else {
			fail(text)
		}
		return
	}
	s := &posterSession{uid: msg.UserID, query: query, userL: shortCode(msg.LangCode), titles: titles, sel: -1, lang: "all"}
	sid := newPosterSession(s)
	text, btn := posterResultsView(sid, s)
	if waitID > 0 {
		_ = m.sender.EditWithPreview(ctx, peer, waitID, text, "", btn, 1)
		if len(titles) == 1 { // one result: straight to its images
			m.posterOpen(ctx, peer, sid, s, 0, waitID, 0)
		}
		return
	}
	_, _ = m.sender.SendWithPreview(ctx, peer, text, "", msg.MessageID, btn, 1)
}

func shortCode(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	return l
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

func posterShort(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func posterResultsView(sid string, s *posterSession) (string, *telegram.ButtonMaker) {
	pages := (len(s.titles) + posterPerPage - 1) / posterPerPage
	if pages < 1 {
		pages = 1
	}
	s.page = (s.page%pages + pages) % pages
	start := s.page * posterPerPage
	end := start + posterPerPage
	if end > len(s.titles) {
		end = len(s.titles)
	}
	text := fmt.Sprintf("㊂ <b><u>Poster Search :</u></b>\n\n"+
		"<blockquote><b>╭</b> <b>Query :</b> <code>%s</code>\n"+
		"┠ <b>Results :</b> <code>%d</code>\n"+
		"<b>╰</b> <b>Page :</b> <code>%d/%d</code></blockquote>\n\n"+
		"<blockquote>➲ Press a title to browse its posters & backdrops</blockquote>",
		telegram.EscapeHTML(posterShort(s.query, 80)), len(s.titles), s.page+1, pages)
	btn := telegram.NewButtonMaker()
	for i := start; i < end; i++ {
		t := s.titles[i]
		btn.IButtonStyled(fmt.Sprintf("%s (%s)", posterShort(t.Name, 48), posterYear(t.Year)), fmt.Sprintf("pstr %s o %d", sid, i), "primary")
		btn.Row()
	}
	pagerRow(btn, "pstr "+sid+" r ", s.page, pages)
	btn.IButtonStyled("❌", fmt.Sprintf("pstr %s x", sid), "danger")
	btn.Row()
	return text, btn
}

// posterOpen loads the images of title i and edits msgID into the image view.
func (m *LeechModule) posterOpen(ctx context.Context, peer tg.InputPeerClass, sid string, s *posterSession, i, msgID int, queryID int64) {
	t := s.titles[i]
	p, b, _, err := ext.TMDBTitleImages(ctx, t.MediaType, t.ID)
	if err != nil {
		if queryID != 0 {
			_ = m.sender.AnswerCallback(ctx, queryID, "Could not load this image, try another one.", true)
		}
		return
	}
	if len(p)+len(b) == 0 {
		if queryID != 0 {
			_ = m.sender.AnswerCallback(ctx, queryID, "No images on TMDB for this title.", true)
		}
		return
	}
	posterMu.Lock()
	s.sel, s.images, s.idx = i, map[string][]ext.TMDBImage{"p": p, "b": b}, 0
	s.kind = "p"
	if len(p) == 0 {
		s.kind = "b"
	}
	if s.lang != "all" && posterLangCount(s)[s.lang] == 0 {
		s.lang = "all"
	}
	text, url, btn := posterImageView(sid, s)
	posterMu.Unlock()
	if queryID != 0 {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	}
	_ = m.sender.EditWithPreview(ctx, peer, msgID, text, url, btn, 5)
}

func langKey(code string) string {
	if code == "" {
		return "xx"
	}
	return code
}

func posterLangCount(s *posterSession) map[string]int {
	c := map[string]int{}
	for _, im := range s.images[s.kind] {
		c[langKey(im.Lang)]++
	}
	return c
}

// posterLangs: the languages of the current kind, English first, then his language, then no
// text, then by count (like poster_lang_counts of the rename bot).
func posterLangs(s *posterSession) []string {
	c := posterLangCount(s)
	var out []string
	for k := range c {
		out = append(out, k)
	}
	rank := func(k string) int {
		switch {
		case k == "en":
			return 0
		case k == s.userL && k != "":
			return 1
		case k == "xx":
			return 2
		}
		return 3
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		if c[out[i]] != c[out[j]] {
			return c[out[i]] > c[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

func posterFiltered(s *posterSession) []ext.TMDBImage {
	all := s.images[s.kind]
	if s.lang == "all" {
		return all
	}
	var out []ext.TMDBImage
	for _, im := range all {
		if langKey(im.Lang) == s.lang {
			out = append(out, im)
		}
	}
	return out
}

// posterImageView: the text, the preview URL and the buttons of the current image.
func posterImageView(sid string, s *posterSession) (string, string, *telegram.ButtonMaker) {
	t := s.titles[s.sel]
	list := posterFiltered(s)
	n := len(list)
	if n == 0 {
		s.lang, list = "all", s.images[s.kind]
		n = len(list)
	}
	s.idx = (s.idx%n + n) % n
	im := list[s.idx]
	orient := "Portrait"
	if im.Width >= im.Height {
		orient = "Landscape"
	}
	tmdbURL := fmt.Sprintf("https://www.themoviedb.org/%s/%d", t.MediaType, t.ID)
	text := fmt.Sprintf("<b>%s (%s)</b>\n\n"+
		"<blockquote><b>╭</b> <b>TMDB :</b> <a href='%s'>tmdb.org/%s/%d</a>\n"+
		"┠ <b>Type :</b> <i>%s · %s</i>\n"+
		"┠ <b>Language :</b> <i>%s</i>\n"+
		"┠ <b>Size :</b> <code>%d x %d</code>\n"+
		"<b>╰</b> <b>Image :</b> <code>%d/%d</code></blockquote>",
		telegram.EscapeHTML(t.Name), posterYear(t.Year), tmdbURL, t.MediaType, t.ID,
		posterKindName[s.kind], orient, ext.TMDBLangName(im.Lang), im.Width, im.Height, s.idx+1, n)

	btn := telegram.NewButtonMaker()
	for _, k := range []string{"p", "b"} {
		if c := len(s.images[k]); c > 0 {
			markBtn(btn, s.kind == k, fmt.Sprintf("%s (%d)", posterKindPlural[k], c), fmt.Sprintf("pstr %s k %s", sid, k))
		}
	}
	btn.Row()
	counts := posterLangCount(s)
	langs := append([]string{"all"}, posterLangs(s)...)
	shown := langs
	if len(shown) > 4 {
		shown = langs[:4]
		in := false
		for _, l := range shown {
			if l == s.lang {
				in = true
			}
		}
		if !in {
			shown = append(append([]string{}, langs[:3]...), s.lang)
		}
	}
	for _, l := range shown {
		label := strings.ToUpper(l)
		c := counts[l]
		switch l {
		case "all":
			label, c = "All", len(s.images[s.kind])
		case "xx":
			label = "No Text"
		}
		markBtn(btn, s.lang == l, fmt.Sprintf("%s (%d)", label, c), fmt.Sprintf("pstr %s l %s", sid, l))
	}
	if len(langs) > len(shown) {
		btn.IButton("More", fmt.Sprintf("pstr %s L", sid))
	}
	btn.Row()
	pagerRow(btn, "pstr "+sid+" i ", s.idx, n)
	btn.IButtonStyled("Send Original", fmt.Sprintf("pstr %s g", sid), "primary")
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("pstr %s b", sid), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("pstr %s x", sid), "danger")
	btn.Row()
	return text, ext.TMDBImageURL(im.Path, "original"), btn
}

func posterLangView(sid string, s *posterSession) (string, *telegram.ButtonMaker) {
	counts := posterLangCount(s)
	btn := telegram.NewButtonMaker()
	markBtn(btn, s.lang == "all", fmt.Sprintf("All (%d)", len(s.images[s.kind])), fmt.Sprintf("pstr %s l all", sid))
	n := 1
	for _, l := range posterLangs(s) {
		name := ext.TMDBLangName(l)
		if l == "xx" {
			name = "No Text"
		}
		markBtn(btn, s.lang == l, fmt.Sprintf("%s (%d)", name, counts[l]), fmt.Sprintf("pstr %s l %s", sid, l))
		n++
		if n%3 == 0 {
			btn.Row()
		}
	}
	btn.Row()
	btn.IButtonStyled("◀️", fmt.Sprintf("pstr %s v", sid), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("pstr %s x", sid), "danger")
	btn.Row()
	text := "㊂ <b><u>Poster Language : " + telegram.EscapeHTML(s.titles[s.sel].Name) + "</u></b>\n\n" +
		"<blockquote>➲ Choose which images to show. The number is how many images exist in that language.</blockquote>"
	return text, btn
}

var posterSafeName = regexp.MustCompile(`[^\w.-]+`)

// HandlePosterCallback: "pstr <session> <action> [arg]".
func (m *LeechModule) HandlePosterCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	sid, act, arg := parts[1], parts[2], ""
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
		_ = m.sender.AnswerCallback(ctx, queryID, "This search expired. Run /p again.", true)
		return
	}
	if fromUID != s.uid {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	if act == "x" {
		_ = m.sender.AnswerCallback(ctx, queryID, "Closed", false)
		m.sender.DeleteWithCommand(ctx, peer, msgID) // like every ❌ of the bot: the command goes too
		posterMu.Lock()
		delete(posterSessions, sid)
		posterMu.Unlock()
		return
	}
	posterMu.Lock()
	if s.busy {
		posterMu.Unlock()
		_ = m.sender.AnswerCallback(ctx, queryID, "⏳", false)
		return
	}
	s.busy = true
	posterMu.Unlock()
	defer func() {
		posterMu.Lock()
		s.busy = false
		posterMu.Unlock()
	}()

	show := func() {
		posterMu.Lock()
		text, url, btn := posterImageView(sid, s)
		posterMu.Unlock()
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		_ = m.sender.EditWithPreview(ctx, peer, msgID, text, url, btn, 5)
	}
	num, numErr := strconv.Atoi(arg)
	switch act {
	case "r": // a page of results
		posterMu.Lock()
		if numErr == nil {
			s.page = num
		}
		text, btn := posterResultsView(sid, s)
		posterMu.Unlock()
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		_ = m.sender.EditWithPreview(ctx, peer, msgID, text, "", btn, 1)
		return
	case "o": // a title
		if numErr != nil || num < 0 || num >= len(s.titles) {
			_ = m.sender.AnswerCallback(ctx, queryID, "", false)
			return
		}
		m.posterOpen(ctx, peer, sid, s, num, msgID, queryID)
		return
	case "b": // back to the results
		posterMu.Lock()
		text, btn := posterResultsView(sid, s)
		posterMu.Unlock()
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		_ = m.sender.EditWithPreview(ctx, peer, msgID, text, "", btn, 1)
		return
	}
	if s.sel < 0 {
		_ = m.sender.AnswerCallback(ctx, queryID, "This search expired. Run /p again.", true)
		return
	}
	switch act {
	case "i": // an image
		posterMu.Lock()
		if numErr == nil {
			s.idx = num
		}
		posterMu.Unlock()
		show()
	case "k": // Posters / Backdrops
		posterMu.Lock()
		ok := len(s.images[arg]) > 0
		if ok {
			s.kind, s.idx = arg, 0
			if s.lang != "all" && posterLangCount(s)[s.lang] == 0 {
				s.lang = "all"
			}
		}
		posterMu.Unlock()
		if !ok {
			_ = m.sender.AnswerCallback(ctx, queryID, "No images on TMDB for this title.", true)
			return
		}
		show()
	case "l": // a language
		posterMu.Lock()
		ok := arg == "all" || posterLangCount(s)[arg] > 0
		if ok {
			s.lang, s.idx = arg, 0
		}
		posterMu.Unlock()
		if !ok {
			_ = m.sender.AnswerCallback(ctx, queryID, "No images on TMDB for this title.", true)
			return
		}
		show()
	case "L": // all the languages
		posterMu.Lock()
		text, btn := posterLangView(sid, s)
		posterMu.Unlock()
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		_ = m.sender.EditWithPreview(ctx, peer, msgID, text, "", btn, 3)
	case "v":
		show()
	case "g": // the original image as a file
		posterMu.Lock()
		list := posterFiltered(s)
		if len(list) == 0 {
			list = s.images[s.kind]
		}
		im := list[s.idx%len(list)]
		t, kind := s.titles[s.sel], s.kind
		posterMu.Unlock()
		_ = m.sender.AnswerCallback(ctx, queryID, "Sending the original file...", false)
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		req, _ := http.NewRequestWithContext(dctx, "GET", ext.TMDBImageURL(im.Path, "original"), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 30<<20))
		if err != nil || len(body) == 0 {
			return
		}
		orient := "Portrait"
		if im.Width >= im.Height {
			orient = "Landscape"
		}
		safe := strings.Trim(posterSafeName.ReplaceAllString(t.Name, "_"), "_")
		if safe == "" {
			safe = "poster"
		}
		safe = posterShort(safe, 60)
		yr := t.Year
		if yr == "" {
			yr = "NA"
		}
		name := fmt.Sprintf("%s_%s_%s_%dx%d.jpg", safe, yr, posterKindName[kind], im.Width, im.Height)
		caption := fmt.Sprintf("<b>%s (%s)</b>\n<blockquote><b>╰</b> <code>%s · %s · %dx%d · %s</code></blockquote>",
			telegram.EscapeHTML(t.Name), posterYear(t.Year), posterKindName[kind], orient, im.Width, im.Height, ext.TMDBLangName(im.Lang))
		_, _ = m.sender.SendDocumentBytes(context.Background(), peer, name, body, caption, msgID, nil)
	default:
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
	}
}
