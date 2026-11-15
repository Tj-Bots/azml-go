package modules

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
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

type ytdlPending struct {
	msg     *types.MessageContext
	link    string
	peer    tg.InputPeerClass
	created time.Time
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
		helpMsg := "㊂ <b><u>YT-DLP Command Usage :</u></b>\n\n" +
			"<b>Usage:</b> <code>/ytdl [link] [quality]</code> or reply to a link\n\n" +
			"<blockquote>╭ <code>/ytdl link</code> — Choose the quality from buttons\n" +
			"┠ <code>720p</code> — Direct quality (144p - 2160p)\n" +
			"┠ <code>best</code> — Best video + audio\n" +
			"┠ <code>audio</code> / <code>mp3</code> — Audio only\n" +
			"┠ <code>-q format</code> — Custom yt-dlp format\n" +
			"┠ <code>-n name</code> — Custom File Name\n" +
			"┠ <code>-z</code> — Compress as Zip\n" +
			"┠ <code>-d</code> / <code>-m</code> — Upload as Document / Media\n" +
			"╰ <code>-up gofile</code> — Mirror to a host</blockquote>\n\n" +
			"➲ <b>Supported :</b> YouTube, Instagram, TikTok, X, Facebook and more\n" +
			"➲ <b>Cookies :</b> <code>cookies.txt</code> in the bot folder is used when present"
		_, _ = m.sender.SendMessage(ctx, peer, helpMsg, msg.MessageID, telegram.CloseMarkup(msg.UserID))
		return
	}

	m.applyMirrorFlag(msg)

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
			_, _ = m.sender.SendMessage(ctx, peer, text, msg.MessageID, telegram.CloseMarkup(msg.UserID))
		}
		return
	}

	key := ext.RandomHex(4)
	ytdlMu.Lock()
	for k, p := range ytdlPendMap {
		if time.Since(p.created) > ytdlTimeout*2 {
			delete(ytdlPendMap, k)
		}
	}
	ytdlPendMap[key] = &ytdlPending{msg: msg, link: link, peer: peer, created: time.Now()}
	ytdlMu.Unlock()

	text, btn := buildYtdlMenu(info, msg.UserID, key)
	if waitID > 0 {
		_ = m.sender.EditMessageWithButtons(ctx, peer, waitID, text, btn, 2)
	} else {
		waitID, _ = m.sender.SendMessageWithButtons(ctx, peer, text, msg.MessageID, btn, 2)
	}

	// timeout like the Python bot
	time.AfterFunc(ytdlTimeout, func() {
		ytdlMu.Lock()
		_, still := ytdlPendMap[key]
		delete(ytdlPendMap, key)
		ytdlMu.Unlock()
		if still && waitID > 0 {
			_ = m.sender.EditMessage(context.Background(), peer, waitID, "㊂ <b><u>YT-DLP :</u></b>\n\n<blockquote>Timed out. Task has been cancelled!</blockquote>", nil)
			m.sender.AutoDeleteMessage(peer, waitID, 20)
		}
	})
}

func buildYtdlMenu(info *downloader.YtFormatInfo, uid int64, key string) (string, *telegram.ButtonMaker) {
	var sb strings.Builder
	sb.WriteString("㊂ <b><u>YT-DLP Quality :</u></b>\n\n<blockquote>")
	title := info.Title
	if title == "" {
		title = "Media"
	}
	sb.WriteString(fmt.Sprintf("╭ <b>Title :</b> %s\n", telegram.EscapeHTML(title)))
	if info.IsPlaylist {
		sb.WriteString(fmt.Sprintf("┠ <b>Playlist :</b> %d items\n", info.Count))
	} else if info.Duration > 0 {
		sb.WriteString(fmt.Sprintf("┠ <b>Duration :</b> %s\n", ext.GetReadableTime(int64(info.Duration))))
	}
	sb.WriteString(fmt.Sprintf("╰ <b>Timeout :</b> %s</blockquote>\n\n➲ <i>Choose the quality :</i>", ext.GetReadableTime(int64(ytdlTimeout.Seconds()))))

	btn := telegram.NewButtonMaker()
	cb := func(choice string) string { return fmt.Sprintf("ytq %d %s %s", uid, key, choice) }
	heights := info.Heights
	if info.IsPlaylist || len(heights) == 0 {
		heights = []int{1080, 720, 480, 360}
	}
	shown := 0
	for _, h := range heights {
		if h < 144 || shown >= 8 {
			continue
		}
		label := fmt.Sprintf("%dp", h)
		if s := info.Sizes[h]; s > 0 {
			label += " [" + ext.GetReadableFileSize(s+info.AudioSize) + "]"
		}
		btn.IButton(label, cb(fmt.Sprintf("%d", h)))
		shown++
	}
	if info.IsPlaylist {
		btn.IButton("Best Videos", cb("best"))
		btn.IButton("Best Audios", cb("audio"))
	} else {
		btn.IButton("Best Video", cb("best"))
		btn.IButton("Best Audio", cb("audio"))
	}
	btn.IButton("MP3 320K", cb("mp3"))
	btn.IButtonStyled("❌", cb("cancel"), "danger")
	return sb.String(), btn
}

// HandleYtdlCallback: "ytq <uid> <key> <choice>".
func (m *YtdlModule) HandleYtdlCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 4 || parts[1] != fmt.Sprintf("%d", fromUID) {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	ytdlMu.Lock()
	p, ok := ytdlPendMap[parts[2]]
	delete(ytdlPendMap, parts[2])
	ytdlMu.Unlock()
	if !ok {
		_ = m.sender.AnswerCallback(ctx, queryID, "This task is not available anymore!", true)
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}
	choice := parts[3]
	if choice == "cancel" {
		_ = m.sender.AnswerCallback(ctx, queryID, "Task has been cancelled!", false)
		_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
		return
	}
	_ = m.sender.AnswerCallback(ctx, queryID, "Starting...", false)
	_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
	m.start(p.peer, p.msg, p.link, choice)
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

	isFirstStatus := false
	if m.statusMod != nil {
		isFirstStatus = m.statusMod.ReserveStatusSlot(msg.ChatID)
	}
	notifyID, _ := m.sender.SendMessage(ctx, peer, fmt.Sprintf("<i>%s...</i>", telegram.ToSmallCaps("Initiating YT-DLP Task")), msg.MessageID, nil)
	if notifyID > 0 && m.statusMod != nil {
		if isFirstStatus {
			m.statusMod.SetStatusMessage(msg.ChatID, notifyID, peer)
		} else {
			m.sender.AutoDeleteMessage(peer, notifyID, 4)
		}
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
