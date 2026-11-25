package modules

import (
	"context"
	"errors"
	"fmt"
	td "github.com/gotd/td/telegram"
	"io"
	"log"
	"maps"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
	"azml-go/helper/pool"
	"azml-go/helper/telegram"
	"azml-go/helper/uploader"
	"azml-go/types"
	"azml-go/web"

	tdDownloader "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type LeechModule struct {
	rawAPI     *tg.Client
	userRawAPI *tg.Client
	sender     *telegram.MessageSender
	uploader   *uploader.TelegramUploader
	userMap    *types.SafeUserMap
	taskMgr    *ext.TaskManager
	statusMod  *StatusModule
	sessPool   *pool.SessionPool
	dumpChatID int64
}

func NewLeechModule(rawAPI, userRawAPI *tg.Client, sender *telegram.MessageSender, uploader *uploader.TelegramUploader, userMap *types.SafeUserMap, statusMod *StatusModule, sessPool *pool.SessionPool, dumpChatID int64) *LeechModule {
	return &LeechModule{
		rawAPI:     rawAPI,
		userRawAPI: userRawAPI,
		sender:     sender,
		uploader:   uploader,
		userMap:    userMap,
		taskMgr:    ext.GetTaskManager(),
		statusMod:  statusMod,
		sessPool:   sessPool,
		dumpChatID: dumpChatID,
	}
}

func (m *LeechModule) SetSessionPool(sessPool *pool.SessionPool, dumpChatID int64) {
	m.sessPool = sessPool
	m.dumpChatID = dumpChatID
}

func (m *LeechModule) SetUserRawAPI(userRawAPI *tg.Client) {
	m.userRawAPI = userRawAPI
}

func (m *LeechModule) SetDumpChatID(dumpChatID int64) {
	m.dumpChatID = dumpChatID
}

func (m *LeechModule) HandleLeech(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, isZip, isUnzip bool) {
	msg.IsZip, msg.IsUnzip = isZip, isUnzip // kept for the Retry button
	cfg := config.Get()

	// Access control & token check
	if msgText, btn := ext.CheckingAccess(msg.UserID, false, telegram.CurrentBotUsername, m.userMap); msgText != "" {
		var markup tg.ReplyMarkupClass
		if btn != nil {
			markup = btn.BuildMenu(1)
		}
		_, _ = m.sender.SendMessage(ctx, peer, msgText, msg.MessageID, markup)
		m.deleteCommandLinks(peer, msg) // like the Python bot: the refused command goes too
		return
	}
	if !m.forceSubOK(ctx, peer, msg, func() { m.HandleLeech(context.Background(), peer, msg, isZip, isUnzip) }) {
		return
	}

	opt, link := parseLeechArgs(msg.Text)
	opt.IsMirror = msg.IsMirror
	opt.MirrorHost = msg.MirrorHost
	opt.RcloneDest = msg.RcloneDest
	if isZip {
		opt.IsZip = true
	}
	if isUnzip {
		opt.IsExtract = true
	}
	if opt.Password != "" && !opt.IsZip && opt.Username == "" {
		opt.IsExtract = true
	}
	// like WZML-X: no upload destination / token = an error before the download, not after it
	if opt.IsMirror {
		var u *types.UserSettings
		if m.userMap != nil {
			u = m.userMap.Get(msg.UserID)
		}
		if e := mirrorDestError(cfg, u, opt.MirrorHost, opt.RcloneDest); e != "" {
			_, _ = m.sender.SendMessage(ctx, peer, "㊂ <b><u>Mirror Error :</u></b>\n\n<blockquote>"+e+"</blockquote>", msg.MessageID, nil)
			return
		}
	}

	if link == "" && msg.IsReply && msg.ReplyText != "" && !msg.HasMedia && msg.RawMedia == nil {
		for _, w := range strings.Fields(msg.ReplyText) {
			if strings.HasPrefix(w, "http") || strings.HasPrefix(w, "magnet:") {
				link = w
				break
			}
		}
		if link == "" && (strings.HasPrefix(msg.ReplyText, "http") || strings.HasPrefix(msg.ReplyText, "magnet:")) {
			link = strings.TrimSpace(msg.ReplyText)
		}
	}

	// Bulk Leech Handling (-b / -bulk)
	if opt.Bulk {
		var rawLinks []string
		sourceText := ""
		if msg.IsReply && msg.ReplyText != "" && !msg.HasMedia && msg.RawMedia == nil {
			sourceText = msg.ReplyText
		} else if link != "" {
			sourceText = link
		}

		for _, l := range strings.Split(sourceText, "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "http") || strings.HasPrefix(l, "magnet:") {
				rawLinks = append(rawLinks, l)
			}
		}

		if len(rawLinks) == 0 {
			_, _ = m.sender.SendMessage(ctx, peer, "<b>Reply to a message with links separated by new line for bulk leeching!</b>", msg.MessageID, nil)
			return
		}

		bulkLinks := rawLinks
		rangeStr := opt.BulkRange
		if rangeStr == "" {
			parts := strings.Fields(msg.Text)
			for idx, p := range parts {
				if (p == "-b" || p == "-bulk") && idx+1 < len(parts) && strings.Contains(parts[idx+1], ":") {
					rangeStr = parts[idx+1]
					break
				}
			}
		}
		if rangeStr != "" && strings.Contains(rangeStr, ":") {
			rangeParts := strings.Split(rangeStr, ":")
			start, _ := strconv.Atoi(rangeParts[0])
			end, _ := strconv.Atoi(rangeParts[1])
			if start < len(rawLinks) {
				if end > 0 && end <= len(rawLinks) {
					bulkLinks = rawLinks[start:end]
				} else {
					bulkLinks = rawLinks[start:]
				}
			}
		}

		_, _ = m.sender.SendMessage(ctx, peer, fmt.Sprintf("⌬ <b>Bulk Leech Initiated:</b> <code>%d links</code>", len(bulkLinks)), msg.MessageID, nil)
		if opt.SameDir != "" && len(bulkLinks) > 1 {
			msg.SameDirKey = newSameDir(opt.SameDir, len(bulkLinks))
		}

		go func() {
			for _, bLink := range bulkLinks {
				subTaskDir := filepath.Join(cfg.DownloadDir, fmt.Sprintf("%d_%s", msg.UserID, ext.RandomHex(4)))
				if err := os.MkdirAll(subTaskDir, 0777); err != nil {
					subTaskDir = filepath.Join(os.TempDir(), "downloads", fmt.Sprintf("%d_%s", msg.UserID, ext.RandomHex(4)))
					_ = os.MkdirAll(subTaskDir, 0777)
				}

				nID, _ := m.sender.SendMessage(context.Background(), peer, fmt.Sprintf("<i>Initiating Leech:</i> <code>%s</code>", truncateString(bLink, 40)), msg.MessageID, nil)
				if nID > 0 && m.statusMod != nil {
					m.statusMod.TakeOverStatus(msg.ChatID, nID, peer)
				}
				m.processLeechPipeline(peer, msg, opt, bLink, subTaskDir, nID)
				time.Sleep(2 * time.Second)
			}
		}()
		return
	}

	// Multi-Message Sequential Leech (-i <count>)
	if opt.Multi > 1 && msg.IsReply && msg.ReplyToID > 0 && opt.SameDir != "" && msg.SameDirKey == "" {
		msg.SameDirKey = newSameDir(opt.SameDir, opt.Multi)
	}
	if opt.Multi > 1 && msg.IsReply && msg.ReplyToID > 0 {
		multiCount := opt.Multi
		baseReplyID := msg.ReplyToID
		go func() {
			for idx := 1; idx < multiCount; idx++ {
				time.Sleep(3 * time.Second)
				targetID := baseReplyID + idx
				targetMsg, err := telegram.FetchMessage(context.Background(), m.rawAPI, peer, targetID)
				if err != nil {
					log.Printf("[LEECH -i] Failed to fetch message ID %d: %v", targetID, err)
					continue
				}
				if targetMsg == nil {
					continue
				}

				nextMsgCtx := &types.MessageContext{
					ChatID:     msg.ChatID,
					MessageID:  targetMsg.ID,
					UserID:     msg.UserID,
					SenderName: msg.SenderName,
					Text:       multiFollowText(msg.Text),
					ReplyText:  targetMsg.Message,
					LangCode:   msg.LangCode,
					Username:   msg.Username,
					Internal:   true, // part of the same -i run
					SameDirKey: msg.SameDirKey,
					HasMedia:   targetMsg.Media != nil,
					RawMedia:   targetMsg.Media,
					ReplyToID:  targetID,
					IsReply:    true,
					IsMirror:   msg.IsMirror,
					MirrorHost: msg.MirrorHost,
					RcloneDest: msg.RcloneDest,
				}

				log.Printf("[LEECH -i] Sequentially triggering download for message ID %d (part %d/%d)", targetID, idx+1, multiCount)
				m.HandleLeech(context.Background(), peer, nextMsgCtx, isZip, isUnzip)
			}
		}()
	}

	if link == "" && !msg.HasMedia && !msg.IsReply && opt.IsMirror {
		m.sendCmdHelp(ctx, peer, msg, "mirror")
		return
	}
	if link == "" && !msg.HasMedia && !msg.IsReply {
		m.sendCmdHelp(ctx, peer, msg, "leech")
		return
	}

	if link != "" && !ext.IsURL(link) && !ext.IsMagnet(link) && !ext.IsMegaLink(link) && !strings.HasPrefix(link, "tg://") && !isLocalTorrent(link) {
		m.sendDownloadError(ctx, peer, msg, 0, "Invalid link or resource not found! Please check the URL.", "#Leech", time.Now())
		return
	}

	if !msg.Internal && !m.timeIntervalOK(ctx, peer, msg) {
		return
	}
	if !msg.Internal && (!m.botTasksOK(ctx, peer, msg) || !m.userTasksOK(ctx, peer, msg)) {
		return
	}

	// Create user download folder
	taskDir := filepath.Join(cfg.DownloadDir, fmt.Sprintf("%d_%s", msg.UserID, ext.RandomHex(4)))
	if err := os.MkdirAll(taskDir, 0777); err != nil {
		log.Printf("[LEECH] Failed to create taskDir %s: %v, falling back to temp dir", taskDir, err)
		taskDir = filepath.Join(os.TempDir(), "downloads", fmt.Sprintf("%d_%s", msg.UserID, ext.RandomHex(4)))
		_ = os.MkdirAll(taskDir, 0777)
	}

	notifyID, _ := m.sender.SendMessage(ctx, peer, fmt.Sprintf("<i>%s...</i>", telegram.ToSmallCaps("Initiating Leech Task")), msg.MessageID, nil)
	if notifyID > 0 && m.statusMod != nil {
		m.statusMod.TakeOverStatus(msg.ChatID, notifyID, peer)
	}

	go m.processLeechPipeline(peer, msg, opt, link, taskDir, notifyID)
}

// deleteCommandLinks: DELETE_LINKS like the Python bot (delete_links): the command and the message
// it answers go also when the task is refused or fails before it starts.
func (m *LeechModule) deleteCommandLinks(peer tg.InputPeerClass, msg *types.MessageContext) {
	if !config.Get().DeleteLinks || msg == nil || msg.Internal {
		return
	}
	ids := []int{msg.MessageID}
	if msg.IsReply && msg.ReplyToID > 0 {
		ids = append(ids, msg.ReplyToID)
	}
	go func() {
		for _, id := range ids {
			dctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = m.sender.DeleteMessage(dctx, peer, []int{id})
			cancel()
		}
	}()
}

func (m *LeechModule) sendDownloadError(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, notifyID int, errStr string, mode string, startTime time.Time) {
	tag := fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", msg.UserID, telegram.EscapeHTML(msg.SenderName))
	if msg.Username != "" {
		tag = "@" + msg.Username
	}

	// stopped by the user (Cancel button, /cancel): the reason of the Python bot, not the raw error
	stopped := ctx.Err() != nil
	if stopped {
		ctx = context.Background() // the task's context is done: the message must still go out
	}
	m.deleteCommandLinks(peer, msg) // like the Python bot: also when the task fails or is refused
	// like the Python bot: "Upload Stopped!" when the upload failed, and a 😘 when the user
	// stopped the task himself
	low := strings.ToLower(errStr)
	isUpload := strings.HasPrefix(low, "upload") || strings.Contains(low, " upload failed")
	header := "Download Stopped!"
	if isUpload {
		header = "Upload Stopped!"
	}
	if strings.Contains(low, "context canceled") || stopped {
		header += " 😘"
		if isUpload {
			errStr = "Your upload has been stopped!"
		} else {
			errStr = "Download stopped by user!"
		}
	}
	elapsed := ext.TimeSinceFormatted(startTime)
	errMsg := fmt.Sprintf("<i><b>"+header+"</b></i>\n\n"+ // an empty line before the quote, like the Python bot
		"<blockquote><b>╭</b> <b>Task for:</b> %s\n"+
		"┠ <b>Due To:</b> %s\n"+
		"┠ <b>Mode:</b> %s\n"+
		"<b>╰</b> <b>Elapsed:</b> %s</blockquote>",
		tag, telegram.EscapeHTML(errStr), mode, elapsed)

	if notifyID > 0 {
		statusMsgID := 0
		if m.statusMod != nil {
			statusMsgID = m.statusMod.GetStatusMessageID(msg.ChatID)
		}
		if notifyID != statusMsgID || m.taskMgr.Count() <= 1 {
			_ = m.sender.DeleteMessage(ctx, peer, []int{notifyID})
		}
	}
	var markup tg.ReplyMarkupClass
	if !noRetry(errStr) {
		markup = retryMarkup(msg)
	}
	_, _ = m.sender.SendMessage(ctx, peer, errMsg, msg.MessageID, markup)

	if m.statusMod != nil {
		if m.taskMgr.Count() == 0 {
			m.statusMod.DeleteAllMessages(ctx)
		} else {
			go m.statusMod.UpdateAllMessages(ctx, true)
		}
	}
}

func (m *LeechModule) processLeechPipeline(peer tg.InputPeerClass, msg *types.MessageContext, opt types.LeechOptions, link, taskDir string, notifyID int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		_ = os.RemoveAll(taskDir)
		log.Printf("[AZML-Go] Cleared directory: %s", taskDir)
	}()

	cfg := config.Get()
	user := m.userMap.Get(msg.UserID)
	isPremium := telegram.IsPremium(msg.UserID, m.userMap)

	// -vt: the tools are chosen before the task starts (like the Python bot)
	var vtSel *VTSelection
	if opt.VT && !msg.Internal {
		if cfg.DisableVT {
			m.sendDownloadError(ctx, peer, msg, notifyID, "Video Tools (-vt) are disabled on this bot.", "#Leech", time.Now())
			return
		}
		if vtSel = m.videoToolsMenu(ctx, peer, msg, link); vtSel == nil {
			m.sendDownloadError(ctx, peer, msg, notifyID, "Video Tools closed, task cancelled.", "#Leech", time.Now())
			return
		}
	}
	// DELETE_LINKS (like the Python bot): the command and the replied link/file are deleted when
	// the task starts; a replied Telegram file right after the bot read it.
	var delOnce sync.Once
	deleteLinks := func() {
		if !cfg.DeleteLinks || msg.Internal {
			return
		}
		delOnce.Do(func() {
			ids := []int{msg.MessageID}
			if msg.IsReply && msg.ReplyToID > 0 {
				ids = append(ids, msg.ReplyToID)
			}
			go func() {
				// one by one: a message the bot may not delete must not keep the other one
				for _, id := range ids {
					dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
					if err := m.sender.DeleteMessage(dctx, peer, []int{id}); err != nil {
						log.Printf("[DELETE_LINKS] can't delete message %d in %d: %v (is the bot admin with 'Delete messages'?)", id, msg.ChatID, err)
					}
					dcancel()
				}
			}()
		})
	}
	if link != "" || (!msg.HasMedia && msg.RawMedia == nil && !msg.IsReply) {
		deleteLinks() // a link: nothing more is needed from the messages
	}
	sourceURL := link // for the Source Link button (before any direct-link resolving)
	if sourceURL == "" && msg.IsReply && msg.ReplyToID > 0 && msg.ChatID < 0 {
		sourceURL = channelMessageLink(msg.ChatID, msg.ReplyToID)
	}

	var downloadedPath string
	var dlErr error

	// 0. Quota and Task Limits Pre-check
	if limitErr := ext.CheckTaskLimits(msg.UserID, 0, "leech", opt.IsExtract, opt.IsZip, 0); limitErr != "" {
		m.sendDownloadError(ctx, peer, msg, notifyID, "Limit Exceeded: "+limitErr, "#Leech", time.Now())
		return
	}

	if cfg.DisableTorrents && isTorrentInput(link, msg) && !m.debridTorrents(msg.UserID) {
		m.sendDownloadError(ctx, peer, msg, notifyID, torrentsDisabledMsg, "#Leech | #Torrent", time.Now())
		return
	}

	// HLS (m3u8): aria2 would only save the playlist text, yt-dlp downloads the stream
	if !opt.Ytdl && isHLSLink(link) {
		useYtdlForHLS(&opt)
	}

	// Try DDL API for Telegram links (t.me/... or t.me/c/...) if a link was provided
	if cfg.DDLAPI != "" && link != "" && !opt.Ytdl {
		if cid, mid, ok := ext.ParseTelegramLink(link); ok {
			tgChannelID := cid
			if tgChannelID == 0 {
				tgChannelID = msg.ChatID
			}
			if mid != 0 {
				if ddlURL, err := ext.TryDDLAPI(ctx, tgChannelID, mid); err == nil && ddlURL != "" {
					link = ddlURL
				}
			}
		}
	}

	// TorBox (TORBOX_API, web downloads): a hoster link becomes a direct link from TorBox; it leaves
	// the TorBox account when the task ends
	if ext.IsURL(link) && !opt.Ytdl && !ext.IsMagnet(link) && !strings.HasSuffix(strings.ToLower(strings.Split(link, "?")[0]), ".torrent") &&
		m.torboxFor("links", msg.UserID) && !downloader.IsGofileLink(link) &&
		(!ext.IsMegaLink(link) || (cfg.MegaEmail == "" && (user == nil || user.MegaEmail == ""))) && ext.IsDebridHost(link) {
		if d, cleanup, err := ext.TorBoxLink(ctx, link); err == nil && d != "" {
			log.Printf("[LEECH] TorBox link for %s", link)
			link = d
			defer cleanup()
		} else if err != nil {
			log.Printf("[LEECH] TorBox: %v", err)
		}
	}

	// Debrid (DEBRID_LINK_API / REAL_DEBRID_API) like the Python bot; Mega only without a MEGA account
	if ext.IsURL(link) && !opt.Ytdl && (cfg.DebridLinkAPI != "" || cfg.RealDebridAPI != "") &&
		(!ext.IsMegaLink(link) || (cfg.MegaEmail == "" && (user == nil || user.MegaEmail == ""))) {
		if d, err := ext.DebridLink(ctx, link); err == nil && d != "" {
			log.Printf("[LEECH] Debrid link for %s", link)
			link = d
		}
	}

	// Try Direct Link Generator for host URLs (Mediafire, Pixeldrain, Solidfiles)
	if ext.IsURL(link) && !opt.Ytdl && !ext.IsMegaLink(link) && !downloader.IsGofileLink(link) {
		if directURL, err := ext.GenerateDirectLink(link); err == nil && directURL != "" {
			link = directURL
		}
	}

	taskUID := fmt.Sprintf("%d_%s", msg.UserID, ext.RandomHex(3))

	displayName := opt.CustomName
	if displayName == "" {
		if ext.IsMagnet(link) {
			displayName = "Torrent/Magnet"
		} else if ext.IsURL(link) {
			displayName = filepath.Base(link)
			if strings.Contains(displayName, "?") {
				displayName = strings.Split(displayName, "?")[0]
			}
		} else if msg.FileName != "" {
			displayName = msg.FileName
		} else if msg.IsReply {
			displayName = "Telegram File"
		} else {
			displayName = "Leech Task"
		}
	}
	displayName = filepath.Base(displayName)

	uTask := ext.NewUnifiedTask(taskUID, displayName, msg.FileSize, msg.ChatID, msg.UserID, notifyID, cancel)
	uTask.SetStatusDisplay(msg.Username, msg.SenderName, pythonMode("", opt))
	m.taskMgr.AddTask(uTask)
	// INCOMPLETE_TASK_NOTIFIER: remembered until the task ends (a restart leaves it in the DB)
	ext.DB.SaveRunningTask(ext.RunningTask{ID: taskUID, ChatID: msg.ChatID, MessageID: msg.MessageID, UserID: msg.UserID, User: msg.SenderName, Name: displayName})
	defer ext.DB.RemoveRunningTask(taskUID)

	// Instantly render the status progress bar so the user gets an immediate (<1s) response
	if notifyID > 0 && m.statusMod != nil {
		if !m.statusMod.HasStatusMessage(msg.ChatID) {
			m.statusMod.SetStatusMessage(msg.ChatID, notifyID, peer)
			go m.statusMod.UpdateAllMessages(ctx, true)
		} else {
			go m.statusMod.UpdateAllMessages(ctx, true)
		}
	}

	// For HTTP/HTTPS links: probe remote headers to resolve true filename (Content-Disposition) & extension in background
	if ext.IsURL(link) && !opt.Ytdl && !ext.IsMegaLink(link) && !downloader.IsGofileLink(link) && !ext.IsMagnet(link) && !strings.Contains(strings.ToLower(link), ".torrent") {
		resolvedName, finalURL, resolvedSize, _ := ext.ResolveDownloadMetadata(ctx, link)
		if low := strings.ToLower(resolvedName); strings.HasSuffix(low, ".m3u8") || strings.HasSuffix(low, ".m3u") {
			resolvedName = "" // an HLS playlist behind a link without .m3u8: yt-dlp takes it
			useYtdlForHLS(&opt)
		}
		if resolvedName != "" {
			if opt.CustomName == "" {
				opt.CustomName = resolvedName
				displayName = resolvedName
				uTask.SetName(resolvedName)
			}
		}
		if finalURL != "" {
			link = finalURL
		}
		if msg.FileSize == 0 && resolvedSize > 0 {
			msg.FileSize = resolvedSize
			uTask.SetSize(resolvedSize)
		}
	}

	defer func() {
		m.taskMgr.RemoveTask(taskUID)
		if m.statusMod != nil {
			if m.taskMgr.Count() == 0 {
				m.statusMod.DeleteAllMessages(context.Background())
			} else {
				go m.statusMod.UpdateAllMessages(context.Background(), true)
			}
		}
	}()

	taskStartTime := time.Now()
	dlStartTime := time.Now()
	var dlDuration time.Duration
	var ulStartTime time.Time
	var maxDLSpeed float64
	var maxULSpeed float64
	var totalSize int64
	var uploadedNames []string
	var mirrorLinks []string
	var gdIndexLinks []string // TJ GDrive: the INDEX_URL links next to mirrorLinks
	gdSess := &downloader.GDSession{}
	gdUserToken := false // uploaded with the user's own token.pickle: his links stay private
	rcSess := &downloader.RcloneSession{}
	var rcPaths []string   // TJ RClone: where each file is (when the remote gives no link)
	var fileLinks []string // link to each uploaded file in the dump / log channel
	mirrorSess := &ext.MirrorSession{}
	if user != nil {
		mirrorSess.Keys = maps.Clone(user.DDLKeys)
	}
	modeStr := "#Leech | #Aria2"
	if ext.IsMagnet(link) || strings.HasSuffix(strings.ToLower(link), ".torrent") {
		modeStr = "#Leech | #Torrent"
	} else if ext.IsMegaLink(link) {
		modeStr = "#Leech | #Mega"
	} else if link == "" && (msg.IsReply || msg.HasMedia) {
		modeStr = "#Leech | #Telegram"
	}
	if opt.Ytdl {
		modeStr = "#Leech | #YtDlp"
		if opt.IsMirror {
			modeStr = "#Mirror | #YtDlp"
		}
		uTask.SetEngine(ext.EngineName("ytdlp"))
		if opt.CustomName == "" {
			uTask.SetName("yt-dlp Media")
		}
	}

	if opt.IsMirror { // the error messages say where it uploads: #tjgd / #tjrc / #DDL, not #Leech
		kind := "#DDL"
		switch opt.MirrorHost {
		case ext.GDriveHostID:
			kind = "#tjgd"
		case ext.RcloneHostID:
			kind = "#tjrc"
		}
		modeStr = strings.Replace(strings.Replace(modeStr, "#Leech", kind, 1), "#Mirror", kind, 1)
	}

	// 0. Queue Check for Download (Sleep v0 Engine)
	queuedDL, qErr := ext.AcquireDownloadQueue(ctx, taskUID, displayName, msg.FileSize, msg.ChatID, msg.UserID, notifyID, cancel)
	if qErr != nil {
		m.sendDownloadError(ctx, peer, msg, notifyID, "Download task cancelled while in queue", modeStr, taskStartTime)
		return
	}
	// every early return must free the download slot, or the next task stays in QueueDL forever
	defer ext.ReleaseDownload(taskUID)

	// like the Python bot (onDownloadStart): the source in LINKS_LOG_ID, "Task Started" in PM
	go m.sendLinksLog(context.Background(), msg, modeStr, sourceURL)
	pmStartID := 0
	if cfg.BotPM || (user != nil && user.BotPM) {
		pmStartID = m.sendPMStart(ctx, msg, sourceURL)
	}
	// a task that fails or is cancelled leaves no "Task Started" in PM (nothing follows it there)
	pmFinished := false
	defer func() {
		if pmStartID > 0 && !pmFinished {
			pm := &tg.InputPeerUser{UserID: msg.UserID, AccessHash: telegram.GetUserAccessHash(msg.UserID)}
			_ = m.sender.DeleteMessage(context.Background(), pm, []int{pmStartID})
		}
	}()
	if queuedDL {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "<i>Starting download from queue...</i>", nil)
	}

	// a local folder (the -m upload task): nothing to download
	if fi, err := os.Stat(link); err == nil && fi.IsDir() && filepath.IsAbs(link) {
		downloadedPath, link = link, ""
		msg.IsReply, msg.HasMedia, msg.RawMedia = false, false, nil
		modeStr = "#Leech | #Folder"
	}
	uTask.SetInputMode(pythonMode(modeStr, opt)) // /status Mode, like the Python bot

	// like limit_checker of the Python bot: the size limits as soon as the size is known
	// (aria2, Mega, GDrive, Telegram, yt-dlp); over a limit the download is stopped
	var limitHit atomic.Pointer[string]
	dlDone := make(chan struct{})
	if !ext.LimitExempt(msg.UserID) {
		kind := "direct"
		switch {
		case opt.Ytdl:
			kind = "ytdlp"
		case isTorrentInput(link, msg):
			kind = "torrent"
		case ext.IsMegaLink(link):
			kind = "mega"
		case downloader.IsGDriveLink(link):
			kind = "gdrive"
		}
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			var lastSize int64
			for {
				select {
				case <-ctx.Done():
					return
				case <-dlDone:
					return
				case <-t.C:
				}
				// checked again whenever the size changes (a .torrent file from Telegram is tiny, then
				// TorBox gives the size of the torrent itself)
				size := uTask.Size()
				if size <= 0 || size == lastSize {
					continue
				}
				lastSize = size
				if e := ext.CheckSizeLimits(msg.UserID, size, kind, opt.IsExtract, opt.IsZip, !opt.IsMirror); e != "" {
					log.Printf("[LIMIT] %s stopped for user %d: %s", displayName, msg.UserID, strings.ReplaceAll(e, "\n", " "))
					limitHit.Store(&e)
					cancel()
					return
				}
			}
		}()
	}

	// 1. Download Phase
	if link == "" && (msg.IsReply || msg.HasMedia) {
		var mediaSource tg.MessageMediaClass = msg.RawMedia

		if mediaSource == nil && msg.IsReply && msg.ReplyToID > 0 {
			targetMsg, err := telegram.FetchMessage(ctx, m.rawAPI, peer, msg.ReplyToID)
			if err == nil && targetMsg != nil {
				if targetMsg.Media != nil {
					mediaSource = targetMsg.Media
					msg.HasMedia = true
					msg.RawMedia = targetMsg.Media
					link = ""
				} else if targetMsg.Message != "" {
					for _, tok := range strings.Fields(targetMsg.Message) {
						if strings.HasPrefix(tok, "http") || strings.HasPrefix(tok, "magnet:") {
							link = tok
							if sourceURL == "" || strings.HasPrefix(sourceURL, "https://t.me/c/") {
								sourceURL = tok // the replied link is the source
							}
							break
						}
					}
				}
			} else if err != nil {
				log.Printf("[LEECH] Failed to fetch replied message %d: %v", msg.ReplyToID, err)
				dlErr = fmt.Errorf("failed to fetch replied message: %v", err)
			}
		}

		// where the file is read from (the copy in the dump channel when the original is deleted)
		srcPeer, srcMsgID := peer, msg.ReplyToID
		if srcMsgID == 0 {
			srcMsgID = msg.MessageID
		}
		// helper bots (BOT_TOKENS) download from a forward of the message, so only then the file is
		// first copied to the dump channel; the bot alone reads it directly (like the Python bot)
		needsCopy := m.sessPool != nil && m.sessPool.ActiveSessionCount() > 0
		if mediaSource != nil && cfg.DeleteLinks && !msg.Internal && !needsCopy {
			deleteLinks() // the file location is known: delete now, the bot downloads it directly
			if strings.HasPrefix(sourceURL, "https://t.me/c/") {
				sourceURL = "" // the original is gone
			}
		}
		if mediaSource != nil && cfg.DeleteLinks && !msg.Internal && needsCopy {
			// DELETE_LINKS with helper bots: copy the file to the dump channel, delete the originals
			// right away, the helper bots download from the copy (the Source Link points to it)
			dump := cfg.DumpChatID // the leech log (one channel)
			if dump == 0 {
				dump = m.dumpChatID
			}
			if dump == 0 {
				dump = cfg.LeechLogID
			}
			if dump != 0 {
				if newID := uploader.CopyMessageID(dump, msg.ChatID, srcMsgID); newID > 0 {
					if h := telegram.ResolveChannelAccessHash(ctx, m.rawAPI, dump); h != 0 {
						dumpPeer := &tg.InputPeerChannel{ChannelID: pool.RawChannelID(dump), AccessHash: h}
						if cm, err := telegram.FetchMessage(ctx, m.rawAPI, dumpPeer, newID); err == nil && cm != nil && cm.Media != nil {
							mediaSource, msg.RawMedia = cm.Media, cm.Media
							srcPeer, srcMsgID = dumpPeer, newID
							sourceURL = channelMessageLink(dump, newID)
						}
					}
				}
			}
			deleteLinks()
		}

		if mediaSource != nil {
			link = ""
			// Case 1A: Document Media (Videos, Audio, Archives, Files)
			if docMedia, ok := mediaSource.(*tg.MessageMediaDocument); ok {
				if doc, ok := docMedia.Document.(*tg.Document); ok {
					fileName := ""
					for _, attr := range doc.Attributes {
						if fnAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
							fileName = fnAttr.FileName
							break
						}
					}
					if fileName == "" {
						isVideo := false
						for _, attr := range doc.Attributes {
							if _, ok := attr.(*tg.DocumentAttributeVideo); ok {
								isVideo = true
								break
							}
						}
						if isVideo {
							fileName = fmt.Sprintf("video_%d.mp4", doc.ID)
						} else {
							fileName = fmt.Sprintf("telegram_doc_%d", doc.ID)
						}
					}
					if opt.CustomName != "" {
						origExt := filepath.Ext(fileName)
						fileName = opt.CustomName
						if filepath.Ext(fileName) == "" && origExt != "" {
							fileName += origExt
						}
					}
					fileName = strings.ReplaceAll(fileName, "/", "_")
					fileName = strings.ReplaceAll(fileName, "\\", "_")
					destPath := filepath.Join(taskDir, fileName)
					_ = os.MkdirAll(filepath.Dir(destPath), 0777)
					uTask.SetName(fileName)
					uTask.SetSize(doc.Size)
					var dlSuccess bool

					effectiveDumpChatID := cfg.DumpChatID // the leech log (one channel)
					if effectiveDumpChatID == 0 {
						effectiveDumpChatID = m.dumpChatID
					}
					if effectiveDumpChatID == 0 && cfg != nil {
						effectiveDumpChatID = cfg.LeechLogID
					}

					// Check if Multi-Bot Download is enabled and sessions are active
					if m.sessPool != nil && m.sessPool.ActiveSessionCount() > 0 && effectiveDumpChatID != 0 {
						targetMsgID := srcMsgID

						rawDumpID := pool.RawChannelID(effectiveDumpChatID)
						dumpHash := telegram.ResolveChannelAccessHash(ctx, m.rawAPI, effectiveDumpChatID)
						if dumpHash == 0 {
							dumpHash = telegram.GetChannelAccessHash(rawDumpID)
						}

						if dumpHash != 0 {
							dumpPeer := &tg.InputPeerChannel{
								ChannelID:  rawDumpID,
								AccessHash: dumpHash,
							}
							log.Printf("[LEECH Multi-Bot] Forwarding message %d to dump channel %d for multi-client download...", targetMsgID, effectiveDumpChatID)
							fwdMsgID, fwdErr := telegram.ForwardToChannelWithFloodWait(ctx, m.rawAPI, srcPeer, dumpPeer, targetMsgID)

							if fwdErr == nil && fwdMsgID > 0 {
								uTask.SetStatus(types.StatusDownloading, ext.EngineName("telegram")+" (Multi-Bot)")
								multiDL := downloader.NewMultiBotDownloader(m.sessPool, cfg.WorkerBotTokens, cfg.TelegramAPI, cfg.TelegramHash)
								dlErr = multiDL.DownloadToFile(ctx, effectiveDumpChatID, int64(fwdMsgID), doc.Size, destPath, func(curr, total int64, speed float64, eta string) {
									uTask.SetProgress(curr, total, speed, eta)
								})
								// Retain forwarded message in dump channel for backup and multi-client access

								if dlErr == nil {
									downloadedPath = destPath
									uTask.SetProgress(doc.Size, doc.Size, 0, "-")
									dlSuccess = true
									log.Printf("[LEECH Multi-Bot] Multi-bot download succeeded for %s (%s)", fileName, ext.GetReadableFileSize(doc.Size))
								} else {
									log.Printf("[LEECH Multi-Bot] Multi-bot download returned error: %v. Falling back to single-client downloader...", dlErr)
								}
							} else {
								log.Printf("[LEECH Multi-Bot] Failed to forward to dump channel (%v, msgID: %d). Falling back to single-client downloader...", fwdErr, fwdMsgID)
							}
						}
					}

					// User session (premium): the bot copies the file to the dump channel and the
					// user session downloads it from there (it can't read the bot's own chats).
					// the premium user session is only for uploads; the bot downloads by itself
					// the file is known now (location + reference): the messages can go
					deleteLinks()
					// Fallback to single-client chunk downloader if multi-bot was not active or failed
					if !dlSuccess {
						uTask.SetStatus(types.StatusDownloading, ext.EngineName("telegram"))
						// the bot's own file location: only the bot account can use it
						downloadClient := m.rawAPI
						chunkDL := downloader.NewTelegramChunkDownloader(downloadClient)
						botTD, where := telegram.ActiveBotMediaTD(), "media servers"
						if botTD == nil {
							botTD, where = telegram.ActiveBotTD(), "normal servers"
						}
						if dcClients, closeFn := dcDownloadClients(ctx, botTD, doc.DCID); len(dcClients) > 0 {
							defer closeFn()
							log.Printf("[LEECH] Bot download on DC%d (%s) with %d connections", doc.DCID, where, dcPoolSize)
							chunkDL = downloader.NewTelegramChunkDownloaderPool(dcClients)
						}
						loc := doc.AsInputDocumentFileLocation("")
						if loc != nil {
							refreshRef := func() ([]byte, error) {
								if srcMsgID > 0 {
									rMsg, err := telegram.FetchMessage(ctx, m.rawAPI, srcPeer, srcMsgID)
									if err == nil && rMsg != nil {
										if dMedia, ok := rMsg.Media.(*tg.MessageMediaDocument); ok {
											if dDoc, ok := dMedia.Document.(*tg.Document); ok {
												return dDoc.FileReference, nil
											}
										}
									}
								}
								return nil, fmt.Errorf("failed to refresh file reference")
							}

							err := chunkDL.DownloadToFile(ctx, loc, doc.Size, destPath, func(curr, total int64, speed float64, eta string) {
								uTask.SetProgress(curr, total, speed, eta)
							}, refreshRef)

							if err == nil {
								downloadedPath = destPath
								uTask.SetProgress(doc.Size, doc.Size, 0, "-")
								dlErr = nil
							} else {
								log.Printf("[LEECH] MTProto document download error: %v", err)
								dlErr = err
							}
						} else {
							dlErr = fmt.Errorf("failed to get Telegram document file location")
						}
					}
				} else {
					dlErr = fmt.Errorf("replied document is empty or unavailable")
				}
			} else if photoMedia, ok := mediaSource.(*tg.MessageMediaPhoto); ok {
				// Case 1B: Photo Media
				deleteLinks()
				if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
					fileName := fmt.Sprintf("photo_%d.jpg", photo.ID)
					if opt.CustomName != "" {
						fileName = opt.CustomName
						if !strings.HasSuffix(strings.ToLower(fileName), ".jpg") && !strings.HasSuffix(strings.ToLower(fileName), ".png") {
							fileName += ".jpg"
						}
					}
					fileName = strings.ReplaceAll(fileName, "/", "_")
					fileName = strings.ReplaceAll(fileName, "\\", "_")
					destPath := filepath.Join(taskDir, fileName)
					_ = os.MkdirAll(filepath.Dir(destPath), 0777)
					uTask.SetName(fileName)
					uTask.SetStatus(types.StatusDownloading, ext.EngineName("telegram"))

					thumbSize := "m"
					if len(photo.Sizes) > 0 {
						switch s := photo.Sizes[len(photo.Sizes)-1].(type) {
						case *tg.PhotoSize:
							thumbSize = s.Type
						case *tg.PhotoSizeProgressive:
							thumbSize = s.Type
						}
					}

					dl := tdDownloader.NewDownloader()
					loc := photo.AsInputPhotoFileLocation(thumbSize)
					if loc != nil {
						_, err := dl.Download(m.rawAPI, loc).ToPath(ctx, destPath)
						if err == nil {
							downloadedPath = destPath
							if fi, statErr := os.Stat(destPath); statErr == nil {
								uTask.SetSize(fi.Size())
								uTask.SetProgress(fi.Size(), fi.Size(), 0, "-")
							}
						} else {
							log.Printf("[LEECH] MTProto photo download error: %v", err)
							dlErr = err
						}
					} else {
						dlErr = fmt.Errorf("failed to get Telegram photo location")
					}
				} else {
					dlErr = fmt.Errorf("replied photo is empty or unavailable")
				}
			} else {
				dlErr = fmt.Errorf("unsupported media in replied message")
			}
		}
	}

	if downloadedPath != "" {
		// Document downloaded directly via MTProto
	} else if opt.Ytdl {
		uTask.SetStatus(types.StatusDownloading, ext.EngineName("ytdlp"))
		yt := downloader.NewYtDlpDownloader()
		var file string
		file, dlErr = yt.Download(ctx, link, taskDir, opt.YtdlFormat, func(curr, total int64, speed float64, eta string) {
			if speed > maxDLSpeed {
				maxDLSpeed = speed
			}
			uTask.SetProgress(curr, total, speed, eta)
		}, opt.YtdlArgs...)
		if dlErr == nil {
			downloadedPath = ytdlResult(taskDir, file)
			if fi, sErr := os.Stat(downloadedPath); sErr == nil && !fi.IsDir() && opt.CustomName != "" && downloadedPath != "" {
				// -n: the new name, with the extension yt-dlp gave
				nn := strings.TrimSuffix(opt.CustomName, filepath.Ext(opt.CustomName)) + filepath.Ext(downloadedPath)
				np := filepath.Join(filepath.Dir(downloadedPath), filepath.Base(nn))
				if os.Rename(downloadedPath, np) == nil {
					downloadedPath = np
				}
			}
			if downloadedPath == "" {
				dlErr = fmt.Errorf("yt-dlp finished but no file was downloaded")
			} else {
				uTask.SetName(filepath.Base(downloadedPath))
			}
		}
	} else if downloader.IsGDriveLink(link) {
		modeStr = "#Leech | #GDrive"
		uTask.SetInputMode(pythonMode(modeStr, opt))
		gdrive := downloader.GetGDriveClient()
		fileID := downloader.ExtractGDriveID(link)
		if fileID == "" {
			m.sendDownloadError(ctx, peer, msg, notifyID, "Google Drive Error: Unable to extract file/folder ID", modeStr, taskStartTime)
			return
		}

		info, err := gdrive.GetFileInfo(fileID)
		if err != nil {
			m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("Google Drive Error: %v", err), modeStr, taskStartTime)
			return
		}

		if info.IsDir {
			files, fErr := gdrive.GetFolderFiles(fileID, "")
			if fErr != nil || len(files) == 0 {
				m.sendDownloadError(ctx, peer, msg, notifyID, "Google Drive Error: No files found in folder", modeStr, taskStartTime)
				return
			}

			filesToDownload := files
			if opt.Select {
				uidStr := fmt.Sprintf("%d", notifyID)
				doneChan := RegisterGDriveSelectCache(uidStr, files, msg.UserID)
				buttons := BuildGDriveSelectionButtons(uidStr)
				selMsg, _ := m.sender.SendMessage(ctx, peer, "㊂ <b><u>Select Files :</u></b>\n\n<blockquote>Your Google Drive download is paused. Choose the files on the web page, then press <b>Done Selecting</b>.</blockquote>", msg.MessageID, buttons)
				defer func() {
					if selMsg > 0 {
						_ = m.sender.DeleteMessage(context.Background(), peer, []int{selMsg})
					}
				}()

				select {
				case ok := <-doneChan:
					if !ok {
						m.sendDownloadError(ctx, peer, msg, notifyID, "Google Drive file selection cancelled by user", modeStr, taskStartTime)
						return
					}
					selectedIDs := web.GDriveStore.ReadSelection(uidStr)
					var filtered []downloader.GDriveFile
					for _, f := range files {
						if !f.IsDir && containsString(selectedIDs, f.ID) {
							filtered = append(filtered, f)
						}
					}
					if len(filtered) == 0 {
						m.sendDownloadError(ctx, peer, msg, notifyID, "No files selected. Download cancelled.", modeStr, taskStartTime)
						return
					}
					filesToDownload = filtered
				case <-ctx.Done():
					return
				}
			}

			uTask.SetStatus(types.StatusDownloading, ext.EngineName("gdrive"))
			_ = m.sender.EditMessage(ctx, peer, notifyID, fmt.Sprintf("<i>Downloading %d Google Drive files...</i>", len(filesToDownload)), nil)
			for i, f := range filesToDownload {
				if f.IsDir {
					continue
				}
				uTask.SetName(fmt.Sprintf("[%d/%d] %s", i+1, len(filesToDownload), f.Name))
				subDir := filepath.Join(taskDir, f.Path)
				_ = os.MkdirAll(subDir, 0755)
				_ = gdrive.DownloadFile(ctx, f.ID, subDir, f.Name, func(curr, total int64, speed float64) {
					rem := total - curr
					eta := "-"
					if speed > 0 && rem > 0 {
						eta = ext.GetReadableTime(int64(float64(rem) / speed))
					}
					uTask.SetProgress(curr, total, speed, eta)
				})
			}
			downloadedPath = taskDir
		} else {
			fName := info.Name
			if opt.CustomName != "" {
				fName = opt.CustomName
			}
			uTask.SetName(fName)
			uTask.SetStatus(types.StatusDownloading, ext.EngineName("gdrive"))
			dlErr = gdrive.DownloadFile(ctx, fileID, taskDir, fName, func(curr, total int64, speed float64) {
				rem := total - curr
				eta := "-"
				if speed > 0 && rem > 0 {
					eta = ext.GetReadableTime(int64(float64(rem) / speed))
				}
				uTask.SetProgress(curr, total, speed, eta)
			})
			downloadedPath = filepath.Join(taskDir, fName)
		}
	} else if downloader.IsGofileLink(link) {
		modeStr = "#Leech | #GoFile"
		uTask.SetInputMode(pythonMode(modeStr, opt))
		uTask.SetStatus(types.StatusDownloading, ext.EngineName("aria2"))
		downloadedPath, dlErr = m.downloadGofile(ctx, peer, msg, opt, link, taskDir, uTask)
		if dlErr == errSelectionCancelled {
			m.sendDownloadError(ctx, peer, msg, notifyID, "File selection cancelled by user", modeStr, taskStartTime)
			return
		}
	} else if ext.IsMegaLink(link) {
		uTask.SetStatus(types.StatusDownloading, ext.EngineName("mega"))
		var only map[string]bool
		if opt.Select {
			// real file list of the folder, chosen on the web page (BASE_URL)
			listCtx := ctx
			if user != nil {
				listCtx = downloader.WithMegaLogin(ctx, user.MegaEmail, user.MegaPass)
			}
			nodes, lErr := downloader.MegaList(listCtx, link)
			if lErr != nil {
				m.sendDownloadError(ctx, peer, msg, notifyID, lErr.Error(), modeStr, taskStartTime)
				return
			}
			if len(nodes) > 1 {
				uidStr := fmt.Sprintf("%d", notifyID)
				var files []downloader.MegaFileInfo
				for _, n := range nodes {
					files = append(files, downloader.MegaFileInfo{ID: n.Handle, Name: n.Name, Size: n.Size, Path: n.Path})
				}
				doneChan := RegisterMegaSelectCache(uidStr, files, msg.UserID)
				selMsg, _ := m.sender.SendMessage(ctx, peer, fmt.Sprintf("㊂ <b><u>MEGA Folder :</u></b>\n\n<blockquote>%d files. Choose them on the web page, then press Done Selecting.</blockquote>", len(files)), msg.MessageID, BuildMegaSelectionButtons(uidStr))
				defer func() {
					if selMsg > 0 {
						_ = m.sender.DeleteMessage(context.Background(), peer, []int{selMsg})
					}
				}()
				select {
				case ok := <-doneChan:
					if !ok {
						ClearMegaSelectCache(uidStr)
						m.sendDownloadError(ctx, peer, msg, notifyID, "MEGA file selection cancelled by user", modeStr, taskStartTime)
						return
					}
				case <-ctx.Done():
					ClearMegaSelectCache(uidStr)
					return
				}
				only = map[string]bool{}
				if c := GetMegaSelectCache(uidStr); c != nil {
					for _, f := range c.Files {
						if f.Selected {
							only[f.ID] = true
						}
					}
				}
				ClearMegaSelectCache(uidStr)
			}
		}
		megaCtx := ctx
		if user != nil {
			megaCtx = downloader.WithMegaLogin(ctx, user.MegaEmail, user.MegaPass) // /us Mega Login first
		}
		downloadedPath, dlErr = downloader.MegaDownloadLink(megaCtx, link, taskDir, only, func(cur, total int64, speed float64, eta string) {
			if speed > maxDLSpeed {
				maxDLSpeed = speed
			}
			uTask.SetProgress(cur, total, speed, eta)
		})
	} else if m.debridTorrents(msg.UserID) && isTorrentInput(link, nil) {
		modeStr = "#Leech | #" + m.debridName(msg.UserID)
		uTask.SetInputMode(pythonMode(modeStr, opt))
		downloadedPath, dlErr = m.downloadDebridTorrent(ctx, peer, msg, opt.Select, link, taskDir, uTask)
		if dlErr == errSelectionCancelled {
			m.sendDownloadError(ctx, peer, msg, notifyID, "File selection cancelled by user", modeStr, taskStartTime)
			return
		}
	} else if ext.IsMagnet(link) || strings.Contains(strings.ToLower(link), ".torrent") || ext.IsURL(link) || (func() bool { fi, err := os.Stat(link); return err == nil && !fi.IsDir() }()) {
		cleanLink := strings.Split(strings.TrimSpace(link), "?")[0]
		isLocalTorrent := false
		if fi, err := os.Stat(link); err == nil && !fi.IsDir() && strings.HasSuffix(strings.ToLower(link), ".torrent") {
			isLocalTorrent = true
		}
		isRemoteTorrent := ext.IsURL(link) && strings.HasSuffix(strings.ToLower(cleanLink), ".torrent")
		if cfg.DisableTorrents && (isLocalTorrent || isRemoteTorrent || ext.IsMagnet(link)) && !m.debridTorrents(msg.UserID) {
			m.sendDownloadError(ctx, peer, msg, notifyID, torrentsDisabledMsg, modeStr, taskStartTime)
			return
		}

		aria := downloader.GetAria2Client()

		if isLocalTorrent {
			log.Printf("[LEECH] Adding local .torrent file to tjurl: %s", link)
			gid, err := aria.AddTorrent(link, taskDir)
			if err != nil {
				m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("Torrent Error: %v", err), modeStr, taskStartTime)
				return
			}
			uTask.SetStatus(types.StatusDownloading, ext.EngineName("aria2"))
			task := downloader.NewAria2DownloadTask(gid, opt.CustomName, taskDir, notifyID, msg.ChatID, msg.UserID)
			downloadedPath, dlErr = task.PollUntilComplete(ctx, func(cur, total int64) {
				uTask.SetProgress(cur, total, task.Speed(), task.ETA())
				if task.Name() != "" {
					uTask.SetName(task.Name())
				}
				if se, le, isT := task.Peers(); isT {
					uTask.SetPeers(se, le)
				}
			})
		} else if isRemoteTorrent {
			log.Printf("[LEECH] Downloading remote .torrent metadata: %s", link)
			torrentDest := filepath.Join(taskDir, fmt.Sprintf("source_%d.torrent", time.Now().UnixNano()))
			if err := ext.DownloadHTTPFile(ctx, link, torrentDest); err == nil {
				gid, tErr := aria.AddTorrent(torrentDest, taskDir)
				if tErr == nil {
					_ = os.Remove(torrentDest)
					uTask.SetStatus(types.StatusDownloading, ext.EngineName("aria2"))
					task := downloader.NewAria2DownloadTask(gid, opt.CustomName, taskDir, notifyID, msg.ChatID, msg.UserID)
					downloadedPath, dlErr = task.PollUntilComplete(ctx, func(cur, total int64) {
						uTask.SetProgress(cur, total, task.Speed(), task.ETA())
						if task.Name() != "" {
							uTask.SetName(task.Name())
						}
						if se, le, isT := task.Peers(); isT {
							uTask.SetPeers(se, le)
						}
					})
				} else {
					log.Printf("[LEECH] AddTorrent error, fallback to AddURI: %v", tErr)
					gid, aErr := aria.AddURI(link, taskDir, opt.CustomName, taskHeaders(opt.Headers, opt.Username, opt.Password))
					if aErr != nil {
						m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("Download Error: %v", aErr), modeStr, taskStartTime)
						return
					}
					uTask.SetStatus(types.StatusDownloading, ext.EngineName("aria2"))
					task := downloader.NewAria2DownloadTask(gid, opt.CustomName, taskDir, notifyID, msg.ChatID, msg.UserID)
					downloadedPath, dlErr = task.PollUntilComplete(ctx, func(cur, total int64) {
						uTask.SetProgress(cur, total, task.Speed(), task.ETA())
						if task.Name() != "" {
							uTask.SetName(task.Name())
						}
						if se, le, isT := task.Peers(); isT {
							uTask.SetPeers(se, le)
						}
					})
				}
			} else {
				log.Printf("[LEECH] DownloadHTTPFile error, fallback to AddURI: %v", err)
				gid, aErr := aria.AddURI(link, taskDir, opt.CustomName, taskHeaders(opt.Headers, opt.Username, opt.Password))
				if aErr != nil {
					m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("Download Error: %v", aErr), modeStr, taskStartTime)
					return
				}
				uTask.SetStatus(types.StatusDownloading, ext.EngineName("aria2"))
				task := downloader.NewAria2DownloadTask(gid, opt.CustomName, taskDir, notifyID, msg.ChatID, msg.UserID)
				downloadedPath, dlErr = task.PollUntilComplete(ctx, func(cur, total int64) {
					uTask.SetProgress(cur, total, task.Speed(), task.ETA())
					if task.Name() != "" {
						uTask.SetName(task.Name())
					}
					if se, le, isT := task.Peers(); isT {
						uTask.SetPeers(se, le)
					}
				})
			}
		} else {
			// All other magnets, torrents, and DDL links are routed through Aria2c with 16 connections
			// Make URI distinct with fragment tag so concurrent duplicate URLs download into separate task directories independently
			ariaLink := link
			if strings.HasPrefix(ariaLink, "http://") || strings.HasPrefix(ariaLink, "https://") {
				if strings.Contains(ariaLink, "#") {
					ariaLink += "_" + taskUID
				} else {
					ariaLink += "#" + taskUID
				}
			}
			var selExtra map[string]interface{}
			isTorrentLink := ext.IsMagnet(link) || strings.HasSuffix(strings.ToLower(cleanLink), ".torrent")
			if opt.Select && isTorrentLink {
				selExtra = map[string]interface{}{"pause-metadata": "true"} // paused until Done Selecting
			}
			gid, err := aria.AddURIWith(ariaLink, taskDir, opt.CustomName, taskHeaders(opt.Headers, opt.Username, opt.Password), selExtra)
			if err == nil && selExtra != nil {
				fileGID, wErr := aria.WaitTorrentFiles(ctx, gid)
				if wErr != nil {
					_ = aria.Cancel(gid)
					m.sendDownloadError(ctx, peer, msg, notifyID, wErr.Error(), modeStr, taskStartTime)
					return
				}
				_ = aria.Pause(fileGID) // a plain .torrent has no metadata step
				if !m.waitSelection(ctx, peer, msg.MessageID, msg.UserID, fileGID, "torrent") {
					_ = aria.Cancel(fileGID)
					m.sendDownloadError(ctx, peer, msg, notifyID, "File selection cancelled by user", modeStr, taskStartTime)
					return
				}
				_ = aria.Unpause(fileGID)
				opt.Select = false
				if notifyID > 0 && m.statusMod != nil {
					go m.statusMod.UpdateAllMessages(context.Background(), true)
				}
			}
			if err != nil {
				// If Aria2c is unreachable or errors on HTTP/HTTPS DDL link, seamlessly fallback to DirectDownloader!
				if ext.IsURL(link) && (strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://")) {
					log.Printf("[LEECH] tjurl AddURI error: %v, falling back to DirectDownloader for %s", err, link)
					directDl := downloader.NewDirectDownloader()
					dest := filepath.Join(taskDir, "downloaded_file")
					if opt.CustomName != "" {
						dest = filepath.Join(taskDir, opt.CustomName)
					}
					uTask.SetStatus(types.StatusDownloading, ext.EngineName("direct"))
					dlErr = directDl.Download(ctx, link, dest, func(cur, total int64, speed float64, eta string) {
						uTask.SetProgress(cur, total, speed, eta)
					})
					downloadedPath = dest
				} else {
					m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("Download Error: %v", err), modeStr, taskStartTime)
					return
				}
			} else {
				uTask.SetStatus(types.StatusDownloading, ext.EngineName("aria2"))
				task := downloader.NewAria2DownloadTask(gid, opt.CustomName, taskDir, notifyID, msg.ChatID, msg.UserID)
				downloadedPath, dlErr = task.PollUntilComplete(ctx, func(cur, total int64) {
					uTask.SetProgress(cur, total, task.Speed(), task.ETA())
					if task.Name() != "" {
						uTask.SetName(task.Name())
					}
					if se, le, isT := task.Peers(); isT {
						uTask.SetPeers(se, le)
					}
				})
			}
		}
	} else if link != "" {
		// Fallback Direct HTTP
		directDl := downloader.NewDirectDownloader()
		dest := filepath.Join(taskDir, "downloaded_file")
		if opt.CustomName != "" {
			dest = filepath.Join(taskDir, opt.CustomName)
		}
		uTask.SetStatus(types.StatusDownloading, ext.EngineName("direct"))
		dlErr = directDl.Download(ctx, link, dest, func(cur, total int64, speed float64, eta string) {
			uTask.SetProgress(cur, total, speed, eta)
		})
		downloadedPath = dest
	} else {
		if dlErr == nil {
			dlErr = fmt.Errorf("no media or valid link found to download")
		}
	}

	close(dlDone)
	if e := limitHit.Load(); e != nil { // stopped by a size limit, not by the user
		ext.ReleaseDownload(taskUID)
		m.sendDownloadError(context.Background(), peer, msg, notifyID, "Limit Exceeded: "+*e, modeStr, taskStartTime)
		return
	}
	if dlErr != nil {
		ext.ReleaseDownload(taskUID)
		if errors.Is(dlErr, context.Canceled) || strings.Contains(strings.ToLower(dlErr.Error()), "cancel") {
			// stopped by the user (Cancel button, /cancel): "Download Stopped!" like the Python bot
			m.sendDownloadError(ctx, peer, msg, notifyID, "context canceled", modeStr, taskStartTime)
			return
		}
		m.sendDownloadError(ctx, peer, msg, notifyID, dlErr.Error(), modeStr, taskStartTime)
		if msg.SameDirKey != "" {
			m.sameDirFailed(peer, msg)
		}
		return
	}
	ext.ReleaseDownload(taskUID)

	// Critical safeguard: If downloadedPath is a .torrent file, automatically extract and download its real payload!
	if strings.HasSuffix(strings.ToLower(downloadedPath), ".torrent") && cfg.DisableTorrents && !m.debridTorrents(msg.UserID) {
		m.sendDownloadError(ctx, peer, msg, notifyID, torrentsDisabledMsg, modeStr, taskStartTime)
		return
	}
	if strings.HasSuffix(strings.ToLower(downloadedPath), ".torrent") && m.debridTorrents(msg.UserID) {
		torrentFile := downloadedPath
		modeStr = "#Leech | #" + m.debridName(msg.UserID)
		uTask.SetInputMode(pythonMode(modeStr, opt))
		p, err := m.downloadDebridTorrent(ctx, peer, msg, opt.Select, torrentFile, taskDir, uTask)
		_ = os.Remove(torrentFile)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.sendDownloadError(ctx, peer, msg, notifyID, err.Error(), modeStr, taskStartTime)
			return
		}
		downloadedPath = p
	}
	if strings.HasSuffix(strings.ToLower(downloadedPath), ".torrent") {
		log.Printf("[LEECH] Downloaded file is a .torrent file (%s), starting real payload download...", downloadedPath)
		torrentFile := downloadedPath
		_ = m.sender.EditMessage(ctx, peer, notifyID, "<i>Processing .torrent metadata & starting payload download...</i>", nil)
		aria := downloader.GetAria2Client()
		gid, err := aria.AddTorrent(torrentFile, taskDir)
		if err == nil {
			_ = os.Remove(torrentFile)
			uTask.SetStatus(types.StatusDownloading, ext.EngineName("aria2"))
			task := downloader.NewAria2DownloadTask(gid, opt.CustomName, taskDir, notifyID, msg.ChatID, msg.UserID)
			downloadedPath, dlErr = task.PollUntilComplete(ctx, func(cur, total int64) {
				uTask.SetProgress(cur, total, task.Speed(), task.ETA())
				if task.Name() != "" {
					uTask.SetName(task.Name())
				}
				if se, le, isT := task.Peers(); isT {
					uTask.SetPeers(se, le)
				}
			})
			if dlErr != nil {
				_ = m.sender.EditMessage(ctx, peer, notifyID, fmt.Sprintf("<b>Torrent Download Failed:</b> %v", dlErr), nil)
				return
			}
		}
	}

	fi, statErr := os.Stat(downloadedPath)
	if statErr != nil {
		m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("Downloaded file not found on disk: %v", statErr), modeStr, taskStartTime)
		return
	}
	if !fi.IsDir() {
		if fi.Size() == 0 {
			m.sendDownloadError(ctx, peer, msg, notifyID, "Downloaded file is empty (0 bytes)", modeStr, taskStartTime)
			return
		}
		uTask.SetSize(fi.Size())
		uTask.SetProgress(fi.Size(), fi.Size(), 0, "-")
	}
	displayName = filepath.Base(downloadedPath)
	uTask.SetName(displayName)

	// -m: into the shared folder; only the last task of the run goes on to upload it
	if msg.SameDirKey != "" {
		folder, sdErr := sameDirAdd(msg.SameDirKey, downloadedPath)
		if sdErr != nil {
			m.sendDownloadError(ctx, peer, msg, notifyID, sdErr.Error(), modeStr, taskStartTime)
			return
		}
		if folder == "" {
			if notifyID > 0 && m.statusMod != nil && notifyID != m.statusMod.GetStatusMessageID(msg.ChatID) {
				_ = m.sender.DeleteMessage(ctx, peer, []int{notifyID})
			}
			return // the last task uploads everything
		}
		defer func() { _ = os.RemoveAll(filepath.Dir(folder)) }()
		downloadedPath = folder
		displayName = filepath.Base(folder)
		uTask.SetName(displayName)
	}

	if opt.Join {
		setStep(uTask, "Join Parts")
		done := trackStep(uTask, taskDir, pathSize(downloadedPath))
		downloadedPath = joinParts(downloadedPath)
		done()
	}

	// 2. Post-processing: Forceful Archive Extraction (/leech -e, -extract, -uz, -p)
	// Reads binary magic bytes (PK, 7z, Rar!, etc.) even if the file extension was disguised or missed
	if opt.IsExtract {
		uTask.SetStatus(types.StatusExtracting, ext.GetExtractEngineName())
		isArch, archType := ext.IsArchive(downloadedPath)
		if isArch {
			log.Printf("[LEECH -e] Detected archive format '%s' via binary header for: %s", archType, downloadedPath)
			extractDir := filepath.Join(taskDir, "extracted")
			done := trackStep(uTask, taskDir, pathSize(downloadedPath))
			outDir, err := ext.ForceExtractArchive(downloadedPath, extractDir, opt.Password)
			done()
			if err == nil {
				_ = os.Remove(downloadedPath)
				_ = os.Remove(downloadedPath + ".hatchzip")
				_ = os.Remove(downloadedPath + ".hatchzip-tail")
				downloadedPath = outDir
			} else {
				log.Printf("[LEECH -e] Extraction error: %v, continuing with original file", err)
				_ = m.sender.EditMessage(ctx, peer, notifyID, fmt.Sprintf("⌬ <b>Extraction Warning:</b> %v\nUploading original archive.", err), nil)
			}
		} else if fi, err := os.Stat(downloadedPath); err == nil && fi.IsDir() {
			_ = filepath.Walk(downloadedPath, func(p string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					if isA, aType := ext.IsArchive(p); isA {
						log.Printf("[LEECH -e] Extracting embedded %s archive: %s", aType, p)
						extDir := filepath.Join(filepath.Dir(p), "extracted")
						if _, eErr := ext.ForceExtractArchive(p, extDir, opt.Password); eErr == nil {
							_ = os.Remove(p)
							_ = os.Remove(p + ".hatchzip")
							_ = os.Remove(p + ".hatchzip-tail")
						}
					}
				}
				return nil
			})
		}
	}

	// media tools from our Python bot, before zip
	if opt.ExtractStrm {
		setStep(uTask, "Extract Streams (FFmpeg)")
		done := trackStep(uTask, taskDir, pathSize(downloadedPath))
		out, err := extractAllStreams(downloadedPath, filepath.Join(taskDir, "streams_"+ext.RandomHex(3)))
		done()
		if err == nil {
			_ = os.RemoveAll(downloadedPath)
			downloadedPath = out
		} else {
			log.Printf("[LEECH -st] %v", err)
		}
	}
	if opt.ExtractAud != "" {
		setStep(uTask, "Extract Audio (FFmpeg)")
		done := trackStep(uTask, taskDir, pathSize(downloadedPath))
		downloadedPath = extractAudio(downloadedPath, opt.ExtractAud)
		done()
	}
	if opt.ConvVideo != "" || opt.ConvAudio != "" {
		setStep(uTask, "Convert Media (FFmpeg)")
		done := trackStep(uTask, taskDir, pathSize(downloadedPath))
		downloadedPath = convertMedia(downloadedPath, opt.ConvVideo, opt.ConvAudio)
		done()
	}

	// Default Audio / Default Subtitle: now the tracks are known, the user picks one
	if vtSel != nil && vtSel.DefAudio == "ask" {
		setStep(uTask, "Waiting: choose the audio")
		vtSel.DefAudio = m.askTrack(ctx, peer, msg, downloadedPath, "audio")
	}
	if vtSel != nil && vtSel.DefSubs == "ask" {
		setStep(uTask, "Waiting: choose the subtitle")
		vtSel.DefSubs = m.askTrack(ctx, peer, msg, downloadedPath, "subs")
	}
	if vtSel != nil && vtSel.Translate != "" {
		if vtSel.TrStream == "ask" {
			setStep(uTask, "Waiting: choose the subtitle")
			vtSel.TrStream = m.askTrack(ctx, peer, msg, downloadedPath, "translate")
		}
	}
	if vtSel != nil && !vtSel.empty() {
		var shots int
		if vtSel.Translate != "" { // the real progress: the lines translated
			setStep(uTask, "Translating Subtitles")
			total := pathSize(downloadedPath)
			downloadedPath, shots = runVideoTools(downloadedPath, vtSel, func(done, all int) {
				if all > 0 {
					uTask.SetProgress(total*int64(done)/int64(all), total, 0, "-")
				}
			})
		} else {
			setStep(uTask, "Video Tools (FFmpeg)")
			done := trackStep(uTask, taskDir, pathSize(downloadedPath))
			downloadedPath, shots = runVideoTools(downloadedPath, vtSel)
			done()
		}
		if shots > 0 {
			opt.Screenshots = shots
		}
	}

	if opt.IsZip && !opt.IsExtract {
		uTask.SetStatus(types.StatusArchiving, "7-Zip")
		// Compression requested (-z / -zip)
		zipTarget := filepath.Join(taskDir, filepath.Base(downloadedPath)+".zip")
		done := trackStep(uTask, taskDir, pathSize(downloadedPath))
		zErr := ext.CompressToZip(downloadedPath, zipTarget, opt.Password)
		done()
		if err := zErr; err == nil {
			_ = os.RemoveAll(downloadedPath)
			downloadedPath = zipTarget
		}
	}

	// Custom FFmpeg commands (-ff)
	if opt.FFmpegKey != "" && !cfg.EnableFFmpegCmds {
		m.sendDownloadError(ctx, peer, msg, notifyID, "FFmpeg Cmds (-ff) are disabled on this bot.", modeStr, taskStartTime)
		return
	}
	if opt.FFmpegKey != "" && cfg.EnableFFmpegCmds {
		uTask.SetStatus(types.StatusFFmpeg, "FFmpeg")
		rawCmd := opt.FFmpegKey
		if user != nil && user.FFmpegCommands != nil {
			if preset, ok := user.FFmpegCommands[opt.FFmpegKey]; ok {
				rawCmd = preset
			}
		}
		ffDir := filepath.Join(taskDir, "ff_tmp")
		_ = os.MkdirAll(ffDir, 0755)
		outFFmpeg := filepath.Join(ffDir, filepath.Base(downloadedPath))
		done := trackStep(uTask, taskDir, pathSize(downloadedPath))
		fErr := ext.ExecuteCustomFFmpeg(downloadedPath, outFFmpeg, rawCmd, cfg.FFmpegEncoding)
		done()
		if fErr == nil {
			_ = os.Remove(downloadedPath)
			downloadedPath = outFFmpeg
		} else {
			log.Printf("[TJFF] Custom tjff error: %v", fErr)
		}
	}

	// 3. Post-processing: Metadata editing
	// Collect all files to process: single file or directory of files
	var filesToUpload []string
	if fi, err := os.Stat(downloadedPath); err == nil && fi.IsDir() {
		_ = filepath.Walk(downloadedPath, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				base := filepath.Base(path)
				if !strings.HasPrefix(base, ".") && !strings.HasSuffix(base, ".aria2") {
					filesToUpload = append(filesToUpload, path)
				}
			}
			return nil
		})
	} else {
		filesToUpload = []string{downloadedPath}
	}

	if len(filesToUpload) == 0 {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "× <b>Download Failed:</b> No payload files found", nil)
		return
	}

	// Queue Check for Upload (Sleep v0 Engine)
	var totalUploadSize int64
	for _, f := range filesToUpload {
		if fi, err := os.Stat(f); err == nil {
			totalUploadSize += fi.Size()
		}
	}

	queuedUP, qUpErr := ext.AcquireUploadQueue(ctx, taskUID, displayName, totalUploadSize, msg.ChatID, msg.UserID, notifyID, cancel)
	if qUpErr != nil {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "× <i>Upload task cancelled while in queue</i>", nil)
		return
	}
	defer ext.ReleaseUpload(taskUID)

	if queuedUP {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "<i>Starting upload from queue...</i>", nil)
	}

	dlDuration = time.Since(dlStartTime)
	if dlDuration.Seconds() < 1 {
		dlDuration = 1 * time.Second
	}
	ulStartTime = time.Now()

	deleteLinks() // fallback (e.g. multi-bot download needed the message until now)

	setStep(uTask, "Preparing Upload")
	taskThumb := ""
	if opt.ThumbURL != "" {
		if taskThumb = m.fetchTaskThumb(ctx, peer, opt.ThumbURL); taskThumb != "" {
			defer os.Remove(taskThumb)
		} else {
			log.Printf("[LEECH -t] could not get the thumbnail from %s", opt.ThumbURL)
		}
	}
	if m.statusMod != nil {
		go m.statusMod.UpdateAllMessages(context.Background(), true)
	}

	// "Leech Started" in the leech log, like the Python bot (CLEAN_LOG_MSG deletes it after the upload)
	logStartID, logStartPeer := m.sendLogStart(ctx, msg, sourceURL, opt.IsMirror)
	defer func() {
		if logStartID > 0 && config.Get().CleanLogMsg {
			_ = m.sender.DeleteMessage(context.Background(), logStartPeer, []int{logStartID})
		}
	}()

	var dumpID int64
	if opt.UserDump != 0 {
		dumpID = opt.UserDump
	} else if user != nil && user.DumpChatID != 0 {
		dumpID = user.DumpChatID
	}

	// /status shows the upload of all the files as one (like the Python bot), not file by file
	batchName := ""
	if len(filesToUpload) > 1 {
		batchName = displayName
	}
	uTask.BeginBatch(batchName, totalUploadSize)
	defer uTask.EndBatch()
	for _, curFile := range filesToUpload {
		userExcl := ""
		if user != nil {
			userExcl = user.ExcludedExts
		}
		if isExcludedFile(curFile, userExcl+" "+cfg.ExtensionFilter) {
			log.Printf("[AZML-Go Leech] Skipping excluded file: %s", filepath.Base(curFile))
			continue
		}
		origBase := filepath.Base(curFile)
		origBase = strings.TrimPrefix(origBase, "meta_")
		origBase = strings.TrimPrefix(origBase, "ff_")

		// Metadata editing
		if user != nil && len(user.Metadata) > 0 {
			setStep(uTask, "Metadata (FFmpeg)")
			metaDir := filepath.Join(taskDir, "meta_tmp")
			_ = os.MkdirAll(metaDir, 0755)
			outMeta := filepath.Join(metaDir, origBase)
			if err := ext.EditMetadata(curFile, outMeta, user.Metadata); err == nil {
				_ = os.Remove(curFile)
				curFile = outMeta
			} else {
				log.Printf("[LEECH Metadata] Warning: EditMetadata failed for %s: %v (using original)", origBase, err)
			}
		}

		// File Renaming
		finalName, caption := ext.FormatFilename(origBase, user, curFile)
		if caption != "" {
			font := cfg.CapFont // CAP_FONT from /bs; the user's own font in /us wins
			if user != nil && user.CapFont != "" {
				font = user.CapFont
			}
			caption = applyCaptionFont(caption, font)
		}
		if opt.CustomName != "" && len(filesToUpload) == 1 {
			finalName = opt.CustomName
		}
		if filepath.Ext(finalName) == "" {
			if origExt := filepath.Ext(origBase); origExt != "" {
				finalName += origExt
			} else if detectedExt := ext.DetectExtension(curFile); detectedExt != "" {
				finalName += detectedExt
			}
		}
		finalName = ext.SanitizeFilename(finalName)
		uTask.SetName(finalName)
		renamedPath := filepath.Join(filepath.Dir(curFile), finalName)
		if renamedPath != curFile {
			_ = os.Rename(curFile, renamedPath)
			curFile = renamedPath
		}
		uploadedNames = append(uploadedNames, finalName)

		// Screenshots
		var ssLinks []string // old Telegraph links (no longer filled)
		var ssFiles []string // screenshots sent as an album after the file
		userShots := 0
		if user != nil {
			userShots = user.Screenshots
		}
		if opt.Screenshots > 0 || userShots > 0 || cfg.ScreenshotsMode {
			ssCount := opt.Screenshots
			if ssCount <= 0 {
				ssCount = userShots
			}
			if ssCount <= 0 {
				ssCount = 4
			}
			if !opt.IsMirror {
				setStep(uTask, "Screenshots (FFmpeg)")
				var ssErr error
				if ssFiles, ssErr = ext.MakeScreenshots(curFile, ssCount); ssErr != nil {
					log.Printf("[AZML-Go Leech] Screenshots skipped for %s: %v", filepath.Base(curFile), ssErr)
				}
			}
		}
		if len(ssFiles) > 0 {
			defer os.RemoveAll(filepath.Dir(ssFiles[0]))
		}

		// Thumbnail & HD Cover
		customThumb := ""
		if user != nil && user.CustomThumb != "" {
			customThumb = user.CustomThumb
		}
		if taskThumb != "" {
			customThumb = taskThumb
		}
		autoThumbEnabled := user.AutoThumbOn(cfg.AutoThumbnail)

		// Effective upload as document resolution:
		// 1. Bot-wide default (cfg.AsDocument, default true)
		// 2. User setting overrides if user exists (user.AsDoc)
		// 3. Command flags (-d/-doc forces document, -m/-med/-media forces media)
		asDoc := cfg.AsDocument
		if user != nil {
			asDoc = user.AsDoc
		}
		if opt.AsDoc {
			asDoc = true
		} else if opt.AsMedia {
			asDoc = false
		}

		if customThumb == "" && autoThumbEnabled {
			setStep(uTask, "Auto Thumbnail (TMDB)")
			if tmdbThumb, err := ext.GetAutoThumbnail(ctx, finalName, asDoc, cfg.LeechFilenameRemnameRegex, msg.LangCode); err == nil && tmdbThumb != "" {
				customThumb = tmdbThumb
			}
		}
		setStep(uTask, "Thumbnail & Cover")
		thumbPath := ext.GenerateThumbnail(curFile, customThumb)
		coverPath := ext.GenerateVideoCover(curFile, customThumb)

		// File Splitting
		if !opt.IsMirror {
			uTask.SetStatus(types.StatusSplitting, "7-Zip")
		}
		maxSplit := config.GetMaxSplitSize()
		splitSize := cfg.LeechSplitSize
		if opt.SplitSize > 0 {
			splitSize = opt.SplitSize
		} else if user != nil && user.SplitSize > 0 {
			splitSize = user.SplitSize
		}
		if splitSize > maxSplit || splitSize <= 0 {
			splitSize = maxSplit
		}

		equalSplits := cfg.EqualSplits
		if user != nil {
			equalSplits = user.EqualSplits
		}

		splitDir := filepath.Join(taskDir, "splits")
		splitParts := []string{curFile}
		if !opt.IsMirror {
			var splitErr error
			var done func()
			if fi, sErr := os.Stat(curFile); sErr == nil && fi.Size() > splitSize {
				done = trackStep(uTask, splitDir, fi.Size()) // only when it really splits
			}
			_ = os.MkdirAll(splitDir, 0755)
			splitParts, splitErr = ext.SplitFile(curFile, splitSize, splitDir, equalSplits)
			if done != nil {
				done()
			}
			if splitErr != nil || len(splitParts) == 0 {
				splitParts = []string{curFile}
			}
		}

		fi, _ := os.Stat(curFile)
		var itemSize int64
		if fi != nil {
			itemSize = fi.Size()
		}
		totalSize += itemSize

		uTask.SetStatus(types.StatusUploading, ext.EngineName("telegram"))
		uTask.SetSize(itemSize)
		uTask.SetProgress(0, itemSize, 0, "-")

		if opt.IsMirror && user != nil && (user.MPrefix != "" || user.MSuffix != "" || user.MRemname != "") {
			curFile = mirrorRename(curFile, user) // Mirror Prefix / Suffix / Remname of /us
		}
		if opt.IsMirror && opt.MirrorHost == ext.RcloneHostID {
			if len(filesToUpload) > 1 && rcSess.FolderName == "" {
				rcSess.FolderName = displayName
				if fi2, e2 := os.Stat(downloadedPath); e2 == nil && fi2.IsDir() {
					rcSess.FolderName = filepath.Base(downloadedPath)
				}
			}
			// -up mrcc:remote:path = the user's own rclone.conf (like the Python bot), else the bot's
			dest, conf := opt.RcloneDest, "rclone.conf"
			if user != nil && user.UserTokens && len(user.RcloneConf) > 0 && !strings.HasPrefix(dest, "mrcc:") {
				// USER mode (like WZML-X): his rclone.conf, with his RClone Path
				if p := strings.TrimPrefix(user.RclonePath, "mrcc:"); p != "" {
					dest = p
				}
				dest = "mrcc:" + dest
			}
			if strings.HasPrefix(dest, "mrcc:") {
				dest = strings.TrimPrefix(dest, "mrcc:")
				conf = ""
				if user != nil && len(user.RcloneConf) > 0 {
					conf = fmt.Sprintf("rclone/%d.conf", msg.UserID)
					_ = os.MkdirAll("rclone", 0o755)
					_ = os.WriteFile(conf, user.RcloneConf, 0o600)
				}
				if conf == "" {
					m.sendDownloadError(ctx, peer, msg, notifyID, "Your rclone.conf is not set: /us → Mirror Settings → RClone", modeStr, taskStartTime)
					return
				}
			}
			uTask.SetStatus(types.StatusUploading, ext.RcloneHostName)
			flags := cfg.RcloneFlags
			if user != nil && user.RcloneFlags != "" {
				flags += " " + user.RcloneFlags
			}
			res, rErr := downloader.UploadToRclone(ctx, curFile, dest, conf, flags, rcSess, func(sent, total int64, speed float64) {
				eta := "-"
				if speed > 0 && total > sent {
					eta = ext.GetReadableTime(int64(float64(total-sent) / speed))
				}
				if speed > maxULSpeed {
					maxULSpeed = speed
				}
				uTask.SetProgress(sent, total, speed, eta)
			})
			if rErr != nil {
				log.Printf("[TJ RClone] upload error for %s: %v", curFile, rErr)
				m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("%s Upload Failed: %v", ext.RcloneHostName, rErr), modeStr, taskStartTime)
				return
			}
			mirrorLinks = append(mirrorLinks, res.Link)
			rcPaths = append(rcPaths, res.Path)
			ext.RecordDailyUsage(msg.UserID, itemSize)
			continue
		}

		if opt.IsMirror && opt.MirrorHost == ext.GDriveHostID {
			// TJ GDrive: several files of one task go into one Drive folder (like the Python bot)
			if len(filesToUpload) > 1 && gdSess.FolderName == "" {
				gdSess.FolderName = displayName
				if msg.SameDirKey != "" || downloadedPath != "" {
					if fi2, e2 := os.Stat(downloadedPath); e2 == nil && fi2.IsDir() {
						gdSess.FolderName = filepath.Base(downloadedPath)
					}
				}
			}
			// like WZML-X: mtp:<id> or USER mode = his own token.pickle (and his Drive folder),
			// otherwise the bot's token.pickle; his GDrive ID / Index URL win over the bot's
			gdOpt, gdErr := gdriveTarget(cfg, user, opt.RcloneDest)
			if gdErr != nil {
				m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("%s Upload Failed: %v", ext.GDriveHostName, gdErr), modeStr, taskStartTime)
				return
			}
			gdUserToken = gdOpt.Client != nil
			uTask.SetStatus(types.StatusUploading, ext.GDriveHostName)
			gdStart := time.Now()
			res, gErr := downloader.UploadToGDrive(ctx, curFile, gdOpt, gdSess, func(sent, total int64) {
				secs := time.Since(gdStart).Seconds()
				if secs < 0.5 {
					return
				}
				speed := float64(sent) / secs
				eta := "-"
				if speed > 0 && total > sent {
					eta = ext.GetReadableTime(int64(float64(total-sent) / speed))
				}
				if speed > maxULSpeed {
					maxULSpeed = speed
				}
				uTask.SetProgress(sent, total, speed, eta)
			})
			if gErr != nil {
				log.Printf("[TJ GDrive] upload error for %s: %v", curFile, gErr)
				m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("%s Upload Failed: %v", ext.GDriveHostName, gErr), modeStr, taskStartTime)
				return
			}
			primary := res.DriveLink
			if cfg.DisableDriveLink && !gdUserToken && res.IndexLink != "" {
				primary = res.IndexLink
			}
			mirrorLinks = append(mirrorLinks, primary)
			gdIndexLinks = append(gdIndexLinks, res.IndexLink)
			ext.RecordDailyUsage(msg.UserID, itemSize)
			continue
		}

		if opt.IsMirror {
			hostID := opt.MirrorHost
			if hostID == "" {
				hostID = ext.MirrorHosts[0].ID
			}
			hostName := hostID
			if h, ok := ext.FindMirrorHost(hostID); ok {
				hostName = h.Name
			}
			uTask.SetStatus(types.StatusUploading, hostName)
			mirrorStart := time.Now()
			link, gErr := ext.UploadToHost(ctx, hostID, curFile, mirrorSess, func(sent, total int64) {
				secs := time.Since(mirrorStart).Seconds()
				if secs < 0.5 {
					return
				}
				speed := float64(sent) / secs
				eta := "-"
				if speed > 0 && total > sent {
					eta = ext.GetReadableTime(int64(float64(total-sent) / speed))
				}
				if speed > maxULSpeed {
					maxULSpeed = speed
				}
				uTask.SetProgress(sent, total, speed, eta)
			})
			if gErr != nil {
				log.Printf("[AZML-Go Mirror] %s upload error for %s: %v", hostName, curFile, gErr)
				m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("%s Upload Failed: %v", hostName, gErr), modeStr, taskStartTime)
				return
			}
			mirrorLinks = append(mirrorLinks, link)
			ext.RecordDailyUsage(msg.UserID, itemSize)
			continue
		}

		useGroup := len(splitParts) > 1 && cfg.MediaGroup
		if user != nil {
			useGroup = len(splitParts) > 1 && user.MediaGroup
		}
		partResults := make([]uploader.UploadResult, len(splitParts))
		var fileButtons *telegram.ButtonMaker
		if user.MediaInfoOn(cfg.ShowMediaInfo) {
			setStep(uTask, "MediaInfo")
			if miLink := mediaInfoLink(curFile, finalName); miLink != "" {
				fileButtons = telegram.NewButtonMaker()
				fileButtons.UButtonStyled("📃 MediaInfo", miLink, "primary")
			}
		}
		if len(ssLinks) > 0 {
			if fileButtons == nil {
				fileButtons = telegram.NewButtonMaker()
			}
			fileButtons.UButtonStyled("🖼 ScreenShots", ssLinks[0], "primary")
		}
		if fileButtons != nil {
			fileButtons.Row()
		}
		// like the Python bot: Save Message only where others see the file (leech log or a group)
		if cfg.SaveMsg && (cfg.LeechLogID != 0 || msg.ChatID != msg.UserID) {
			if fileButtons == nil {
				fileButtons = telegram.NewButtonMaker()
			}
			saveMsgButton(fileButtons)
		}
		uTask.SetStatus(types.StatusUploading, ext.EngineName("telegram"))
		var groupOpt, lastUpOpt uploader.UploadOptions
		for idx, part := range splitParts {
			partCaption := caption
			if len(splitParts) > 1 {
				partCaption = fmt.Sprintf("<b>Part %d/%d</b>\n%s", idx+1, len(splitParts), caption)
			}

			botPM := cfg.BotPM || (user != nil && user.BotPM) || (msg.ChatID != msg.UserID)

			upOpt := uploader.UploadOptions{
				FilePath:       part,
				FileName:       filepath.Base(part),
				Caption:        partCaption,
				ThumbPath:      thumbPath,
				CoverPath:      coverPath,
				AsDoc:          asDoc,
				DumpChatID:     dumpID,
				LogChatID:      cfg.LeechLogID,
				NoLog:          isExcepChat(msg.ChatID),
				UserID:         msg.UserID,
				UserAccessHash: telegram.GetUserAccessHash(msg.UserID),
				BotPM:          botPM,
				IsPremium:      isPremium,
				GID:            taskUID,
				OnProgress: func(curr, total int64, speed float64, eta string) {
					if speed > maxULSpeed {
						maxULSpeed = speed
					}
					uTask.SetProgress(curr, total, speed, eta)
				},
			}

			upOpt.Result = &partResults[idx]
			upOpt.LogReplyTo = logStartID
			upOpt.PMReplyTo = pmStartID
			if useGroup {
				partCaption = upOpt.Caption + captionLinks(fileButtons)
				upOpt.Caption = partCaption
			} else {
				upOpt.Buttons = fileButtons
			}
			lastUpOpt = upOpt
			if useGroup {
				upOpt.SkipDelivery = true
				groupOpt = upOpt
			}
			_, upErr := m.uploader.UploadFile(ctx, peer, upOpt)
			if upErr != nil {
				log.Printf("[AZML-Go Leech] Upload error for %s: %v", part, upErr)
				m.sendDownloadError(ctx, peer, msg, notifyID, fmt.Sprintf("Upload Failed: %v", upErr), modeStr, taskStartTime)
				return
			}
		}
		if useGroup {
			var done []uploader.UploadResult
			for _, r := range partResults {
				if r.MsgID > 0 {
					done = append(done, r)
				}
			}
			if first := m.uploader.SendAlbum(ctx, done, groupOpt); first.MsgID > 0 {
				partResults[0] = first
			}
		}
		if user != nil && user.SampleSec > 0 && !opt.IsMirror {
			setStep(uTask, "Sample Video (FFmpeg)")
			if samplePath := ext.MakeSampleVideo(curFile, user.SampleSec); samplePath != "" {
				uTask.SetStatus(types.StatusUploading, "Sample Video")
				sampleOpt := groupOpt
				if !useGroup {
					sampleOpt = lastUpOpt
				}
				sampleOpt.FilePath = samplePath
				sampleOpt.FileName = filepath.Base(samplePath)
				sampleOpt.Caption = fmt.Sprintf("<b>Sample</b> | <code>%s</code>", telegram.EscapeHTML(finalName))
				sampleOpt.SkipDelivery = false
				sampleOpt.Result = nil
				sampleOpt.OnProgress = nil
				sampleOpt.Buttons = nil
				if _, sErr := m.uploader.UploadFile(ctx, peer, sampleOpt); sErr != nil {
					log.Printf("[AZML-Go Leech] Sample upload failed: %v", sErr)
				}
				_ = os.Remove(samplePath)
			}
		}
		if len(ssFiles) > 0 && len(partResults) > 0 && partResults[0].MsgID > 0 {
			setStep(uTask, "Screenshots")
			dest := partResults[0].ChatID
			copyTo := []int64{msg.UserID}
			if lg := cfg.LeechLogID; lg != 0 && lg != dest {
				copyTo = append(copyTo, lg)
			}
			caption := fmt.Sprintf("<b>Screenshots</b> | <code>%s</code>", telegram.EscapeHTML(finalName))
			if err := uploader.SendScreenshots(dest, partResults[0].MsgID, ssFiles, caption, copyTo); err != nil {
				log.Printf("[AZML-Go Leech] Screenshots album failed: %v", err)
			}
		}
		if len(splitParts) > 1 && len(uploadedNames) > 0 {
			// like the Python bot: every part on its own line, Total Files counts the parts
			uploadedNames = uploadedNames[:len(uploadedNames)-1]
			for idx, part := range splitParts {
				uploadedNames = append(uploadedNames, filepath.Base(part))
				link := ""
				switch {
				case useGroup && len(partResults) > 0 && partResults[0].LogMsgID > 0:
					link = channelMessageLink(partResults[0].LogChatID, partResults[0].LogMsgID+idx) // the album in the leech log
				case useGroup && len(partResults) > 0 && partResults[0].MsgID > 0:
					link = channelMessageLink(partResults[0].ChatID, partResults[0].MsgID+idx) // album = consecutive ids
				case idx < len(partResults):
					link = resultLink(partResults[idx])
				}
				fileLinks = append(fileLinks, link)
			}
		} else if len(partResults) > 0 {
			fileLinks = append(fileLinks, resultLink(partResults[0]))
		} else {
			fileLinks = append(fileLinks, "")
		}

		if thumbPath != "" && thumbPath != customThumb {
			_ = os.Remove(thumbPath)
		}
		if coverPath != "" && coverPath != customThumb {
			_ = os.Remove(coverPath)
		}
		ext.RecordDailyUsage(msg.UserID, itemSize)
	}

	ulDuration := time.Since(ulStartTime)
	if ulDuration.Seconds() < 1 {
		ulDuration = 1 * time.Second
	}
	overallElapsed := time.Since(taskStartTime)
	if overallElapsed.Seconds() < 1 {
		overallElapsed = 1 * time.Second
	}

	avgDLSpeed := float64(totalSize) / dlDuration.Seconds()
	avgULSpeed := float64(totalSize) / ulDuration.Seconds()
	if maxDLSpeed <= 0 {
		maxDLSpeed = avgDLSpeed * 1.35
	}
	if maxULSpeed <= 0 {
		maxULSpeed = avgULSpeed * 1.25
	}

	// The finish message of the Python bot (themes: NAME, SIZE, ELAPSE, MODE, L_TOTAL_FILES, L_CC)
	var sb strings.Builder
	mainName := displayName
	if len(uploadedNames) == 1 {
		mainName = uploadedNames[0]
	}
	if cfg.SafeMode && msg.ChatID != msg.UserID {
		mainName = "Task has been Completed!"
	}
	userTag := fmt.Sprintf("@%s", msg.Username)
	if msg.Username == "" {
		userTag = fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", msg.UserID, telegram.EscapeHTML(msg.SenderName))
	}
	sb.WriteString(fmt.Sprintf("<blockquote><b>╭</b> <b>Name:</b> <code>%s</code>\n", telegram.EscapeHTML(mainName)))
	sb.WriteString(fmt.Sprintf("┠ <b>Size: </b>%s\n", ext.GetReadableFileSize(totalSize)))
	sb.WriteString(fmt.Sprintf("┠ <b>Elapsed: </b>%s\n", ext.GetReadableTime(int64(overallElapsed.Seconds()))))
	sb.WriteString(fmt.Sprintf("┠ <b>Mode: </b>%s\n", pythonMode(modeStr, opt)))
	if opt.IsMirror {
		typ := "Folder"
		if len(uploadedNames) == 1 {
			typ = mimeOfName(uploadedNames[0])
		} else {
			sb.WriteString(fmt.Sprintf("┠ <b>Type: </b>%s\n", typ))
			sb.WriteString(fmt.Sprintf("┠ <b>Files: </b>%d\n", len(uploadedNames)))
			typ = ""
		}
		if typ != "" {
			sb.WriteString(fmt.Sprintf("┠ <b>Type: </b>%s\n", typ))
		}
		sb.WriteString(fmt.Sprintf("<b>╰</b> <b>By: </b>%s</blockquote>\n\n", userTag)) // the empty line after the quote, like leech
	} else {
		sb.WriteString(fmt.Sprintf("┠ <b>Total Files: </b>%d\n", len(uploadedNames)))
		sb.WriteString(fmt.Sprintf("<b>╰</b> <b>By: </b>%s</blockquote>\n\n", userTag))
	}
	_, _ = avgDLSpeed, avgULSpeed

	botPM := cfg.BotPM || (user != nil && user.BotPM) || (msg.ChatID != msg.UserID)
	if opt.IsMirror {
		if len(uploadedNames) > 1 { // one file: its link is the button, like the Python bot
			sb.WriteString(finishFileList(uploadedNames, mirrorLinks))
		}
	} else {
		if botPM {
			sb.WriteString("➲ <b><i>File(s) have been Sent to Bot PM (Private)</i></b>\n")
		} else {
			sb.WriteString("➲ <b><i>File(s) have been Sent. Access via Links...</i></b>\n")
		}
		names := uploadedNames
		if cfg.SafeMode { // SAFE_MODE: no names, only the links
			names = make([]string, len(uploadedNames))
			for i := range names {
				names[i] = fmt.Sprintf("File %d", i+1)
			}
		}
		if user.MediaStoreOn(cfg.MediaStore) {
			names = withStoreLinks(names, fileLinks)
		}
		sb.WriteString(finishFileList(names, fileLinks))
	}

	botUser := telegram.CurrentBotUsername
	if botUser == "" {
		botUser = ""
	}
	// the link buttons of a mirror (up to 3 different links)
	addMirrorLinks := func(btn *telegram.ButtonMaker) {
		if opt.MirrorHost == ext.RcloneHostID {
			seen := map[string]bool{}
			for _, link := range mirrorLinks {
				if link != "" && !seen[link] && len(seen) < 3 {
					seen[link] = true
					btn.UButtonStyled("📎 "+ext.RcloneHostName+" Link", link, "primary")
					btn.Row()
				}
			}
			return
		}
		if opt.MirrorHost == ext.GDriveHostID { // Drive Link (unless DISABLE_DRIVE_LINK) and Index Link
			seen := map[string]bool{}
			for i, link := range mirrorLinks {
				idx := ""
				if i < len(gdIndexLinks) {
					idx = gdIndexLinks[i]
				}
				if (!cfg.DisableDriveLink || gdUserToken) && link != "" && link != idx && !seen[link] {
					seen[link] = true
					btn.UButtonStyled("☁️ Drive Link", link, "primary")
					btn.Row()
				}
				if idx != "" && !seen[idx] {
					seen[idx] = true
					btn.UButtonStyled("⚡ Index Link", idx, "primary")
					// like the Python bot: a video / audio / image file also gets the index player
					if !strings.HasSuffix(idx, "/") {
						if mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(idx))); strings.HasPrefix(mt, "video/") ||
							strings.HasPrefix(mt, "audio/") || strings.HasPrefix(mt, "image/") || strings.HasSuffix(strings.ToLower(idx), ".mkv") {
							btn.UButtonStyled("🌐 View Link", idx+"?a=view", "primary")
						}
					}
					btn.Row()
				}
				if len(seen) >= 4 {
					break
				}
			}
			return
		}
		seen := map[string]bool{}
		var uniq []string
		for _, link := range mirrorLinks {
			if !seen[link] {
				seen[link] = true
				uniq = append(uniq, link)
			}
		}
		for idx, link := range uniq {
			if idx >= 3 {
				break
			}
			label := "📎 " + mirrorHostName(opt.MirrorHost) + " Link"
			if len(uniq) > 1 {
				label = fmt.Sprintf("%s %d", label, idx+1)
			}
			btn.UButtonStyled(label, link, "primary")
			btn.Row()
		}
	}
	if opt.IsMirror && opt.MirrorHost == ext.RcloneHostID && len(rcPaths) > 0 { // like the Python bot: the path
		seenP := map[string]bool{}
		for _, p := range rcPaths {
			if !seenP[p] {
				seenP[p] = true
				sb.WriteString("➲ <b>Path:</b> <code>" + telegram.EscapeHTML(p) + "</code>\n")
			}
		}
	}
	showSource := cfg.SourceLink && !cfg.SafeMode && (ext.IsURL(sourceURL) || strings.HasPrefix(sourceURL, "https://t.me/")) && !ext.IsMagnet(sourceURL)

	// a mirror with Bot PM (BOT_PM or the user's /us) from a group, like the Python bot: the links
	// go to the user's PM; the group gets the message with "View in Bot PM" and, only without
	// MIRROR_LOG_ID, the link buttons too
	mirrorToPM := false
	if opt.IsMirror && msg.ChatID != msg.UserID && (cfg.BotPM || (user != nil && user.BotPM)) {
		pmBtn := telegram.NewButtonMaker()
		addMirrorLinks(pmBtn)
		if showSource {
			pmBtn.UButtonStyled("🔐 Source Link", sourceURL, "primary")
			pmBtn.Row()
		}
		pm := &tg.InputPeerUser{UserID: msg.UserID, AccessHash: telegram.GetUserAccessHash(msg.UserID)}
		if id, err := m.sender.SendMessageWithButtons(ctx, pm, sb.String(), pmStartID, pmBtn, 1); err == nil && id > 0 {
			mirrorToPM = true
		}
	}

	finishBtn := telegram.NewButtonMaker()
	if opt.IsMirror {
		if !mirrorToPM || cfg.MirrorLogID == 0 {
			addMirrorLinks(finishBtn)
		}
		if mirrorToPM && botUser != "" {
			finishBtn.IButtonStyled("📥 View in Bot PM", fmt.Sprintf("start_botpm %d", msg.UserID), "primary")
			finishBtn.Row()
		}
	} else {
		if botUser != "" && msg.ChatID != msg.UserID { // in PM the files are already here
			finishBtn.IButtonStyled("📥 View in Bot PM", fmt.Sprintf("start_botpm %d", msg.UserID), "primary")
			finishBtn.Row() // one button per row
		}
	}

	if showSource {
		finishBtn.UButtonStyled("🔐 Source Link", sourceURL, "primary")
		finishBtn.Row()
	}
	if cfg.SaveMsg && msg.ChatID != msg.UserID { // in PM there is nothing to save
		saveMsgButton(finishBtn)
	}
	groupText := sb.String()
	if mirrorToPM {
		groupText += "➲ <b><i>Link(s) have been Sent to Bot PM (Private)</i></b>\n"
	}
	// Send completion message into the group / chat where leech was initiated, replying to user's command
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, groupText, msg.MessageID, finishBtn, 1)

	// MIRROR_LOG_ID: the mirror finish message with its buttons (like the Python bot)
	if opt.IsMirror && cfg.MirrorLogID != 0 && !isExcepChat(msg.ChatID) && !gdUserToken {
		if lp := m.logPeerFor(ctx, cfg.MirrorLogID); lp != nil {
			logBtn := telegram.NewButtonMaker()
			addMirrorLinks(logBtn)
			if showSource {
				logBtn.UButtonStyled("🔐 Source Link", sourceURL, "primary")
				logBtn.Row()
			}
			if cfg.SaveMsg {
				saveMsgButton(logBtn)
			}
			if _, err := m.sender.SendMessageWithButtons(ctx, lp, sb.String(), 0, logBtn, 1); err != nil {
				log.Printf("[MIRROR_LOG] %v", err)
			}
		}
	}

	if user != nil {
		mode := "Leech"
		if opt.IsMirror {
			mode = mirrorHostName(opt.MirrorHost)
		}
		for idx, name := range uploadedNames {
			link := ""
			if opt.IsMirror && idx < len(mirrorLinks) {
				link = mirrorLinks[idx]
			} else if idx < len(fileLinks) {
				link = fileLinks[idx]
			}
			addHistory(user, types.HistoryEntry{Name: name, Size: totalSize / int64(len(uploadedNames)), Mode: mode, Link: link, Time: time.Now().Unix()})
		}
		_ = ext.DB.SaveUser(user)
	}

	// the "Task Started" in PM goes after the upload, like the Python bot: when DELETE_LINKS is
	// off (the source is still in the group) or CLEAN_LOG_MSG is on
	pmFinished = true // the files reached the PM: the rule of the Python bot below
	if pmStartID > 0 && (!cfg.DeleteLinks || cfg.CleanLogMsg) {
		pm := &tg.InputPeerUser{UserID: msg.UserID, AccessHash: telegram.GetUserAccessHash(msg.UserID)}
		_ = m.sender.DeleteMessage(context.Background(), pm, []int{pmStartID})
	}

	m.taskMgr.RemoveTask(taskUID)
	if m.taskMgr.Count() == 0 {
		if m.statusMod != nil {
			m.statusMod.DeleteAllMessages(ctx)
		}
	} else {
		if m.statusMod != nil {
			go m.statusMod.UpdateAllMessages(context.Background(), true)
		}
	}
}

func splitArgsRespectingQuotes(s string) []string {
	var tokens []string
	var current strings.Builder
	inDouble := false
	inSingle := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '"':
			if !inSingle {
				inDouble = !inDouble
			} else {
				current.WriteByte(ch)
			}
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			} else {
				current.WriteByte(ch)
			}
		case ' ', '\t', '\n', '\r':
			if inDouble || inSingle {
				current.WriteByte(ch)
			} else if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func isLeechFlag(tok string) bool {
	switch tok {
	case "-b", "-bulk", "-z", "-zip", "-e", "-extract", "-uz", "-unzip",
		"-d", "-doc", "-asdoc", "-m", "-sd", "-samedir", "-med", "-media", "-asmedia",
		"-st", "-streams", "-n", "-name", "-p", "-password",
		"-pssw", "-ss", "-screenshots", "-ud", "-dump", "-ff", "-i", "-s", "-select",
		"-t", "-thumb", "-sp", "-h", "-headers", "-u", "-user", "-ea", "-cv", "-ca", "-j", "-join", "-pass", "-vt":
		return true
	default:
		return false
	}
}

func parseLeechArgs(text string) (types.LeechOptions, string) {
	var opt types.LeechOptions
	tokens := splitArgsRespectingQuotes(text)
	if len(tokens) <= 1 {
		return opt, ""
	}

	var link string
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		switch tok {
		case "-b", "-bulk":
			opt.Bulk = true
			if i+1 < len(tokens) && !isLeechFlag(tokens[i+1]) && !ext.IsURL(tokens[i+1]) && !ext.IsMagnet(tokens[i+1]) && strings.Contains(tokens[i+1], ":") {
				opt.BulkRange = tokens[i+1]
				i++
			}
		case "-z", "-zip":
			opt.IsZip = true
		case "-e", "-extract", "-uz", "-unzip":
			opt.IsExtract = true
		case "-d", "-doc", "-asdoc":
			opt.AsDoc = true
		case "-med", "-media", "-asmedia":
			opt.AsMedia = true
		case "-m", "-sd", "-samedir":
			var np []string
			for i+1 < len(tokens) && !isLeechFlag(tokens[i+1]) && !ext.IsURL(tokens[i+1]) && !ext.IsMagnet(tokens[i+1]) {
				np = append(np, tokens[i+1])
				i++
			}
			opt.SameDir = strings.Join(np, " ")
			if opt.SameDir == "" {
				opt.SameDir = "Leech Folder"
			}
		case "-st", "-streams":
			opt.ExtractStrm = true
		case "-n", "-name":
			var nameParts []string
			for i+1 < len(tokens) {
				nextTok := tokens[i+1]
				if isLeechFlag(nextTok) || ext.IsURL(nextTok) || ext.IsMagnet(nextTok) || ext.IsMegaLink(nextTok) || strings.HasPrefix(nextTok, "tg://") {
					break
				}
				nameParts = append(nameParts, nextTok)
				i++
			}
			if len(nameParts) > 0 {
				opt.CustomName = strings.Join(nameParts, " ")
			}
		case "-p", "-password", "-pssw":
			if i+1 < len(tokens) {
				opt.Password = tokens[i+1]
				i++
			}
		case "-ss", "-screenshots":
			if i+1 < len(tokens) {
				opt.Screenshots, _ = strconv.Atoi(tokens[i+1])
				i++
			}
		case "-ud", "-dump":
			if i+1 < len(tokens) {
				opt.UserDump, _ = strconv.ParseInt(tokens[i+1], 10, 64)
				i++
			}
		case "-ff":
			if i+1 < len(tokens) {
				opt.FFmpegKey = tokens[i+1]
				i++
			}
		case "-i":
			if i+1 < len(tokens) {
				opt.Multi, _ = strconv.Atoi(tokens[i+1])
				i++
			}
		case "-s", "-select":
			opt.Select = true
		case "-t", "-thumb":
			if i+1 < len(tokens) {
				opt.ThumbURL = tokens[i+1]
				i++
			}
		case "-sp":
			if i+1 < len(tokens) {
				opt.SplitSize = parseSizeArg(tokens[i+1])
				i++
			}
		case "-u", "-user":
			if i+1 < len(tokens) {
				opt.Username = tokens[i+1]
				i++
			}
		case "-pass":
			if i+1 < len(tokens) {
				opt.Password = tokens[i+1]
				i++
			}
		case "-h", "-headers":
			var hp []string
			for i+1 < len(tokens) && !isLeechFlag(tokens[i+1]) {
				hp = append(hp, tokens[i+1])
				i++
			}
			opt.Headers = strings.Join(hp, " ")
		case "-ea":
			opt.ExtractAud = "mp3"
			if i+1 < len(tokens) {
				if _, ok := audioCodecArgs[strings.ToLower(tokens[i+1])]; ok {
					opt.ExtractAud = strings.ToLower(tokens[i+1])
					i++
				}
			}
		case "-cv":
			if i+1 < len(tokens) {
				opt.ConvVideo = strings.ToLower(strings.TrimPrefix(tokens[i+1], "."))
				i++
			}
		case "-ca":
			if i+1 < len(tokens) {
				opt.ConvAudio = strings.ToLower(strings.TrimPrefix(tokens[i+1], "."))
				i++
			}
		case "-j", "-join":
			opt.Join = true
		case "-vt":
			opt.VT = true
		default:
			if strings.HasPrefix(tok, "-p") && len(tok) > 2 {
				opt.Password = strings.TrimPrefix(tok, "-p")
			} else if strings.HasPrefix(strings.ToLower(tok), "pssw:") {
				val := strings.TrimPrefix(tok, "pssw:")
				if val != "" {
					opt.Password = val
				} else if i+1 < len(tokens) {
					opt.Password = tokens[i+1]
					i++
				}
			} else if strings.HasPrefix(strings.ToLower(tok), "password:") {
				val := strings.TrimPrefix(tok, "password:")
				if val != "" {
					opt.Password = val
				} else if i+1 < len(tokens) {
					opt.Password = tokens[i+1]
					i++
				}
			} else if ext.IsURL(tok) || ext.IsMagnet(tok) || ext.IsMegaLink(tok) || strings.HasPrefix(tok, "tg://") || isLocalTorrent(tok) {
				link = tok
			}
		}
	}
	// If password provided and user didn't specify zip, default to extract
	if opt.Password != "" && !opt.IsZip && opt.Username == "" {
		opt.IsExtract = true
	}
	return opt, link
}

// mirrorHostName is the display name of a /mirror host id (first host when empty).
func mirrorHostName(id string) string {
	if id == ext.GDriveHostID {
		return ext.GDriveHostName
	}
	if id == ext.RcloneHostID {
		return ext.RcloneHostName
	}
	if id == "" {
		id = ext.MirrorHosts[0].ID
	}
	if h, ok := ext.FindMirrorHost(id); ok {
		return h.Name
	}
	return id
}

// applyCaptionFont wraps the caption in the user's Caption Font tag.
func applyCaptionFont(caption, font string) string {
	tag := map[string]string{"b": "b", "i": "i", "u": "u", "s": "s", "code": "code", "spoiler": "tg-spoiler"}[font]
	if tag == "" {
		return caption
	}
	return fmt.Sprintf("<%s>%s</%s>", tag, caption, tag)
}

// isExcludedFile reports whether the file's extension is in the user's excluded list.
func isExcludedFile(path, excluded string) bool {
	if strings.TrimSpace(excluded) == "" {
		return false
	}
	name := strings.ToLower(filepath.Base(path))
	for _, e := range strings.Fields(strings.ToLower(excluded)) {
		if strings.HasSuffix(name, e) {
			return true
		}
	}
	return false
}

// channelMessageLink is the t.me link of a message in a channel ("" for other chats).
func channelMessageLink(chatID int64, msgID int) string {
	if msgID <= 0 {
		return ""
	}
	id := fmt.Sprintf("%d", chatID)
	if !strings.HasPrefix(id, "-100") {
		return ""
	}
	return fmt.Sprintf("https://t.me/c/%s/%d", strings.TrimPrefix(id, "-100"), msgID)
}

// userChannelPeer finds the dump channel as the user session sees it (its own access hash).
func userChannelPeer(ctx context.Context, client *tg.Client, chatID int64) (*tg.InputPeerChannel, error) {
	if h := telegram.ResolveUserChannelHash(ctx, client, chatID); h != 0 {
		return &tg.InputPeerChannel{ChannelID: pool.RawChannelID(chatID), AccessHash: h}, nil
	}
	return nil, fmt.Errorf("the user session is not a member of the dump channel %d", chatID)
}

// userSessionDownload: the bot forwards the file to the dump channel, the user session downloads
// that copy, then the copy is deleted.
func (m *LeechModule) userSessionDownload(ctx context.Context, peer tg.InputPeerClass, msgID int, dumpChatID int64, size int64, destPath string, onProgress func(curr, total int64, speed float64, eta string)) error {
	rawDump := pool.RawChannelID(dumpChatID)
	botHash := telegram.ResolveChannelAccessHash(ctx, m.rawAPI, dumpChatID)
	if botHash == 0 {
		return fmt.Errorf("bot can't access the dump channel")
	}
	botDump := &tg.InputPeerChannel{ChannelID: rawDump, AccessHash: botHash}
	fwdID, err := telegram.ForwardToChannelWithFloodWait(ctx, m.rawAPI, peer, botDump, msgID)
	if err != nil && tgerr.Is(err, "CHANNEL_INVALID", "CHANNEL_PRIVATE", "PEER_ID_INVALID") {
		// a stale hash (e.g. after a restart): resolve it again and retry once
		telegram.ForgetChannelAccessHash(rawDump)
		if botHash = telegram.ResolveChannelAccessHash(ctx, m.rawAPI, dumpChatID); botHash != 0 {
			botDump = &tg.InputPeerChannel{ChannelID: rawDump, AccessHash: botHash}
			fwdID, err = telegram.ForwardToChannelWithFloodWait(ctx, m.rawAPI, peer, botDump, msgID)
		}
	}
	if err != nil || fwdID <= 0 {
		return fmt.Errorf("forward to dump failed: %v", err)
	}
	defer func() {
		_, _ = m.rawAPI.ChannelsDeleteMessages(context.Background(), &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: rawDump, AccessHash: botHash},
			ID:      []int{fwdID},
		})
	}()

	userDump, err := userChannelPeer(ctx, m.userRawAPI, dumpChatID)
	if err != nil {
		return err
	}
	fetchDoc := func() (*tg.Document, error) {
		umsg, err := telegram.FetchMessage(ctx, m.userRawAPI, userDump, fwdID)
		if err != nil || umsg == nil {
			return nil, fmt.Errorf("user session can't read the copy: %v", err)
		}
		md, ok := umsg.Media.(*tg.MessageMediaDocument)
		if !ok {
			return nil, fmt.Errorf("copy has no document")
		}
		d, ok := md.Document.(*tg.Document)
		if !ok {
			return nil, fmt.Errorf("copy has no document")
		}
		return d, nil
	}
	doc, err := fetchDoc()
	if err != nil {
		// the saved user-session hash may be stale: look the channel up again once
		telegram.CacheUserSessionChannel(pool.RawChannelID(dumpChatID), 0)
		if userDump, err = userChannelPeer(ctx, m.userRawAPI, dumpChatID); err != nil {
			return err
		}
		if doc, err = fetchDoc(); err != nil {
			return err
		}
	}
	// fastest: a multi-connection pool opened right on the DC that holds the file
	dlTD, where := telegram.ActiveMediaTD(), "media servers"
	if dlTD == nil {
		dlTD, where = telegram.ActiveUserTD(), "normal servers"
	}
	if dcClients, closeFn := dcDownloadClients(ctx, dlTD, doc.DCID); len(dcClients) > 0 {
		defer closeFn()
		log.Printf("[LEECH] User session download on DC%d (%s) with %d connections", doc.DCID, where, dcPoolSize)
		dl := downloader.NewTelegramChunkDownloaderPool(dcClients)
		err := dl.DownloadToFile(ctx, doc.AsInputDocumentFileLocation(""), size, destPath, onProgress, func() ([]byte, error) {
			d, err := fetchDoc()
			if err != nil {
				return nil, err
			}
			return d.FileReference, nil
		})
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("[LEECH] DC pool download failed (%v), trying the user session pool", err)
	}
	clients := []*tg.Client{m.userRawAPI}
	for _, c := range telegram.ActiveUserPool() {
		if c != m.userRawAPI {
			clients = append(clients, c)
		}
	}
	log.Printf("[LEECH] User session download over %d connection(s)", len(clients))
	chunkDL := downloader.NewTelegramChunkDownloaderPool(clients)
	return chunkDL.DownloadToFile(ctx, doc.AsInputDocumentFileLocation(""), size, destPath, onProgress, func() ([]byte, error) {
		d, err := fetchDoc()
		if err != nil {
			return nil, err
		}
		return d.FileReference, nil
	})
}

// ---- Retry and /history ----

type retryEntry struct {
	msg *types.MessageContext
	at  time.Time
}

var (
	retryMu    sync.Mutex
	retryStore = map[string]retryEntry{}
)

// retryMarkup keeps the failed command so the Retry button can run it again (for 1 hour).
func retryMarkup(msg *types.MessageContext) tg.ReplyMarkupClass {
	if msg == nil {
		return nil
	}
	key := ext.RandomHex(4)
	cp := *msg
	retryMu.Lock()
	for k, v := range retryStore {
		if time.Since(v.at) > time.Hour {
			delete(retryStore, k)
		}
	}
	retryStore[key] = retryEntry{msg: &cp, at: time.Now()}
	retryMu.Unlock()
	btn := telegram.NewButtonMaker()
	btn.IButtonStyled("🔄 Retry", fmt.Sprintf("retry %d %s", msg.UserID, key), "primary")
	btn.IButtonStyled("❌", fmt.Sprintf("start_close %d", msg.UserID), "danger")
	btn.Row()
	return btn.BuildMenu(2)
}

// HandleRetryCallback runs a failed task again: "retry <userID> <key>".
func (m *LeechModule) HandleRetryCallback(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
	parts := strings.Fields(data)
	if len(parts) < 3 || parts[1] != fmt.Sprintf("%d", fromUID) {
		_ = m.sender.AnswerCallback(ctx, queryID, "𝙏𝙝𝙖𝙩'𝙨 𝙉𝙤𝙩 𝙔𝙤𝙪𝙧𝙨...😤", true)
		return
	}
	retryMu.Lock()
	entry, ok := retryStore[parts[2]]
	delete(retryStore, parts[2])
	retryMu.Unlock()
	if !ok {
		_ = m.sender.AnswerCallback(ctx, queryID, "Retry is not available anymore!", true)
		return
	}
	_ = m.sender.AnswerCallback(ctx, queryID, "Retrying...", false)
	_ = m.sender.DeleteMessage(ctx, peer, []int{msgID})
	if entry.msg.YtdlQuality != "" {
		link, _ := parseYtdlArgs(entry.msg.Text)
		if link == "" {
			for _, w := range strings.Fields(entry.msg.ReplyText) {
				if ext.IsURL(w) {
					link = w
					break
				}
			}
		}
		if link != "" {
			m.StartYtdl(peer, entry.msg, link, entry.msg.YtdlQuality)
			return
		}
	}
	go m.HandleLeech(context.Background(), peer, entry.msg, entry.msg.IsZip, entry.msg.IsUnzip)
}

func addHistory(u *types.UserSettings, e types.HistoryEntry) {
	u.History = append([]types.HistoryEntry{e}, u.History...)
	if len(u.History) > 10 {
		u.History = u.History[:10]
	}
}

// HandleHistory shows the user's last 10 finished tasks.
func (m *LeechModule) HandleHistory(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) {
	u := m.userMap.Get(msg.UserID)
	if u == nil || len(u.History) == 0 {
		id, _ := m.sender.SendMessage(ctx, peer, "㊂ <b><u>History :</u></b>\n\n<blockquote>◈ <i>No finished tasks yet.</i></blockquote>", msg.MessageID, telegram.CloseMarkup(msg.UserID))
		autoDeletePair(m.sender, peer, id, msg.MessageID)
		return
	}
	var sb strings.Builder
	sb.WriteString("㊂ <b><u>History :</u></b>\n\n")
	for i, h := range u.History {
		name := telegram.EscapeHTML(h.Name)
		if h.Link != "" {
			name = fmt.Sprintf("<a href=\"%s\">%s</a>", h.Link, name)
		}
		when := time.Unix(h.Time, 0).Format("02/01 15:04")
		sb.WriteString(fmt.Sprintf("<blockquote><b>╭</b> <b>%d.</b> %s\n<b>╰</b> %s | %s | %s</blockquote>\n", i+1, name, ext.GetReadableFileSize(h.Size), telegram.EscapeHTML(h.Mode), when))
	}
	_, _ = m.sender.SendMessage(ctx, peer, sb.String(), msg.MessageID, telegram.CloseMarkup(msg.UserID))
}

// captionLinks: the button links as caption text (for albums, which can't have buttons).
func captionLinks(btn *telegram.ButtonMaker) string {
	if btn == nil {
		return ""
	}
	var out []string
	for _, row := range btn.Rows(1) {
		for _, b := range row {
			if b.URL != "" {
				out = append(out, fmt.Sprintf("<a href=\"%s\">%s</a>", b.URL, b.Text))
			}
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "\n\n◈ " + strings.Join(out, " | ")
}

// setStep shows a processing step (screenshots, thumbnail...) in the status, with no speed.
func setStep(t *ext.UnifiedTask, step string) {
	t.SetStatus(types.StatusProcessing, step)
	t.SetProgress(t.DownloadedBytes(), t.Size(), 0, "-")
}

// dcPoolSize is how many connections are opened on the file's DC.
const dcPoolSize = 8

// dcDownloadClients opens a pool of connections on the DC of a file (nil when it can't).
// Every worker of the downloader uses it, the pool spreads the requests over the connections.
func dcDownloadClients(ctx context.Context, client *td.Client, dc int) ([]*tg.Client, func()) {
	if client == nil || dc <= 0 {
		return nil, func() {}
	}
	var inv td.CloseInvoker
	var err error
	if client.Config().ThisDC == dc {
		// the file is on the account's own DC: exporting auth there fails (DC_ID_INVALID)
		inv, err = client.Pool(dcPoolSize)
	} else {
		inv, err = client.DC(ctx, dc, dcPoolSize)
	}
	if err != nil {
		log.Printf("[LEECH] Could not open connections on DC%d: %v", dc, err)
		return nil, func() {}
	}
	api := tg.NewClient(inv)
	clients := make([]*tg.Client, dcPoolSize)
	for i := range clients {
		clients[i] = api
	}
	return clients, func() { _ = inv.Close() }
}

// ytdlResult: the single downloaded file, or the folder when yt-dlp saved several (playlist).
func ytdlResult(taskDir, _ string) string {
	var files []string
	_ = filepath.WalkDir(taskDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n := strings.ToLower(d.Name())
			if strings.HasSuffix(n, ".part") || strings.HasSuffix(n, ".ytdl") || strings.HasSuffix(n, ".json") {
				return nil
			}
			files = append(files, p)
		}
		return nil
	})
	switch len(files) {
	case 0:
		return ""
	case 1:
		return files[0]
	}
	return taskDir
}

// finishFileList: every file name with its link, all in one collapsed (expandable) quote.
func finishFileList(names, links []string) string {
	if len(names) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<blockquote expandable>")
	budget := 2800 // visible chars; the whole message must stay under 4096
	for idx, name := range names {
		store := ""
		if n, sl, ok := strings.Cut(name, "\x00"); ok { // Media Store link
			name, store = n, fmt.Sprintf(" | <a href=\"%s\">Store</a>", sl)
		}
		if r := []rune(name); len(r) > 120 {
			name = string(r[:117]) + "..."
		}
		budget -= len([]rune(name)) + 6
		if budget < 0 {
			sb.WriteString(fmt.Sprintf("<i>...and %d more file(s)</i>\n", len(names)-idx))
			break
		}
		if idx < len(links) && links[idx] != "" {
			sb.WriteString(fmt.Sprintf("%d. <a href=\"%s\">%s</a>%s\n", idx+1, links[idx], telegram.EscapeHTML(name), store))
		} else {
			sb.WriteString(fmt.Sprintf("%d. <code>%s</code>\n", idx+1, telegram.EscapeHTML(name)))
		}
	}
	out := strings.TrimRight(sb.String(), "\n")
	return out + "</blockquote>"
}

var multiFlagRe = regexp.MustCompile(`\s-i\s+\d+`)

// multiFollowText: the command of an -i run without "-i N" and without its link,
// so the next messages keep the same flags (-z, -t, -n ...).
func multiFollowText(text string) string {
	text = multiFlagRe.ReplaceAllString(" "+text, " ")
	var out []string
	for _, w := range strings.Fields(text) {
		if ext.IsURL(w) || ext.IsMagnet(w) {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// leechHelpText: every flag, like the help of our Python bot.
// isTorrentInput: a magnet, a .torrent link or a replied .torrent file.
func isTorrentInput(link string, msg *types.MessageContext) bool {
	l := strings.ToLower(strings.Split(strings.TrimSpace(link), "?")[0])
	if ext.IsMagnet(link) || strings.HasPrefix(l, "magnet:") || strings.HasSuffix(l, ".torrent") {
		return true
	}
	if link == "" && msg != nil {
		if strings.HasSuffix(strings.ToLower(msg.FileName), ".torrent") {
			return true
		}
		if dm, ok := msg.RawMedia.(*tg.MessageMediaDocument); ok {
			if d, ok := dm.Document.(*tg.Document); ok {
				for _, a := range d.Attributes {
					if fn, ok := a.(*tg.DocumentAttributeFilename); ok && strings.HasSuffix(strings.ToLower(fn.FileName), ".torrent") {
						return true
					}
				}
				if d.MimeType == "application/x-bittorrent" {
					return true
				}
			}
		}
	}
	return false
}

// torrentsDisabledMsg: the reason shown when DISABLE_TORRENTS is on (like the limit errors).
const torrentsDisabledMsg = "Torrents are disabled on this bot. Magnet links and .torrent files are not supported right now."

// isLocalTorrent: a .torrent file the bot saved from a sent document.
func isLocalTorrent(p string) bool {
	if !strings.HasSuffix(strings.ToLower(p), ".torrent") || !filepath.IsAbs(p) {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// sendLogStart posts "Leech Started" in LEECH_LOG_ID (user + source link).
func (m *LeechModule) sendLogStart(ctx context.Context, msg *types.MessageContext, source string, mirror bool) (int, tg.InputPeerClass) {
	logID := config.Get().LeechLogID
	if logID == 0 || mirror || isExcepChat(msg.ChatID) {
		return 0, nil
	}
	hash := telegram.ResolveChannelAccessHash(ctx, m.rawAPI, logID)
	if hash == 0 {
		return 0, nil
	}
	logPeer := &tg.InputPeerChannel{ChannelID: pool.RawChannelID(logID), AccessHash: hash}
	mention := fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", msg.UserID, telegram.EscapeHTML(msg.SenderName))
	if msg.Username != "" {
		mention = "@" + msg.Username
	}
	src := "Telegram"
	if config.Get().SafeMode {
		src = "Hidden"
	} else if ext.IsURL(source) || strings.HasPrefix(source, "https://t.me/") {
		src = fmt.Sprintf("<a href=\"%s\">Click Here</a>", telegram.EscapeHTML(source))
	}
	text := fmt.Sprintf("➲ <b><u>Leech Started </u></b>🏁\n┠\n┠ <b>User :</b> %s ( #ID%d )\n<b>╰</b> <b>Source :</b> %s", mention, msg.UserID, src)
	id, err := m.sender.SendMessage(ctx, logPeer, text, 0, nil)
	if err != nil {
		log.Printf("[LEECH] log start message failed: %v", err)
		return 0, nil
	}
	return id, logPeer
}

// logPeerFor resolves a log channel id to a peer (nil when the bot can't reach it).
func (m *LeechModule) logPeerFor(ctx context.Context, id int64) tg.InputPeerClass {
	if id == 0 {
		return nil
	}
	hash := telegram.ResolveChannelAccessHash(ctx, m.rawAPI, id)
	if hash == 0 {
		return nil
	}
	return &tg.InputPeerChannel{ChannelID: pool.RawChannelID(id), AccessHash: hash}
}

func userTagOf(msg *types.MessageContext) string {
	if msg.Username != "" {
		return "@" + msg.Username
	}
	return fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", msg.UserID, telegram.EscapeHTML(msg.SenderName))
}

// sendLinksLog: LINKS_LOG_ID gets every task's source when it starts (Python bot: onDownloadStart).
func (m *LeechModule) sendLinksLog(ctx context.Context, msg *types.MessageContext, mode, source string) {
	cfg := config.Get()
	if cfg.LinksLogID == 0 || msg.Internal || isExcepChat(msg.ChatID) {
		return
	}
	lp := m.logPeerFor(ctx, cfg.LinksLogID)
	if lp == nil {
		return
	}
	loc := time.Local
	if l, err := time.LoadLocation(cfg.Timezone); err == nil {
		loc = l
	}
	src := "<code>Telegram File</code>"
	if source != "" {
		src = "<code>" + telegram.EscapeHTML(source) + "</code>"
	}
	text := fmt.Sprintf("<b><i>Task Started</i></b>\n<blockquote>┠ <b>Mode:</b> %s\n<b>╰</b> <b>By:</b> %s\n\n➲ <b>Source:</b>\n<b>╰</b> <b>Added On:</b> %s</blockquote>\n------------------------------------------\n%s\n------------------------------------------",
		mode, userTagOf(msg), time.Now().In(loc).Format("02/01/06, 03:04:05 PM"), src)
	if _, err := m.sender.SendMessage(ctx, lp, text, 0, nil); err != nil {
		log.Printf("[LINKS_LOG] %v", err)
	}
}

// sendPMStart: a task from a group with BOT_PM, "Task Started" in the user's PM; the files answer it.
func (m *LeechModule) sendPMStart(ctx context.Context, msg *types.MessageContext, source string) int {
	if msg.ChatID == msg.UserID || msg.Internal {
		return 0
	}
	link := source
	if msg.ChatID < 0 {
		link = channelMessageLink(msg.ChatID, msg.MessageID)
	}
	text := "➲ <b><u>Task Started </u></b>🏁"
	if link != "" && !config.Get().SafeMode {
		text += fmt.Sprintf("\n┠\n<b>╰</b> <b>Link:</b> <a href=\"%s\">Click Here</a>", telegram.EscapeHTML(link))
	}
	id, err := m.sender.SendMessage(ctx, &tg.InputPeerUser{UserID: msg.UserID, AccessHash: telegram.GetUserAccessHash(msg.UserID)}, text, 0, nil)
	if err != nil {
		return 0
	}
	return id
}

// isExcepChat: EXCEP_CHATS, chats without leech log.
func isExcepChat(chatID int64) bool {
	for _, f := range strings.Fields(config.Get().ExcepChats) {
		if id, err := strconv.ParseInt(f, 10, 64); err == nil && id == chatID {
			return true
		}
	}
	return false
}

// userTasksOK: USER_TASKS_LIMIT, tasks one user may run at once.
func (m *LeechModule) userTasksOK(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) bool {
	limit := config.Get().UserMaxTasks // USER_MAX_TASKS (USER_TASKS_LIMIT is the old name)
	if limit <= 0 {
		limit = config.Get().UserTasksLimit
	}
	if telegram.IsOwner(msg.UserID) || telegram.IsSudo(msg.UserID, m.userMap) {
		return true
	}
	if telegram.IsPremium(msg.UserID, m.userMap) { // premium: PREMIUM_MAX_TASKS instead (0 = no limit)
		limit = config.Get().PremiumMaxTasks
	}
	if limit <= 0 {
		return true
	}
	n := 0
	for _, t := range m.taskMgr.GetAllTasks() {
		if t.UserID() == msg.UserID {
			n++
		}
	}
	if n < limit {
		return true
	}
	m.replyTemp(ctx, peer, msg, fmt.Sprintf("<blockquote>Your tasks limit exceeded for %d tasks</blockquote>", limit))
	m.deleteCommandLinks(peer, msg)
	return false
}

// botTasksOK: BOT_MAX_TASKS, tasks the whole bot takes (owner and sudo pass).
func (m *LeechModule) botTasksOK(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext) bool {
	limit := config.Get().BotMaxTasks
	if limit <= 0 || telegram.IsOwner(msg.UserID) || telegram.IsSudo(msg.UserID, m.userMap) || m.taskMgr.Count() < limit {
		return true
	}
	m.replyTemp(ctx, peer, msg, fmt.Sprintf("<blockquote>Bot Max Tasks limit exceeded.\nBot max tasks limit is %d.\nPlease wait for the completion of other tasks.</blockquote>", limit))
	m.deleteCommandLinks(peer, msg)
	return false
}

// replyTemp answers the command; with AUTO_DELETE_MESSAGE_DURATION both are deleted after it.
func (m *LeechModule) replyTemp(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, text string) {
	id, _ := m.sender.SendMessage(ctx, peer, text, msg.MessageID, telegram.CloseMarkup(msg.UserID))
	autoDeletePair(m.sender, peer, id, msg.MessageID)
}

func autoDeletePair(sender *telegram.MessageSender, peer tg.InputPeerClass, botMsg, cmdMsg int) {
	sec := config.Get().AutoDeleteMessageDuration
	if sec <= 0 || botMsg <= 0 {
		return
	}
	go func() {
		time.Sleep(time.Duration(sec) * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = sender.DeleteMessage(ctx, peer, []int{botMsg, cmdMsg})
	}()
}

var errSelectionCancelled = fmt.Errorf("selection cancelled")

// downloadGofile: every file of a GoFile link (or the ones chosen with -s) through aria2.
func (m *LeechModule) downloadGofile(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, opt types.LeechOptions, link, taskDir string, uTask *ext.UnifiedTask) (string, error) {
	files, header, err := downloader.GofileList(ctx, link)
	if err != nil {
		return "", err
	}
	if opt.Select && len(files) > 1 {
		uid := fmt.Sprintf("gf%d", time.Now().UnixNano()%1e9)
		var infos []downloader.MegaFileInfo
		for _, f := range files {
			infos = append(infos, downloader.MegaFileInfo{ID: f.ID, Name: f.Name, Size: f.Size, Path: f.Path})
		}
		done := RegisterMegaSelectCache(uid, infos, msg.UserID)
		selMsg, _ := m.sender.SendMessage(ctx, peer, fmt.Sprintf("㊂ <b><u>GoFile Folder :</u></b>\n\n<blockquote>%d files. Choose them on the web page, then press Done Selecting.</blockquote>", len(files)), msg.MessageID, BuildMegaSelectionButtons(uid))
		defer func() {
			if selMsg > 0 {
				_ = m.sender.DeleteMessage(context.Background(), peer, []int{selMsg})
			}
		}()
		select {
		case ok := <-done:
			if !ok {
				ClearMegaSelectCache(uid)
				return "", errSelectionCancelled
			}
		case <-ctx.Done():
			ClearMegaSelectCache(uid)
			return "", ctx.Err()
		}
		chosen := map[string]bool{}
		if c := GetMegaSelectCache(uid); c != nil {
			for _, f := range c.Files {
				if f.Selected {
					chosen[f.ID] = true
				}
			}
		}
		ClearMegaSelectCache(uid)
		var keep []downloader.GofileFile
		for _, f := range files {
			if chosen[f.ID] {
				keep = append(keep, f)
			}
		}
		if len(keep) == 0 {
			return "", fmt.Errorf("no files selected")
		}
		files = keep
	}
	aria := downloader.GetAria2Client()
	var last string
	for i, f := range files {
		dir := filepath.Join(taskDir, f.Path)
		name := strings.NewReplacer("/", "_", "\\", "_").Replace(f.Name)
		uTask.SetName(fmt.Sprintf("[%d/%d] %s", i+1, len(files), name))
		gid, err := aria.AddURI(f.URL, dir, name, []string{header, "User-Agent: Mozilla/5.0"})
		if err != nil {
			return "", err
		}
		task := downloader.NewAria2DownloadTask(gid, name, dir, 0, msg.ChatID, msg.UserID)
		p, err := task.PollUntilComplete(ctx, func(cur, total int64) {
			uTask.SetProgress(cur, total, task.Speed(), task.ETA())
		})
		if err != nil {
			return "", fmt.Errorf("%s: %w", f.Name, err)
		}
		last = p
	}
	if len(files) == 1 {
		return last, nil
	}
	entries, _ := os.ReadDir(taskDir)
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(taskDir, entries[0].Name()), nil
	}
	return taskDir, nil
}

// downloadDebridTorrent: a magnet / .torrent (link or file) downloaded by TorBox, then its files
// from TorBox over HTTPS (aria2). The server never joins the torrent.
var (
	torboxMu      sync.Mutex
	torboxRunning = map[int64]int{} // TorBox torrents running per user
)

// torboxFor: TorBox is set for kind and this user may use it (TORBOX_ACCESS).
func (m *LeechModule) torboxFor(kind string, uid int64) bool {
	sudo := telegram.IsOwner(uid) || telegram.IsSudo(uid, m.userMap)
	return ext.TorBoxFor(kind, uid, sudo, telegram.IsPremium(uid, m.userMap))
}

// debridTorrents: TorBox takes the torrents of this user.
func (m *LeechModule) debridTorrents(uid int64) bool {
	return m.torboxFor("torrents", uid)
}

func (m *LeechModule) debridName(uid int64) string { return "TorBox" }

func (m *LeechModule) downloadDebridTorrent(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, sel bool, link, taskDir string, uTask *ext.UnifiedTask) (string, error) {
	uid := msg.UserID
	src := link
	low := strings.ToLower(strings.Split(link, "?")[0])
	if ext.IsURL(link) && strings.HasSuffix(low, ".torrent") { // a .torrent link: the file is sent to TorBox
		req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
		if err != nil {
			return "", err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		_ = resp.Body.Close()
		if err != nil || len(b) == 0 {
			return "", fmt.Errorf("could not get the .torrent file")
		}
		src = filepath.Join(taskDir, "debrid.torrent")
		if err := os.WriteFile(src, b, 0o644); err != nil {
			return "", err
		}
		defer os.Remove(src)
	}
	// TORBOX_USER_TASKS: TorBox torrents one user may run at once (owner and sudo: no limit)
	if lim := config.Get().TorBoxUserTasks; lim > 0 && !telegram.IsOwner(uid) && !telegram.IsSudo(uid, m.userMap) {
		torboxMu.Lock()
		if torboxRunning[uid] >= lim {
			torboxMu.Unlock()
			return "", fmt.Errorf("You can run %d torrent(s) at once. Wait for your torrent to finish.", lim)
		}
		torboxRunning[uid]++
		torboxMu.Unlock()
		defer func() {
			torboxMu.Lock()
			torboxRunning[uid]--
			torboxMu.Unlock()
		}()
	}
	provider := m.debridName(uid)
	uTask.SetStatus(types.StatusDownloading, provider)
	// while TorBox downloads: its progress, speed, ETA, seeders and peers, like a torrent here
	progress := func(p ext.TBProgress) {
		if p.Name != "" {
			uTask.SetName(p.Name)
		}
		eta := "-"
		if p.ETA > 0 {
			eta = ext.GetReadableTime(p.ETA)
		} else if p.Speed > 0 && p.Total > p.Done {
			eta = ext.GetReadableTime(int64(float64(p.Total-p.Done) / p.Speed))
		}
		uTask.SetProgress(p.Done, p.Total, p.Speed, eta)
		uTask.SetPeers(p.Seeds, p.Peers)
	}
	files, cleanup, name, err := ext.TorBoxTorrent(ctx, src, progress)
	uTask.ClearPeers() // the files now come from TorBox's servers, not from a torrent
	if err == nil {
		defer cleanup() // it leaves the TorBox account when the files are here
	}
	if err != nil {
		return "", err
	}
	// -s: TorBox has the whole torrent; the user picks which files the bot takes from it
	if sel && len(files) > 1 {
		selID := fmt.Sprintf("tb%d", time.Now().UnixNano()%1e9)
		var infos []downloader.MegaFileInfo
		for i, f := range files {
			infos = append(infos, downloader.MegaFileInfo{ID: strconv.Itoa(i), Name: f.Name, Size: f.Size, Path: f.Path})
		}
		done := RegisterMegaSelectCache(selID, infos, uid)
		selMsg, _ := m.sender.SendMessage(ctx, peer, fmt.Sprintf("㊂ <b><u>Select Files :</u></b>\n\n<blockquote>The torrent is ready: %d files. Choose them on the web page, then press <b>Done Selecting</b>.</blockquote>", len(files)), msg.MessageID, BuildMegaSelectionButtons(selID))
		cleanSel := func() {
			if selMsg > 0 {
				_ = m.sender.DeleteMessage(context.Background(), peer, []int{selMsg})
			}
		}
		select {
		case ok := <-done:
			cleanSel()
			if !ok {
				ClearMegaSelectCache(selID)
				return "", errSelectionCancelled
			}
		case <-ctx.Done():
			cleanSel()
			ClearMegaSelectCache(selID)
			return "", ctx.Err()
		}
		chosen := map[string]bool{}
		if c := GetMegaSelectCache(selID); c != nil {
			for _, f := range c.Files {
				if f.Selected {
					chosen[f.ID] = true
				}
			}
		}
		ClearMegaSelectCache(selID)
		var keep []ext.ADFile
		for i, f := range files {
			if chosen[strconv.Itoa(i)] {
				keep = append(keep, f)
			}
		}
		if len(keep) == 0 {
			return "", fmt.Errorf("no files selected")
		}
		files = keep
	}
	root := taskDir
	if len(files) > 1 && name != "" {
		root = filepath.Join(taskDir, strings.NewReplacer("/", "_", "\\", "_").Replace(name))
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	if name != "" {
		uTask.SetName(name)
	}
	uTask.SetStatus(types.StatusDownloading, ext.EngineName("aria2"))
	aria := downloader.GetAria2Client()
	var done int64
	var last string
	for _, f := range files {
		dir := filepath.Join(root, f.Path)
		fname := strings.NewReplacer("/", "_", "\\", "_").Replace(f.Name)
		gid, err := aria.AddURI(f.Link, dir, fname, []string{"User-Agent: Mozilla/5.0"})
		if err != nil {
			return "", err
		}
		task := downloader.NewAria2DownloadTask(gid, fname, dir, 0, 0, 0)
		p, err := task.PollUntilComplete(ctx, func(cur, _ int64) {
			uTask.SetProgress(done+cur, total, task.Speed(), task.ETA())
		})
		if err != nil {
			return "", fmt.Errorf("%s: %w", f.Name, err)
		}
		done += f.Size
		last = p
	}
	if len(files) == 1 {
		return last, nil
	}
	return root, nil
}

// sameDirFailed: a task of a -m run failed; when it was the last one, the folder is uploaded
// by a new task.
func (m *LeechModule) sameDirFailed(peer tg.InputPeerClass, msg *types.MessageContext) {
	folder, err := sameDirAdd(msg.SameDirKey, "")
	if err != nil || folder == "" {
		return
	}
	if entries, _ := os.ReadDir(folder); len(entries) == 0 {
		_ = os.RemoveAll(filepath.Dir(folder))
		return
	}
	cfg := config.Get()
	taskDir := filepath.Join(cfg.DownloadDir, fmt.Sprintf("%d_%s", msg.UserID, ext.RandomHex(4)))
	_ = os.MkdirAll(taskDir, 0777)
	dest := filepath.Join(taskDir, filepath.Base(folder))
	if os.Rename(folder, dest) != nil {
		return
	}
	_ = os.RemoveAll(filepath.Dir(folder))
	up := *msg
	up.Internal = true
	up.SameDirKey = ""
	notifyID, _ := m.sender.SendMessage(context.Background(), peer, "<i>Uploading the folder...</i>", msg.MessageID, nil)
	opt, _ := parseLeechArgs(msg.Text)
	go m.processLeechPipeline(peer, &up, opt, dest, taskDir, notifyID)
}

// noRetry: Retry makes no sense when the user stopped the task himself or the bot refuses it.
func noRetry(reason string) bool {
	r := strings.ToLower(reason)
	for _, k := range []string{"cancel", "stopped", "disabled", "limit", "not supported", "closed"} {
		if strings.Contains(r, k) {
			return true
		}
	}
	return false
}

// withStoreLinks marks names whose file has a Store link (finishFileList adds it).
func withStoreLinks(names, links []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n
		if i < len(links) {
			if chat, msgID, ok := parseChannelLink(links[i]); ok {
				if sl := storeLink(chat, msgID); sl != "" {
					out[i] = n + "\x00" + sl
				}
			}
		}
	}
	return out
}

// parseChannelLink reads https://t.me/c/<id>/<msg>.
func parseChannelLink(link string) (int64, int, bool) {
	rest := strings.TrimPrefix(link, "https://t.me/c/")
	if rest == link {
		return 0, 0, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return 0, 0, false
	}
	id, err1 := strconv.ParseInt(parts[0], 10, 64)
	msgID, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return -1000000000000 - id, msgID, true
}

// resultLink: the file in the leech log (the channel people are in), else in the dump channel.
func resultLink(r uploader.UploadResult) string {
	if r.LogMsgID > 0 {
		return channelMessageLink(r.LogChatID, r.LogMsgID)
	}
	return channelMessageLink(r.ChatID, r.MsgID)
}

// isHLSLink: an m3u8 playlist (HLS stream).
func isHLSLink(link string) bool {
	if !ext.IsURL(link) {
		return false
	}
	l := strings.ToLower(link)
	path := strings.SplitN(strings.SplitN(l, "?", 2)[0], "#", 2)[0]
	return strings.HasSuffix(path, ".m3u8") || strings.Contains(path, ".m3u8/") ||
		strings.Contains(l, "format=m3u8") || strings.Contains(l, "type=m3u8")
}

// useYtdlForHLS: the task goes through yt-dlp (best quality, one mp4), keeping -n and -h.
func useYtdlForHLS(opt *types.LeechOptions) {
	opt.Ytdl = true
	if opt.YtdlFormat == "" {
		opt.YtdlFormat = "bv*+ba/b"
	}
	opt.YtdlArgs = append(opt.YtdlArgs, "--merge-output-format", "mp4")
	for _, h := range taskHeaders(opt.Headers, opt.Username, opt.Password) {
		opt.YtdlArgs = append(opt.YtdlArgs, "--add-header", h)
	}
}

// pathSize: a file's size, or everything inside a folder.
func pathSize(p string) int64 {
	var n int64
	_ = filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}

// trackStep shows a processing step (extract, zip, split, video tools, FFmpeg) like a transfer:
// Processed, Speed and ETA come from the files the step writes in dir. total is the expected
// output (the input size). Call the returned func when the step ends.
func trackStep(t *ext.UnifiedTask, dir string, total int64) func() {
	start := time.Now()
	stop := make(chan struct{})
	t.SetProgress(0, total, 0, "-")
	// the files already there: only new or changed ones are the step's output
	type st struct {
		size int64
		mod  time.Time
	}
	before := map[string]st{}
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			before[p] = st{info.Size(), info.ModTime()}
		}
		return nil
	})
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			var cur int64
			_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					if old, ok := before[p]; !ok || old.size != info.Size() || !old.mod.Equal(info.ModTime()) {
						cur += info.Size() // written since the step began
					}
				}
				return nil
			})
			if total > 0 && cur > total {
				cur = total
			}
			el := time.Since(start).Seconds()
			spd := float64(cur) / el
			eta := "-"
			if spd > 0 && total > cur {
				eta = ext.GetReadableTime(int64(float64(total-cur) / spd))
			}
			t.SetProgress(cur, total, spd, eta)
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}

// pythonMode: the Mode line of the Python bot: " #Leech (Zip) | #Aria2".
func pythonMode(modeStr string, opt types.LeechOptions) string {
	kind := "#Leech"
	if opt.IsMirror {
		kind = "#DDL"
		if opt.MirrorHost == ext.GDriveHostID {
			kind = "#tjgd"
		} else if opt.MirrorHost == ext.RcloneHostID {
			kind = "#tjrc"
		}
	}
	if opt.IsZip {
		kind += " (Zip)"
	} else if opt.IsExtract {
		kind += " (Unzip)"
	}
	eng := "#Aria2"
	switch {
	case strings.Contains(modeStr, "YtDlp"):
		eng = "#ytdlp"
	case strings.Contains(modeStr, "Telegram"), strings.Contains(modeStr, "Folder"):
		eng = "#Tg"
	case strings.Contains(modeStr, "Mega"):
		eng = "#Mega"
	case strings.Contains(modeStr, "GDrive"):
		eng = "#GDrive"
	case strings.Contains(modeStr, "TorBox"):
		eng = "#TorBox"
	}
	return kind + " | " + eng
}

// mimeOfName: the Type line of a mirrored file.
func mimeOfName(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return strings.SplitN(t, ";", 2)[0]
	}
	return "application/octet-stream"
}

// mirrorRename: the file gets the user's Mirror Remname (old:new|word), Prefix and Suffix
// (before the extension), like the Python bot; the new path, or the old one when it fails.
func mirrorRename(path string, u *types.UserSettings) string {
	dir, base := filepath.Dir(path), filepath.Base(path)
	ext0 := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext0)
	for _, item := range strings.Split(u.MRemname, "|") {
		if item == "" {
			continue
		}
		if old, nw, ok := strings.Cut(item, ":"); ok {
			stem = strings.ReplaceAll(stem, old, nw)
		} else {
			stem = strings.ReplaceAll(stem, item, "")
		}
	}
	name := strings.TrimSpace(u.MPrefix + stem + u.MSuffix + ext0)
	if name == "" || name == base {
		return path
	}
	np := filepath.Join(dir, name)
	if err := os.Rename(path, np); err != nil {
		return path
	}
	return np
}

// userGDClients: the users' own token.pickle clients (the access token is kept between tasks).
var (
	userGDMu      sync.Mutex
	userGDClients = map[int64]struct {
		data   string
		client *downloader.GDriveClient
	}{}
)

// gdriveTarget: token, folder and index of a tjgd upload (like WZML-X):
// mtp:<id> = his token.pickle to that folder; tp:<id> = the bot's token to that folder;
// USER mode (/us) = his token.pickle to his GDrive ID; else the bot's token to his GDrive ID
// or GDRIVE_ID. The index: his Index URL with his folder, INDEX_URL with the bot's.
// mirrorDestError: why GDrive / RClone cannot take his upload (WZML-X messages), "" = fine.
func mirrorDestError(cfg *config.Config, u *types.UserSettings, host, dest string) string {
	fileOK := func(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }
	switch host {
	case ext.GDriveHostID:
		low := strings.ToLower(dest)
		useUser := strings.HasPrefix(low, "mtp:") || (!strings.HasPrefix(low, "tp:") && u != nil && u.UserTokens)
		if useUser {
			if u == nil || len(u.TokenPickle) == 0 {
				return "<b>NO TOKEN!</b> token.pickle not Exists!\n➲ <i>Send yours in /us → Mirror Settings → GDrive</i>"
			}
		} else if !fileOK("token.pickle") {
			return "<b>NO TOKEN!</b> token.pickle not Exists!\n➲ <i>GDrive is not set up: set your own token.pickle and GDrive ID in /us → Mirror Settings → GDrive (USER mode)</i>"
		}
		if _, f, ok := strings.Cut(dest, ":"); ok && f != "" {
			return ""
		}
		if (u != nil && u.GDriveID != "") || (!useUser && cfg.GDriveID != "") {
			return ""
		}
		return "<b>No Upload Destination Found!</b>\n➲ <i>Set your GDrive ID in /us → Mirror Settings → GDrive</i>"
	case ext.RcloneHostID:
		own := strings.HasPrefix(dest, "mrcc:") || (u != nil && u.UserTokens && len(u.RcloneConf) > 0)
		if strings.TrimPrefix(dest, "mrcc:") == "" && !(own && u != nil && strings.TrimPrefix(u.RclonePath, "mrcc:") != "") {
			return "<b>No Rclone Destination!</b>\n➲ <i>Set your RClone Path in /us → Mirror Settings → RClone</i>"
		}
		if own {
			if u == nil || len(u.RcloneConf) == 0 {
				return "<b>Rclone Config:</b> rclone.conf not Exists!\n➲ <i>Send yours in /us → Mirror Settings → RClone</i>"
			}
		} else if !fileOK("rclone.conf") {
			return "<b>Rclone Config:</b> rclone.conf not Exists!\n➲ <i>RClone is not set up: send your own rclone.conf in /us → Mirror Settings → RClone (USER mode)</i>"
		}
	}
	return ""
}

func gdriveTarget(cfg *config.Config, u *types.UserSettings, dest string) (downloader.GDUploadOptions, error) {
	o := downloader.GDUploadOptions{IsTeamDrive: cfg.IsTeamDrive, StopDuplicate: u.StopDupOn(cfg.StopDuplicate)}
	useUser, folder := false, ""
	switch low := strings.ToLower(dest); {
	case strings.HasPrefix(low, "mtp:"):
		useUser, folder = true, dest[4:]
	case strings.HasPrefix(low, "tp:"):
		folder = dest[3:]
	default:
		useUser = u != nil && u.UserTokens
	}
	if useUser {
		if u == nil || len(u.TokenPickle) == 0 {
			return o, fmt.Errorf("your token.pickle is not set: /us → Mirror Settings → GDrive Tools")
		}
		userGDMu.Lock()
		c, ok := userGDClients[u.UserID]
		if !ok || c.data != string(u.TokenPickle) {
			cl, err := downloader.NewGDriveClientFromBytes(u.TokenPickle)
			if err != nil {
				userGDMu.Unlock()
				return o, fmt.Errorf("your token.pickle: %v", err)
			}
			c.data, c.client = string(u.TokenPickle), cl
			userGDClients[u.UserID] = c
		}
		userGDMu.Unlock()
		o.Client = c.client
		o.IsTeamDrive = false
	} else {
		o.Private = cfg.DisableDriveLink // the bot's account: no public files when its link is hidden
	}
	switch {
	case folder != "":
		o.ParentID = folder
		if u != nil && folder == u.GDriveID {
			o.IndexURL = u.IndexURL
		} else if folder == cfg.GDriveID {
			o.IndexURL = cfg.IndexURL
		}
	case u != nil && u.GDriveID != "":
		o.ParentID, o.IndexURL = u.GDriveID, u.IndexURL
	case !useUser:
		o.ParentID, o.IndexURL = cfg.GDriveID, cfg.IndexURL
	}
	if o.ParentID == "" {
		return o, fmt.Errorf("no GDrive ID: set yours in /us → Mirror Settings → GDrive Tools")
	}
	return o, nil
}
