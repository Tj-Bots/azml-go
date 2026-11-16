package modules

import (
	"context"
	"errors"
	"fmt"
	td "github.com/gotd/td/telegram"
	"log"
	"maps"
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
		return
	}
	if !m.forceSubOK(ctx, peer, msg) {
		return
	}

	opt, link := parseLeechArgs(msg.Text)
	opt.IsMirror = msg.IsMirror
	opt.MirrorHost = msg.MirrorHost
	if isZip {
		opt.IsZip = true
	}
	if isUnzip {
		opt.IsExtract = true
	}
	if opt.Password != "" && !opt.IsZip && opt.Username == "" {
		opt.IsExtract = true
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
					HasMedia:   targetMsg.Media != nil,
					RawMedia:   targetMsg.Media,
					ReplyToID:  targetID,
					IsReply:    true,
					IsMirror:   msg.IsMirror,
					MirrorHost: msg.MirrorHost,
				}

				log.Printf("[LEECH -i] Sequentially triggering download for message ID %d (part %d/%d)", targetID, idx+1, multiCount)
				m.HandleLeech(context.Background(), peer, nextMsgCtx, isZip, isUnzip)
			}
		}()
	}

	if link == "" && !msg.HasMedia && !msg.IsReply && opt.IsMirror {
		var hosts []string
		var userKeys map[string]string
		if u := m.userMap.Get(msg.UserID); u != nil {
			userKeys = u.DDLKeys
		}
		for _, h := range ext.AvailableMirrorHostsFor(userKeys) {
			hosts = append(hosts, h.Name)
		}
		helpMsg := fmt.Sprintf("㊂ <b><u>Mirror Command Usage :</u></b>\n\n"+
			"<b>Usage:</b> <code>/mirror"+config.Get().CmdSuffix+" [link]</code> or reply to a link / file\n\n"+
			"<blockquote>╭ <b>Upload To :</b> %s\n"+
			"┠ <code>-up gofile</code> — Choose the host directly\n"+
			"┠ <code>-n name</code> — Custom File Name\n"+
			"┠ <code>-z</code> — Compress as Zip\n"+
			"┠ <code>-e</code> — Extract Archive\n"+
			"╰ <code>-p pass</code> — Extraction Password</blockquote>\n\n"+
			"➲ <b>Default host :</b> /us → Mirror Settings",
			strings.Join(hosts, ", "))
		_, _ = m.sender.SendMessage(ctx, peer, helpMsg, msg.MessageID, telegram.CloseMarkup(msg.UserID))
		return
	}
	if link == "" && !msg.HasMedia && !msg.IsReply {
		helpMsg := leechHelpText()
		_, _ = m.sender.SendMessage(ctx, peer, helpMsg, msg.MessageID, telegram.CloseMarkup(msg.UserID))
		return
	}

	if link != "" && !ext.IsURL(link) && !ext.IsMagnet(link) && !ext.IsMegaLink(link) && !strings.HasPrefix(link, "tg://") && !isLocalTorrent(link) {
		m.sendDownloadError(ctx, peer, msg, 0, "Invalid link or resource not found! Please check the URL.", "#Leech", time.Now())
		return
	}

	if !msg.Internal && !m.timeIntervalOK(ctx, peer, msg) {
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

func (m *LeechModule) sendDownloadError(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, notifyID int, errStr string, mode string, startTime time.Time) {
	tag := fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", msg.UserID, telegram.EscapeHTML(msg.SenderName))
	if msg.Username != "" {
		tag = "@" + msg.Username
	}

	elapsed := ext.TimeSinceFormatted(startTime)
	errMsg := fmt.Sprintf("<i><b>Download Stopped!</b></i>\n"+
		"<blockquote>╭ <b>Task for:</b> %s\n"+
		"┠ <b>Due To:</b> %s\n"+
		"┠ <b>Mode:</b> %s\n"+
		"╰ <b>Elapsed:</b> %s</blockquote>",
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
	_, _ = m.sender.SendMessage(ctx, peer, errMsg, msg.MessageID, retryMarkup(msg))

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

	if cfg.DisableTorrents && isTorrentInput(link, msg) {
		m.sendDownloadError(ctx, peer, msg, notifyID, torrentsDisabledMsg, "#Leech | #Torrent", time.Now())
		return
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

	// Try Direct Link Generator for host URLs (Mediafire, Pixeldrain, Solidfiles)
	if ext.IsURL(link) && !opt.Ytdl && !ext.IsMegaLink(link) {
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
	uTask.SetStatusDisplay(msg.Username, msg.SenderName, "#leech")
	m.taskMgr.AddTask(uTask)

	// Instantly render the status progress bar so the user gets an immediate (<1s) response
	if notifyID > 0 && m.statusMod != nil {
		if !m.statusMod.HasStatusMessage(msg.ChatID) {
			m.statusMod.SetStatusMessage(msg.ChatID, notifyID, peer)
			text, btn := m.taskMgr.BuildStatusMessage(1)
			_ = m.sender.EditMessageWithButtons(ctx, peer, notifyID, text, btn, 3)
		} else {
			go m.statusMod.UpdateAllMessages(ctx, true)
		}
	}

	// For HTTP/HTTPS links: probe remote headers to resolve true filename (Content-Disposition) & extension in background
	if ext.IsURL(link) && !opt.Ytdl && !ext.IsMegaLink(link) && !ext.IsMagnet(link) && !strings.Contains(strings.ToLower(link), ".torrent") {
		resolvedName, finalURL, resolvedSize, _ := ext.ResolveDownloadMetadata(ctx, link)
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

	// 0. Queue Check for Download (Sleep v0 Engine)
	queuedDL, qErr := ext.AcquireDownloadQueue(ctx, taskUID, displayName, msg.FileSize, msg.ChatID, msg.UserID, notifyID, cancel)
	if qErr != nil {
		m.sendDownloadError(ctx, peer, msg, notifyID, "Download task cancelled while in queue", modeStr, taskStartTime)
		return
	}
	if queuedDL {
		_ = m.sender.EditMessage(ctx, peer, notifyID, "<i>Starting download from queue...</i>", nil)
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
							break
						}
					}
				}
			} else if err != nil {
				log.Printf("[LEECH] Failed to fetch replied message %d: %v", msg.ReplyToID, err)
				dlErr = fmt.Errorf("failed to fetch replied message: %v", err)
			}
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

					effectiveDumpChatID := m.dumpChatID
					if effectiveDumpChatID == 0 && cfg != nil {
						effectiveDumpChatID = cfg.DumpChatID
					}
					if effectiveDumpChatID == 0 && cfg != nil {
						effectiveDumpChatID = cfg.LeechLogID
					}

					// Check if Multi-Bot Download is enabled and sessions are active
					if m.sessPool != nil && m.sessPool.ActiveSessionCount() > 0 && effectiveDumpChatID != 0 {
						targetMsgID := msg.ReplyToID
						if targetMsgID == 0 {
							targetMsgID = msg.MessageID
						}

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
							fwdMsgID, fwdErr := telegram.ForwardToChannelWithFloodWait(ctx, m.rawAPI, peer, dumpPeer, targetMsgID)

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
								if msg.IsReply && msg.ReplyToID > 0 {
									rMsg, err := telegram.FetchMessage(ctx, m.rawAPI, peer, msg.ReplyToID)
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
			if downloadedPath == "" {
				dlErr = fmt.Errorf("yt-dlp finished but no file was downloaded")
			} else {
				uTask.SetName(filepath.Base(downloadedPath))
			}
		}
	} else if downloader.IsGDriveLink(link) {
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
	} else if ext.IsMegaLink(link) {
		uTask.SetStatus(types.StatusDownloading, ext.EngineName("mega"))
		var only map[string]bool
		if opt.Select {
			// real file list of the folder, chosen on the web page (BASE_URL)
			nodes, lErr := downloader.MegaList(ctx, link)
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
		downloadedPath, dlErr = downloader.MegaDownloadLink(ctx, link, taskDir, only, func(cur, total int64, speed float64, eta string) {
			if speed > maxDLSpeed {
				maxDLSpeed = speed
			}
			uTask.SetProgress(cur, total, speed, eta)
		})
	} else if ext.IsMagnet(link) || strings.Contains(strings.ToLower(link), ".torrent") || ext.IsURL(link) || (func() bool { fi, err := os.Stat(link); return err == nil && !fi.IsDir() }()) {
		cleanLink := strings.Split(strings.TrimSpace(link), "?")[0]
		isLocalTorrent := false
		if fi, err := os.Stat(link); err == nil && !fi.IsDir() && strings.HasSuffix(strings.ToLower(link), ".torrent") {
			isLocalTorrent = true
		}
		isRemoteTorrent := ext.IsURL(link) && strings.HasSuffix(strings.ToLower(cleanLink), ".torrent")
		if cfg.DisableTorrents && (isLocalTorrent || isRemoteTorrent || ext.IsMagnet(link)) {
			m.sendDownloadError(ctx, peer, msg, notifyID, torrentsDisabledMsg, modeStr, taskStartTime)
			return
		}

		aria := downloader.GetAria2Client()

		if isLocalTorrent {
			log.Printf("[LEECH] Adding local .torrent file to Aria2c: %s", link)
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
					log.Printf("[LEECH] Aria2c AddURI error: %v, falling back to DirectDownloader for %s", err, link)
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

	if dlErr != nil {
		ext.ReleaseDownload(taskUID)
		if errors.Is(dlErr, context.Canceled) || strings.Contains(strings.ToLower(dlErr.Error()), "cancel") {
			m.taskMgr.RemoveTask(taskUID)
			if m.statusMod != nil {
				if m.taskMgr.Count() == 0 {
					m.statusMod.DeleteAllMessages(ctx)
				} else {
					go m.statusMod.UpdateAllMessages(ctx, true)
				}
			}
			return
		}
		m.sendDownloadError(ctx, peer, msg, notifyID, dlErr.Error(), modeStr, taskStartTime)
		return
	}
	ext.ReleaseDownload(taskUID)

	// Critical safeguard: If downloadedPath is a .torrent file, automatically extract and download its real payload!
	if strings.HasSuffix(strings.ToLower(downloadedPath), ".torrent") && cfg.DisableTorrents {
		m.sendDownloadError(ctx, peer, msg, notifyID, torrentsDisabledMsg, modeStr, taskStartTime)
		return
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

	if opt.Join {
		setStep(uTask, "Join Parts")
		downloadedPath = joinParts(downloadedPath)
	}

	// 2. Post-processing: Forceful Archive Extraction (/leech -e, -extract, -uz, -p)
	// Reads binary magic bytes (PK, 7z, Rar!, etc.) even if the file extension was disguised or missed
	if opt.IsExtract {
		uTask.SetStatus(types.StatusExtracting, ext.GetExtractEngineName())
		isArch, archType := ext.IsArchive(downloadedPath)
		if isArch {
			log.Printf("[LEECH -e] Detected archive format '%s' via binary header for: %s", archType, downloadedPath)
			extractDir := filepath.Join(taskDir, "extracted")
			if outDir, err := ext.ForceExtractArchive(downloadedPath, extractDir, opt.Password); err == nil {
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
		if out, err := extractAllStreams(downloadedPath, filepath.Join(taskDir, "streams_"+ext.RandomHex(3))); err == nil {
			_ = os.RemoveAll(downloadedPath)
			downloadedPath = out
		} else {
			log.Printf("[LEECH -st] %v", err)
		}
	}
	if opt.ExtractAud != "" {
		setStep(uTask, "Extract Audio (FFmpeg)")
		downloadedPath = extractAudio(downloadedPath, opt.ExtractAud)
	}
	if opt.ConvVideo != "" || opt.ConvAudio != "" {
		setStep(uTask, "Convert Media (FFmpeg)")
		downloadedPath = convertMedia(downloadedPath, opt.ConvVideo, opt.ConvAudio)
	}

	if opt.IsZip && !opt.IsExtract {
		uTask.SetStatus(types.StatusArchiving, "7-Zip")
		// Compression requested (-z / -zip)
		zipTarget := filepath.Join(taskDir, filepath.Base(downloadedPath)+".zip")
		if err := ext.CompressToZip(downloadedPath, zipTarget, opt.Password); err == nil {
			_ = os.RemoveAll(downloadedPath)
			downloadedPath = zipTarget
		}
	}

	// Custom FFmpeg commands (-ff)
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
		if fErr := ext.ExecuteCustomFFmpeg(downloadedPath, outFFmpeg, rawCmd, cfg.FFmpegEncoding); fErr == nil {
			_ = os.Remove(downloadedPath)
			downloadedPath = outFFmpeg
		} else {
			log.Printf("[FFMPEG] Custom ffmpeg error: %v", fErr)
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

	setStep(uTask, "Preparing Upload")
	taskThumb := ""
	if opt.ThumbURL != "" {
		if taskThumb = m.fetchTaskThumb(ctx, peer, opt.ThumbURL); taskThumb != "" {
			defer os.Remove(taskThumb)
		} else {
			log.Printf("[LEECH -t] could not get the thumbnail from %s", opt.ThumbURL)
		}
	}
	if notifyID > 0 {
		text, btn := m.taskMgr.BuildStatusMessage(1)
		_ = m.sender.EditMessageWithButtons(ctx, peer, notifyID, text, btn, 3)
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

	for _, curFile := range filesToUpload {
		if user != nil && isExcludedFile(curFile, user.ExcludedExts) {
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
		if user != nil && caption != "" {
			caption = applyCaptionFont(caption, user.CapFont)
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
		autoThumbEnabled := cfg.AutoThumbnail
		if user != nil && user.AutoThumb {
			autoThumbEnabled = true
		}

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
			splitParts, splitErr = ext.SplitFile(curFile, splitSize, splitDir, equalSplits)
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
				fileButtons.UButtonStyled("MediaInfo", miLink, "primary")
			}
		}
		if len(ssLinks) > 0 {
			if fileButtons == nil {
				fileButtons = telegram.NewButtonMaker()
			}
			fileButtons.UButtonStyled("ScreenShots", ssLinks[0], "primary")
		}
		if fileButtons != nil {
			fileButtons.Row()
		}
		if cfg.SaveMsg {
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
				case useGroup && len(partResults) > 0 && partResults[0].MsgID > 0:
					link = channelMessageLink(partResults[0].ChatID, partResults[0].MsgID+idx) // album = consecutive ids
				case idx < len(partResults):
					link = channelMessageLink(partResults[idx].ChatID, partResults[idx].MsgID)
				}
				fileLinks = append(fileLinks, link)
			}
		} else if len(partResults) > 0 {
			fileLinks = append(fileLinks, channelMessageLink(partResults[0].ChatID, partResults[0].MsgID))
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

	// Build completion message exactly matching Image 1
	var sb strings.Builder
	mainName := displayName
	if len(uploadedNames) > 0 {
		mainName = uploadedNames[0]
	}
	sb.WriteString(fmt.Sprintf("<b>%s</b>\n", mainName))
	sb.WriteString(fmt.Sprintf("<blockquote>╭ <b>Size:</b> %s\n", ext.GetReadableFileSize(totalSize)))
	sb.WriteString(fmt.Sprintf("┠ <b>Elapsed:</b> %s\n", ext.GetReadableTime(int64(overallElapsed.Seconds()))))
	sb.WriteString(fmt.Sprintf("┠ <b>Avg DL Speed:</b> %s/s\n", ext.GetReadableFileSize(int64(avgDLSpeed))))
	sb.WriteString(fmt.Sprintf("┠ <b>Avg UL Speed:</b> %s/s\n", ext.GetReadableFileSize(int64(avgULSpeed))))
	sb.WriteString(fmt.Sprintf("┠ <b>Mode:</b> %s\n", modeStr))
	sb.WriteString(fmt.Sprintf("┠ <b>Max DL Speed:</b> %s/s\n", ext.GetReadableFileSize(int64(maxDLSpeed))))
	sb.WriteString(fmt.Sprintf("┠ <b>Max UL Speed:</b> %s/s\n", ext.GetReadableFileSize(int64(maxULSpeed))))
	sb.WriteString(fmt.Sprintf("┠ <b>Total Files:</b> %d\n", len(uploadedNames)))

	userTag := fmt.Sprintf("@%s", msg.Username)
	if msg.Username == "" {
		userTag = fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", msg.UserID, msg.SenderName)
	}
	sb.WriteString(fmt.Sprintf("╰ <b>By:</b> %s</blockquote>\n\n", userTag))

	botPM := cfg.BotPM || (user != nil && user.BotPM) || (msg.ChatID != msg.UserID)
	if opt.IsMirror {
		sb.WriteString(fmt.Sprintf("➲ <b><i>Uploaded to %s :</i></b>\n", mirrorHostName(opt.MirrorHost)))
		sb.WriteString(finishFileList(uploadedNames, mirrorLinks))
	} else {
		if botPM {
			sb.WriteString("➲ <b><i>File(s) have been Sent to Bot PM (Private)</i></b>\n")
		} else {
			sb.WriteString("➲ <b><i>File(s) have been Sent. Access via Links...</i></b>\n")
		}
		sb.WriteString(finishFileList(uploadedNames, fileLinks))
	}

	botUser := telegram.CurrentBotUsername
	if botUser == "" {
		botUser = ""
	}
	finishBtn := telegram.NewButtonMaker()
	if opt.IsMirror {
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
			label := "◈ " + mirrorHostName(opt.MirrorHost)
			if len(uniq) > 1 {
				label = fmt.Sprintf("%s %d", label, idx+1)
			}
			finishBtn.UButtonStyled(label, link, "primary")
			finishBtn.Row()
		}
	} else {
		if botUser != "" {
			finishBtn.UButtonStyled("View in Bot PM", fmt.Sprintf("https://t.me/%s", botUser), "primary")
		}
	}

	if cfg.SourceLink && (ext.IsURL(sourceURL) || strings.HasPrefix(sourceURL, "https://t.me/")) && !ext.IsMagnet(sourceURL) {
		finishBtn.UButtonStyled("Source Link", sourceURL, "primary")
		finishBtn.Row()
	}
	if cfg.SaveMsg {
		saveMsgButton(finishBtn)
	}
	// Send completion message into the group / chat where leech was initiated, replying to user's command
	_, _ = m.sender.SendMessageWithButtons(ctx, peer, sb.String(), msg.MessageID, finishBtn, 1)

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
		"-d", "-doc", "-asdoc", "-m", "-med", "-media", "-asmedia",
		"-st", "-streams", "-n", "-name", "-p", "-password",
		"-pssw", "-ss", "-screenshots", "-ud", "-dump", "-ff", "-i", "-s", "-select",
		"-t", "-thumb", "-sp", "-h", "-headers", "-u", "-user", "-ea", "-cv", "-ca", "-j", "-join", "-pass":
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
		case "-m", "-med", "-media", "-asmedia":
			opt.AsMedia = true
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
	btn.IButtonStyled("↻ Retry", fmt.Sprintf("retry %d %s", msg.UserID, key), "primary")
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
		_, _ = m.sender.SendMessage(ctx, peer, "㊂ <b><u>History :</u></b>\n\n<blockquote>◈ <i>No finished tasks yet.</i></blockquote>", msg.MessageID, telegram.CloseMarkup(msg.UserID))
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
		sb.WriteString(fmt.Sprintf("<blockquote>╭ <b>%d.</b> %s\n╰ %s | %s | %s</blockquote>\n", i+1, name, ext.GetReadableFileSize(h.Size), telegram.EscapeHTML(h.Mode), when))
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
		if r := []rune(name); len(r) > 120 {
			name = string(r[:117]) + "..."
		}
		budget -= len([]rune(name)) + 6
		if budget < 0 {
			sb.WriteString(fmt.Sprintf("<i>...and %d more file(s)</i>\n", len(names)-idx))
			break
		}
		if idx < len(links) && links[idx] != "" {
			sb.WriteString(fmt.Sprintf("%d. <a href=\"%s\">%s</a>\n", idx+1, links[idx], telegram.EscapeHTML(name)))
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
func leechHelpText() string {
	return "㊂ <b><u>Leech Command Usage :</u></b>\n\n" +
		"<b>Usage:</b> <code>/leech" + config.Get().CmdSuffix + " [link] [flags]</code> or reply to a link / file\n\n" +
		"<blockquote expandable>╭ <b>Name & Archive :</b>\n" +
		"┠ <code>-n name</code> — New file name\n" +
		"┠ <code>-z</code> — Zip  ·  <code>-e</code> — Extract\n" +
		"┠ <code>-p pass</code> — Archive password\n" +
		"┠ <code>-j</code> — Join split parts (.001 .002 ...)\n" +
		"┠ <b>Many Tasks :</b>\n" +
		"┠ <code>-i 5</code> — Reply to the first: this message and the next 4\n" +
		"┠ <code>-b</code> — Bulk: reply to a list of links (<code>-b 3:8</code> range)\n" +
		"┠ <b>Upload :</b>\n" +
		"┠ <code>-t link</code> — Thumbnail for this task (image link or t.me message link)\n" +
		"┠ <code>-sp 1.5GB</code> — Split size for this task\n" +
		"┠ <code>-d</code> / <code>-med</code> — As document / as media\n" +
		"┠ <code>-ud chat_id</code> — Dump chat for this task\n" +
		"┠ <code>-ss 6</code> — Screenshots album\n" +
		"┠ <b>Media Tools :</b>\n" +
		"┠ <code>-st</code> — Every video, audio and subtitle track as its own file\n" +
		"┠ <code>-ea</code> — Only the audio (<code>-ea m4a</code> flac opus ogg wav)\n" +
		"┠ <code>-cv mp4</code> — Videos to mp4 (no re-encode)  ·  <code>-ca mp3</code> — Audios to mp3\n" +
		"┠ <b>Direct Links :</b>\n" +
		"┠ <code>-u user -p pass</code> — Login\n" +
		"╰ <code>-h Referer: https://site.com</code> — Headers</blockquote>"
}

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
	if logID == 0 || mirror {
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
	if ext.IsURL(source) || strings.HasPrefix(source, "https://t.me/") {
		src = fmt.Sprintf("<a href=\"%s\">Click Here</a>", telegram.EscapeHTML(source))
	}
	text := fmt.Sprintf("➲ <b><u>Leech Started :</u></b>\n\n<blockquote>╭ <b>User :</b> %s ( #ID%d )\n╰ <b>Source :</b> %s</blockquote>", mention, msg.UserID, src)
	id, err := m.sender.SendMessage(ctx, logPeer, text, 0, nil)
	if err != nil {
		log.Printf("[LEECH] log start message failed: %v", err)
		return 0, nil
	}
	return id, logPeer
}
