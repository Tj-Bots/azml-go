package modules

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
	"azml-go/helper/telegram"
	"azml-go/types"

	"github.com/gotd/td/tg"
)

// YtdlModule: /ytdl like the Python bot — pick a quality from buttons, then the
// download runs through the normal leech pipeline (splits, media group, dump, mirror...).
type YtdlModule struct {
	sender  *telegram.MessageSender
	userMap *types.SafeUserMap
	leech   *LeechModule
}

func NewYtdlModule(sender *telegram.MessageSender, leech *LeechModule, userMap *types.SafeUserMap) *YtdlModule {
	return &YtdlModule{sender: sender, userMap: userMap, leech: leech}
}

type ytdlGroup struct {
	name string     // e.g. 1080p60-mp4 or opus-webm
	tbrs []ytdlRate // bit rates of this group
}

type ytdlRate struct {
	tbr    float64
	size   int64
	format string
}

type ytdlPending struct {
	msg     *types.MessageContext
	link    string
	peer    tg.InputPeerClass
	created time.Time
	info    *downloader.YtFormatInfo
	groups  []ytdlGroup
	msgID   int
	uid     int64
	key     string
}

var (
	ytdlMu      sync.Mutex
	ytdlPendMap = map[string]*ytdlPending{}
)

const ytdlTimeout = 2 * time.Minute

var ytdlQualityRe = regexp.MustCompile(`(?i)^(\d{3,4}p?|best|audio|mp3)$`)

// parseYtdlArgs finds the link and an optional quality ("720p", "best", "audio", "mp3",
// or "-q <yt-dlp format>") in the command text.
func parseYtdlArgs(text string) (link, quality string) {
	tokens := splitArgsRespectingQuotes(text)
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == "-q" || tok == "-opt" || tok == "-f":
			if i+1 < len(tokens) {
				quality = tokens[i+1]
				i++
			}
		case ext.IsURL(tok):
			if link == "" {
				link = tok
			}
		case ytdlQualityRe.MatchString(tok) && quality == "":
			quality = strings.ToLower(tok)
		}
	}
	return link, quality
}

func (m *YtdlModule) HandleYtdl(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	if msgText, btn := ext.CheckingAccess(msg.UserID, false, telegram.CurrentBotUsername, m.userMap); msgText != "" {
		var markup tg.ReplyMarkupClass
		if btn != nil {
			markup = btn.BuildMenu(1)
		}
		_, _ = m.sender.SendMessage(ctx, peer, msgText, msg.MessageID, markup)
		m.leech.deleteCommandLinks(peer, msg)
		return
	}

	if !m.leech.forceSubOK(ctx, peer, msg, func() { m.HandleYtdl(context.Background(), peer, msg) }) {
		return
	}
	link, quality := parseYtdlArgs(msg.Text)
	if link == "" && msg.IsReply && msg.ReplyText != "" {
		for _, w := range strings.Fields(msg.ReplyText) {
			if ext.IsURL(w) {
				link = w
				break
			}
		}
	}
	if link == "" {
		m.leech.sendCmdHelp(ctx, peer, msg, "ytdl")
		return
	}

	m.applyMirrorFlag(msg)

	if !m.leech.timeIntervalOK(ctx, peer, msg) || !m.leech.botTasksOK(ctx, peer, msg) || !m.leech.userTasksOK(ctx, peer, msg) {
		return
	}

	if quality != "" {
		m.start(peer, msg, link, quality)
		return
	}

	// quality buttons like the Python bot
	waitID, _ := m.sender.SendMessage(ctx, peer, "<i>"+telegram.ToSmallCaps("Fetching available qualities")+"...</i>", msg.MessageID, nil)
	go m.showQualities(peer, msg, link, waitID)
}

// applyMirrorFlag: "-up host" sends the result to a DDL host (like /mirror).
func (m *YtdlModule) applyMirrorFlag(msg *types.MessageContext) {
	tokens := strings.Fields(msg.Text)
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == "-up" {
			if h, ok := ext.FindMirrorHost(strings.ToLower(tokens[i+1])); ok {
				msg.IsMirror = true
				msg.MirrorHost = h.ID
			}
		}
	}
}

func (m *YtdlModule) showQualities(peer tg.InputPeerClass, msg *types.MessageContext, link string, waitID int) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var extra []string
	if cf := writeUserCookies(m.userMap.Get(msg.UserID)); cf != "" {
		defer os.Remove(cf)
		extra = append(extra, "--cookies", cf)
	}
	info, err := downloader.NewYtDlpDownloader().GetFormats(ctx, link, extra...)
	if err != nil {
		text := fmt.Sprintf("㊂ <b><u>YT-DLP Error :</u></b>\n\n<blockquote>%s</blockquote>", telegram.EscapeHTML(trimErr(err.Error())))
		if waitID > 0 {
			_ = m.sender.EditMessage(ctx, peer, waitID, text, telegram.CloseMarkup(msg.UserID))
		} else {
			waitID, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, telegram.CloseMarkup(msg.UserID))
		}
		m.leech.deleteCommandLinks(peer, msg)
		autoDeletePair(m.sender, peer, waitID, msg.MessageID) // a short error: it goes too
		return
	}

	p := &ytdlPending{msg: msg, link: link, peer: peer, created: time.Now(), info: info,
		groups: ytdlGroups(info.Formats), uid: msg.UserID, key: ext.RandomHex(4)}
	ytdlMu.Lock()
	for k, old := range ytdlPendMap {
		if time.Since(old.created) > ytdlTimeout*2 {
			delete(ytdlPendMap, k)
		}
	}
	ytdlPendMap[p.key] = p
	ytdlMu.Unlock()

	text, btn := p.mainMenu()
	if waitID > 0 {
		_ = m.sender.EditMessageWithButtons(ctx, peer, waitID, text, btn, 2)
	} else {
		waitID, _ = m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 2)
	}
	p.msgID = waitID

	// timeout like the Python bot
	time.AfterFunc(ytdlTimeout, func() {
		ytdlMu.Lock()
		_, still := ytdlPendMap[p.key]
		delete(ytdlPendMap, p.key)
		ytdlMu.Unlock()
		if still && waitID > 0 {
			_ = m.sender.EditMessage(context.Background(), peer, waitID, "㊂ <b><u>YT-DLP :</u></b>\n\n<blockquote>Timed Out. Task has been cancelled!</blockquote>", nil)
			m.sender.AutoDeleteMessage(peer, waitID, 20)
		}
	})
}

// ytdlGroups groups the formats like the Python bot: "<height>p<fps>-<ext>" for video,
// "<acodec>-<ext>" for audio, every group with its bit rates.
func ytdlGroups(formats []downloader.YtFormat) []ytdlGroup {
	var groups []ytdlGroup
	idx := map[string]int{}
	isM4a := false
	for _, f := range formats {
		if f.Tbr <= 0 {
			continue
		}
		size := f.Size
		if size == 0 {
			size = f.Approx
		}
		var name, format string
		switch {
		case f.VideoExt == "none" && f.Acodec != "" && f.Acodec != "none":
			if f.AudioExt == "m4a" {
				isM4a = true
			}
			name = f.Acodec + "-" + f.Ext
			format = f.ID
		case f.Height > 0:
			fps := ""
			if f.Fps > 0 {
				fps = fmt.Sprintf("%d", int(f.Fps))
			}
			name = fmt.Sprintf("%dp%s-%s", f.Height, fps, f.Ext)
			baExt := ""
			if isM4a && f.Ext == "mp4" {
				baExt = "[ext=m4a]"
			}
			format = fmt.Sprintf("%s+ba%s/b[height=?%d]", f.ID, baExt, f.Height)
		default:
			continue
		}
		i, ok := idx[name]
		if !ok {
			i = len(groups)
			idx[name] = i
			groups = append(groups, ytdlGroup{name: name})
		}
		groups[i].tbrs = append(groups[i].tbrs, ytdlRate{tbr: f.Tbr, size: size, format: format})
	}
	return groups
}

func (p *ytdlPending) cb(parts ...string) string {
	return fmt.Sprintf("ytq %d %s %s", p.uid, p.key, strings.Join(parts, " "))
}

func (p *ytdlPending) header(title string) string {
	left := ytdlTimeout - time.Since(p.created)
	if left < 0 {
		left = 0
	}
	name := "Media"
	if p.info != nil && p.info.Title != "" {
		name = p.info.Title
	}
	var sb strings.Builder
	sb.WriteString("㊂ <b><u>" + title + " :</u></b>\n\n<blockquote>")
	sb.WriteString(fmt.Sprintf("<b>╭</b> <b>Title :</b> %s\n", telegram.EscapeHTML(name)))
	if p.info != nil && p.info.IsPlaylist {
		sb.WriteString(fmt.Sprintf("┠ <b>Playlist :</b> %d items\n", p.info.Count))
	} else if p.info != nil && p.info.Duration > 0 {
		sb.WriteString(fmt.Sprintf("┠ <b>Duration :</b> %s\n", ext.GetReadableTime(int64(p.info.Duration))))
	}
	sb.WriteString(fmt.Sprintf("<b>╰</b> <b>Timeout :</b> %s</blockquote>", ext.GetReadableTime(int64(left.Seconds()))))
	return sb.String()
}

// footer: buttons on their own last row.
func (p *ytdlPending) footer(btn *telegram.ButtonMaker, back string) {
	btn.Row()
	if back != "" {
		btn.IButtonStyled("◀️", p.cb(back), "primary")
	}
	btn.IButtonStyled("❌", p.cb("cancel"), "danger")
	btn.Row()
}

// grid adds buttons with cols per row.
func grid(btn *telegram.ButtonMaker, cols int, items [][2]string) {
	for i, it := range items {
		btn.IButton(it[0], it[1])
		if (i+1)%cols == 0 {
			btn.Row()
		}
	}
	btn.Row()
}

func (p *ytdlPending) mainMenu() (string, *telegram.ButtonMaker) {
	btn := telegram.NewButtonMaker()
	var items [][2]string
	if p.info != nil && p.info.IsPlaylist {
		for _, h := range []string{"144", "240", "360", "480", "720", "1080", "1440", "2160"} {
			items = append(items, [2]string{h + "-mp4", p.cb("pl", h, "mp4")}, [2]string{h + "-webm", p.cb("pl", h, "webm")})
		}
		items = append(items, [2]string{"MP3", p.cb("mp3")}, [2]string{"Audio Formats", p.cb("audio")},
			[2]string{"Best Videos", p.cb("fmt", "bv")}, [2]string{"Best Audios", p.cb("fmt", "ba")})
		grid(btn, 3, items)
		p.footer(btn, "")
		return p.header("Choose Playlist Videos Quality"), btn
	}
	for i, g := range p.groups {
		if len(g.tbrs) == 1 {
			items = append(items, [2]string{fmt.Sprintf("%s (%s)", g.name, ext.GetReadableFileSize(g.tbrs[0].size)), p.cb("sel", fmt.Sprint(i), "0")})
		} else {
			items = append(items, [2]string{g.name, p.cb("dict", fmt.Sprint(i))})
		}
	}
	items = append(items, [2]string{"MP3", p.cb("mp3")}, [2]string{"Audio Formats", p.cb("audio")},
		[2]string{"Best Video", p.cb("fmt", "bv")}, [2]string{"Best Audio", p.cb("fmt", "ba")})
	grid(btn, 2, items)
	p.footer(btn, "")
	return p.header("Choose Video Quality"), btn
}

func (p *ytdlPending) bitrateMenu(i int) (string, *telegram.ButtonMaker) {
	btn := telegram.NewButtonMaker()
	var items [][2]string
	g := p.groups[i]
	for j, r := range g.tbrs {
		items = append(items, [2]string{fmt.Sprintf("%gK (%s)", r.tbr, ext.GetReadableFileSize(r.size)), p.cb("sel", fmt.Sprint(i), fmt.Sprint(j))})
	}
	grid(btn, 2, items)
	p.footer(btn, "back")
	return p.header("Choose Bit rate for " + telegram.EscapeHTML(g.name)), btn
}

func (p *ytdlPending) mp3Menu() (string, *telegram.ButtonMaker) {
	btn := telegram.NewButtonMaker()
	var items [][2]string
	for _, q := range []string{"64", "128", "320"} {
		items = append(items, [2]string{q + "K-mp3", p.cb("q", "ba/b-mp3-"+q)})
	}
	grid(btn, 3, items)
	p.footer(btn, "back")
	return p.header("Choose MP3 Audio Bitrate"), btn
}

func (p *ytdlPending) audioMenu() (string, *telegram.ButtonMaker) {
	btn := telegram.NewButtonMaker()
	var items [][2]string
	for _, f := range []string{"aac", "alac", "flac", "m4a", "opus", "vorbis", "wav"} {
		items = append(items, [2]string{f, p.cb("aqf", f)})
	}
	grid(btn, 3, items)
	p.footer(btn, "back")
	return p.header("Choose Audio Format"), btn
}

func (p *ytdlPending) audioQualityMenu(f string) (string, *telegram.ButtonMaker) {
	btn := telegram.NewButtonMaker()
	var items [][2]string
	for q := 0; q <= 10; q++ {
		items = append(items, [2]string{fmt.Sprint(q), p.cb("q", fmt.Sprintf("ba/b-%s-%d", f, q))})
	}
	grid(btn, 5, items)
	p.footer(btn, "audio")
	return p.header("Choose "+strings.ToUpper(f)+" Audio Quality") + "\n\n➲ <i>0 is best and 10 is worst</i>", btn
}

// HandleYtdlCallback: "ytq <uid> <key> <action> [args]".
func (m *YtdlModule) HandleYtdlCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 4 || parts[1] != fmt.Sprintf("%d", fromUID) {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	ytdlMu.Lock()
	p, ok := ytdlPendMap[parts[2]]
	ytdlMu.Unlock()
	if !ok {
		_ = m.sender.AnswerCallback(ctx, queryID, "This task is not available anymore!", true)
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}
	arg := func(i int) string {
		if len(parts) > 4+i {
			return parts[4+i]
		}
		return ""
	}
	show := func(text string, btn *telegram.ButtonMaker) {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		_ = m.sender.EditMessageWithButtons(ctx, peer, msgID, text, btn, 2)
	}
	quality := ""
	switch parts[3] {
	case "cancel":
		ytdlMu.Lock()
		delete(ytdlPendMap, p.key)
		ytdlMu.Unlock()
		_ = m.sender.AnswerCallback(ctx, queryID, "Task has been cancelled!", false)
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	case "back":
		show(p.mainMenu())
		return
	case "dict":
		i, err := strconv.Atoi(arg(0))
		if err != nil || i < 0 || i >= len(p.groups) {
			show(p.mainMenu())
			return
		}
		show(p.bitrateMenu(i))
		return
	case "mp3":
		show(p.mp3Menu())
		return
	case "audio":
		show(p.audioMenu())
		return
	case "aqf":
		show(p.audioQualityMenu(arg(0)))
		return
	case "sel":
		i, err1 := strconv.Atoi(arg(0))
		j, err2 := strconv.Atoi(arg(1))
		if err1 != nil || err2 != nil || i < 0 || i >= len(p.groups) || j < 0 || j >= len(p.groups[i].tbrs) {
			show(p.mainMenu())
			return
		}
		quality = p.groups[i].tbrs[j].format
	case "pl":
		h, e := arg(0), arg(1)
		if e == "webm" {
			quality = fmt.Sprintf("bv*[height<=?%s][ext=webm]+ba/b[height<=?%s]", h, h)
		} else {
			quality = fmt.Sprintf("bv*[height<=?%s][ext=mp4]+ba[ext=m4a]/b[height<=?%s]", h, h)
		}
	case "fmt":
		if arg(0) == "ba" {
			quality = "ba/b"
		} else {
			quality = "bv*+ba/b"
		}
	case "q":
		quality = arg(0)
	}
	if quality == "" {
		_ = m.sender.AnswerCallback(ctx, queryID, "", false)
		return
	}
	ytdlMu.Lock()
	delete(ytdlPendMap, p.key)
	ytdlMu.Unlock()
	_ = m.sender.AnswerCallback(ctx, queryID, "Starting...", false)
	_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
	m.start(p.peer, p.msg, p.link, quality)
}

func (m *YtdlModule) start(peer tg.InputPeerClass, msg *types.MessageContext, link, quality string) {
	msg.YtdlQuality = quality
	m.leech.StartYtdl(peer, msg, link, quality)
}

// StartYtdl runs a yt-dlp download through the leech pipeline.
func (m *LeechModule) StartYtdl(peer tg.InputPeerClass, msg *types.MessageContext, link, quality string) {
	ctx := context.Background()
	cfg := config.Get()
	opt, _ := parseLeechArgs(msg.Text)
	opt.IsMirror = msg.IsMirror
	opt.MirrorHost = msg.MirrorHost
	opt.Ytdl = true
	opt.YtdlFormat, opt.YtdlArgs = downloader.YtQuality(quality)
	msg.YtdlQuality = quality
	if g, _ := safeYtOpts(cfg.YtDlpOptions); len(g) > 0 { // YT_DLP_OPTIONS for everyone
		opt.YtdlArgs = append(append([]string{}, opt.YtdlArgs...), g...)
	}
	if cfg.PlaylistLimit > 0 {
		opt.YtdlArgs = append(opt.YtdlArgs, "--playlist-end", strconv.Itoa(cfg.PlaylistLimit))
	}
	cookieFile := ""
	if u := m.userMap.Get(msg.UserID); u != nil {
		extra, _ := safeYtOpts(u.YtOpt)
		opt.YtdlArgs = append(append([]string{}, opt.YtdlArgs...), extra...)
		cookieFile = writeUserCookies(u)
		if cookieFile != "" {
			opt.YtdlArgs = append(opt.YtdlArgs, "--cookies", cookieFile)
		}
	}

	taskDir := filepath.Join(cfg.DownloadDir, fmt.Sprintf("yt_%d_%s", msg.UserID, ext.RandomHex(4)))
	if err := os.MkdirAll(taskDir, 0777); err != nil {
		log.Printf("[YTDL] Failed to create taskDir %s: %v, falling back to temp dir", taskDir, err)
		taskDir = filepath.Join(os.TempDir(), "downloads", fmt.Sprintf("yt_%d_%s", msg.UserID, ext.RandomHex(4)))
		_ = os.MkdirAll(taskDir, 0777)
	}

	notifyID, _ := m.sender.SendMessage(ctx, peer, fmt.Sprintf("<i>%s...</i>", telegram.ToSmallCaps("Initiating YT-DLP Task")), msg.MessageID, nil)
	if notifyID > 0 && m.statusMod != nil {
		m.statusMod.TakeOverStatus(msg.ChatID, notifyID, peer)
	}
	go func() {
		m.processLeechPipeline(peer, msg, opt, link, taskDir, notifyID)
		if cookieFile != "" {
			_ = os.Remove(cookieFile)
		}
	}()
}

// writeUserCookies puts the user's cookies.txt in a temp file (outside the task folder).
func writeUserCookies(u *types.UserSettings) string {
	if u == nil || u.YtCookies == "" {
		return ""
	}
	f, err := os.CreateTemp("", "ytc_*.txt")
	if err != nil {
		return ""
	}
	_, _ = f.WriteString(u.YtCookies)
	_ = f.Close()
	return f.Name()
}

// safeYtOpts splits the user's yt-dlp options and drops the ones that could run commands,
// read or write files on the server.
func safeYtOpts(s string) (allowed, dropped []string) {
	blocked := []string{"--exec", "--config", "--batch-file", "--output", "--paths", "--load-info",
		"--plugin", "--external-downloader", "--downloader", "--postprocessor-args", "--ppa",
		"--cookies", "--print-to-file", "--write", "--cache-dir", "--ffmpeg-location", "--netrc",
		"--use-postprocessor", "--alias", "--download-archive", "--compat-options", "--update", "-U"}
	okShort := map[string]bool{"-f": true, "-S": true, "-x": true, "-k": true, "-N": true, "-R": true,
		"-r": true, "-4": true, "-6": true, "-i": true, "-w": true, "-F": false}
	tokens := splitArgsRespectingQuotes("x " + s)[1:]
	skipValue := false
	for _, t := range tokens {
		if !strings.HasPrefix(t, "-") {
			if skipValue {
				dropped = append(dropped, t)
				continue
			}
			allowed = append(allowed, t)
			continue
		}
		skipValue = false
		bad := false
		if strings.HasPrefix(t, "--") {
			name := strings.ToLower(strings.SplitN(t, "=", 2)[0])
			for _, b := range blocked {
				if strings.HasPrefix(name, b) {
					bad = true
					break
				}
			}
		} else if !okShort[t] {
			bad = true
		}
		if bad {
			dropped = append(dropped, t)
			skipValue = true // its value goes too
			continue
		}
		allowed = append(allowed, t)
	}
	return allowed, dropped
}

func trimErr(s string) string {
	if len(s) > 600 {
		s = s[:600] + "..."
	}
	return s
}
