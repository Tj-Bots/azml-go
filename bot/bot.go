package bot

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
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

	td "github.com/gotd/td/telegram"
	tdDownloader "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

var (
	accessHashMu        sync.RWMutex
	userAccessHashes    = make(map[int64]int64)
	channelAccessHashes = make(map[int64]int64)
)

type BotApp struct {
	client      *td.Client
	rawAPI      *tg.Client
	userClient  *td.Client
	userRawAPI  *tg.Client
	userPool    *telegram.UserClientPool
	userCancel  context.CancelFunc
	userMu      sync.RWMutex
	userUpdater func(userRawAPI *tg.Client)
	dispatcher  tg.UpdateDispatcher
	gaps        *updates.Manager
	sender      *telegram.MessageSender
	uploader    *uploader.TelegramUploader
	userMap     *types.SafeUserMap
	sessPool    *pool.SessionPool
	startTime   time.Time
	botUsername string

	// Modules
	leechHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, isZip, isUnzip bool)
	gateHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, resume func()) bool
	ytdlHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	ytdlCbHandler         func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	selectCbHandler       func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	saveCbHandler         func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	vtCbHandler           func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	vtInput               func(uid int64, key, val string) bool
	statusHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	statusCbHandler       func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	cancelHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	cancelAllHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	cancelCbHandler       func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	settingsHandler       func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	autoRenameHandler     func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	settingsCbHandler     func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	coverHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	shellHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	cmdHelpCbHandler      func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	userSettingsImporter  func(ctx context.Context, peer tg.InputPeerClass, replyTo int, userID int64, data []byte)
	settingsImporter      func(ctx context.Context, peer tg.InputPeerClass, replyTo int, data []byte)
	settingsUpdateHandler func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, u *types.UserSettings, key string)
	bsettingHandler       func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	bsettingCbHandler     func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	bsettingInputHandler  func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key, val string, page int)
	bsettingCancelHandler func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key string, page int)
	blacklistHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	rmBlacklistHandler    func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	usersHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	historyHandler        func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	retryCbHandler        func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	authHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	unauthHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	addSudoHandler        func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	rmSudoHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	addPremHandler        func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	rmPremHandler         func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	checkPremHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	startHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	helpHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	pingHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	statsHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	logHandler            func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	restartHandler        func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	speedtestHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	mediaInfoHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	broadcastHandler      func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	imdbHandler           func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)
	animeHandler          func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)

	statsCbHandler      func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	megaSelectCbHandler func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
	gdSelectCbHandler   func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)
}

type botUpdateHandler struct {
	app        *BotApp
	dispatcher tg.UpdateDispatcher
	gaps       *updates.Manager
}

func (h *botUpdateHandler) Handle(ctx context.Context, u tg.UpdatesClass) error {
	switch upd := u.(type) {
	case *tg.UpdateShortMessage:
		msg := &tg.Message{
			Out:       upd.Out,
			Mentioned: upd.Mentioned,
			ID:        upd.ID,
			PeerID:    &tg.PeerUser{UserID: upd.UserID},
			FromID:    &tg.PeerUser{UserID: upd.UserID},
			Message:   upd.Message,
			Date:      upd.Date,
			ReplyTo:   upd.ReplyTo,
			Entities:  upd.Entities,
		}
		h.app.routeMessage(ctx, msg, tg.Entities{
			Users: map[int64]*tg.User{
				upd.UserID: {
					ID: upd.UserID,
				},
			},
		})
		return nil

	case *tg.UpdateShortChatMessage:
		msg := &tg.Message{
			Out:       upd.Out,
			Mentioned: upd.Mentioned,
			ID:        upd.ID,
			PeerID:    &tg.PeerChat{ChatID: upd.ChatID},
			FromID:    &tg.PeerUser{UserID: upd.FromID},
			Message:   upd.Message,
			Date:      upd.Date,
			ReplyTo:   upd.ReplyTo,
			Entities:  upd.Entities,
		}
		h.app.routeMessage(ctx, msg, tg.Entities{
			Users: map[int64]*tg.User{
				upd.FromID: {
					ID: upd.FromID,
				},
			},
		})
		return nil

	default:
		switch upd := u.(type) {
		case *tg.Updates:
			for _, chat := range upd.Chats {
				if ch, ok := chat.(*tg.Channel); ok && ch.AccessHash != 0 && !ch.Min {
					h.app.cacheChannelAccessHash(ch.ID, ch.AccessHash)
				}
			}
			for _, user := range upd.Users {
				if usr, ok := user.(*tg.User); ok && usr.AccessHash != 0 {
					h.app.cacheAccessHash(usr.ID, usr.AccessHash)
				}
			}
		case *tg.UpdatesCombined:
			for _, chat := range upd.Chats {
				if ch, ok := chat.(*tg.Channel); ok && ch.AccessHash != 0 && !ch.Min {
					h.app.cacheChannelAccessHash(ch.ID, ch.AccessHash)
				}
			}
			for _, user := range upd.Users {
				if usr, ok := user.(*tg.User); ok && usr.AccessHash != 0 {
					h.app.cacheAccessHash(usr.ID, usr.AccessHash)
				}
			}
		}
		// straight to the handlers, like the Python bot: the gaps manager held group commands
		// back (a bot in privacy mode sees a "gap" before almost every group command, waits,
		// then Telegram makes it wait more for getChannelDifference), so replies came late
		return h.dispatcher.Handle(ctx, u)
	}
}

func NewBotApp() *BotApp {
	cfg := config.Get()
	userMap := types.NewSafeUserMap()

	dispatcher := tg.NewUpdateDispatcher()
	gaps := updates.New(updates.Config{
		Handler: dispatcher,
	})

	app := &BotApp{
		dispatcher: dispatcher,
		gaps:       gaps,
		userMap:    userMap,
		startTime:  time.Now(),
	}

	handler := &botUpdateHandler{
		app:        app,
		dispatcher: dispatcher,
		gaps:       gaps,
	}

	client := td.NewClient(cfg.TelegramAPI, cfg.TelegramHash, td.Options{
		UpdateHandler: handler,
	})
	app.client = client
	app.rawAPI = client.API()

	var userClient *td.Client
	var userRawAPI *tg.Client
	if cfg.UserSessionString != "" {
		if uStorage, err := telegram.ParseSessionString(context.Background(), cfg.UserSessionString); err == nil {
			userClient = td.NewClient(cfg.TelegramAPI, cfg.TelegramHash, td.Options{
				SessionStorage: uStorage,
			})
			userRawAPI = userClient.API()
		} else {
			log.Printf("[AZML-Go User Client] Failed to parse USER_SESSION_STRING: %v", err)
		}
	}
	app.userClient = userClient
	app.userRawAPI = userRawAPI

	app.sender = telegram.NewMessageSender(app.rawAPI)
	app.uploader = uploader.NewTelegramUploader(app.rawAPI, app.userRawAPI)

	app.registerDispatcher()
	return app
}

func (b *BotApp) SetLeechHandler(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, isZip, isUnzip bool)) {
	b.leechHandler = h
}

// SetGateHandler: the PM / FSUB check of task commands (false = blocked, the user was told).
func (b *BotApp) SetGateHandler(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext, resume func()) bool) {
	b.gateHandler = h
}

func (b *BotApp) SetYtdlHandler(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.ytdlHandler = h
	b.ytdlCbHandler = cb
}
func (b *BotApp) SetStatusHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.statusHandler = h
	b.statusCbHandler = cb
}
func (b *BotApp) SetCancelHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), all func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.cancelHandler = h
	b.cancelAllHandler = all
	b.cancelCbHandler = cb
}
func (b *BotApp) SetSettingsHandlers(
	h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext),
	cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64),
	update func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, u *types.UserSettings, key string),
) {
	b.settingsHandler = h
	b.settingsCbHandler = cb
	b.settingsUpdateHandler = update
}
func (b *BotApp) SetAutoRenameHandler(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)) {
	b.autoRenameHandler = h
}
func (b *BotApp) SetBotSettingsHandlers(h func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.bsettingHandler = h
	b.bsettingCbHandler = cb
}
func (b *BotApp) SetBotSettingsInputHandlers(val func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key, val string, page int), cancel func(ctx context.Context, peer tg.InputPeerClass, promptMsgID int, key string, page int)) {
	b.bsettingInputHandler = val
	b.bsettingCancelHandler = cancel
}

// SetToolHandlers: /cover and /shell.
func (b *BotApp) SetToolHandlers(cover, shell func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)) {
	b.coverHandler = cover
	b.shellHandler = shell
}

// SetCmdHelpHandler: the buttons of the /leech /mirror /ytdl usage menu.
func (b *BotApp) SetCmdHelpHandler(h func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.cmdHelpCbHandler = h
}

// SetUserSettingsImporter: the azml_user_settings.json of /us Export (sent after /us → Import).
func (b *BotApp) SetUserSettingsImporter(fn func(ctx context.Context, peer tg.InputPeerClass, replyTo int, userID int64, data []byte)) {
	b.userSettingsImporter = fn
}

// SetSettingsImporter: the azml_settings.json of /bs Export (sent after /bs → Import).
func (b *BotApp) SetSettingsImporter(fn func(ctx context.Context, peer tg.InputPeerClass, replyTo int, data []byte)) {
	b.settingsImporter = fn
}

func (b *BotApp) SetUserRawAPIUpdater(fn func(userRawAPI *tg.Client)) {
	b.userUpdater = fn
}

func (b *BotApp) ReloadUserSession(ctx context.Context, sessionStr string) (string, int64, bool, error) {
	b.userMu.Lock()
	defer b.userMu.Unlock()

	sessionStr = strings.TrimSpace(sessionStr)
	if sessionStr == "" {
		telegram.SetActiveUserPool(nil)
		if b.userPool != nil {
			b.userPool.Close()
			b.userPool = nil
		}
		if b.userCancel != nil {
			b.userCancel()
			b.userCancel = nil
		}
		b.userClient = nil
		b.userRawAPI = nil
		telegram.StopMediaClient()
		telegram.SetActiveTD(b.client, nil)
		if b.uploader != nil {
			b.uploader.SetUserClient(nil)
		}
		if b.userUpdater != nil {
			b.userUpdater(nil)
		}
		config.SetIsPremiumUser(false)
		_ = config.UpdateConfigVar("USER_SESSION_STRING", "")
		return "", 0, false, nil
	}

	uStorage, err := telegram.ParseSessionString(ctx, sessionStr)
	if err != nil {
		return "", 0, false, fmt.Errorf("failed to parse session string: %w", err)
	}

	cfg := config.Get()
	newUserClient := td.NewClient(cfg.TelegramAPI, cfg.TelegramHash, td.Options{
		SessionStorage: uStorage,
	})

	uCtx, uCancel := context.WithCancel(context.Background())
	type authResult struct {
		user *tg.User
		err  error
	}
	resCh := make(chan authResult, 1)

	go func() {
		_ = newUserClient.Run(uCtx, func(cCtx context.Context) error {
			status, sErr := newUserClient.Auth().Status(cCtx)
			if sErr != nil {
				resCh <- authResult{err: sErr}
				return sErr
			}
			if !status.Authorized {
				resCh <- authResult{err: fmt.Errorf("session string is not authorized")}
				return fmt.Errorf("session not authorized")
			}
			userSelf, selfErr := newUserClient.Self(cCtx)
			if selfErr != nil {
				resCh <- authResult{err: selfErr}
				return selfErr
			}
			// Pre-warm MTProto connection pool, auth keys, and DC configuration
			_, _ = newUserClient.API().HelpGetConfig(cCtx)
			resCh <- authResult{user: userSelf}
			<-cCtx.Done()
			return cCtx.Err()
		})
	}()

	select {
	case res := <-resCh:
		if res.err != nil {
			uCancel()
			return "", 0, false, fmt.Errorf("session login failed: %w", res.err)
		}

		telegram.SetActiveUserPool(nil)
		if b.userPool != nil {
			b.userPool.Close()
			b.userPool = nil
		}
		if b.userCancel != nil {
			b.userCancel()
		}
		b.userCancel = uCancel
		b.userClient = newUserClient
		b.userRawAPI = newUserClient.API()
		telegram.SetActiveTD(b.client, newUserClient)
		// downloads like wzgram: a second connection of the session on Telegram's media servers

		uname := res.user.Username
		if uname == "" {
			uname = res.user.FirstName
		}
		isPrem := res.user.Premium
		config.SetIsPremiumUser(isPrem)
		_ = config.UpdateConfigVar("USER_SESSION_STRING", sessionStr)

		if isPrem {
			log.Printf("[AZML-Go User Client] Activated User Session as @%s (ID: %d, PREMIUM ACTIVE: Max Split 3.91GB / 4GB enabled)", uname, res.user.ID)
		} else {
			log.Printf("[AZML-Go User Client] Activated User Session as @%s (ID: %d, NON-PREMIUM: Max Split 1.91GB)", uname, res.user.ID)
		}

		// Initialize parallel client pool (matching WZGram Premium 14-session pool)
		poolSize := 14
		if !isPrem {
			poolSize = 8
		}
		sData, _ := telegram.ParseSessionData(ctx, sessionStr)
		if sData != nil {
			uPool, pErr := telegram.StartUserClientPool(uCtx, cfg.TelegramAPI, cfg.TelegramHash, sData, b.userRawAPI, poolSize)
			if pErr == nil && uPool != nil {
				b.userPool = uPool
				telegram.SetActiveUserPool(uPool.Clients())
				if b.uploader != nil {
					b.uploader.SetUserClientPool(b.userRawAPI, uPool.Clients())
				}
			} else {
				if b.uploader != nil {
					b.uploader.SetUserClient(b.userRawAPI)
				}
			}
		} else {
			if b.uploader != nil {
				b.uploader.SetUserClient(b.userRawAPI)
			}
		}

		if b.userUpdater != nil {
			b.userUpdater(b.userRawAPI)
		}

		// Proactively grab & cache AccessHash for DUMP_CHAT_ID and LEECH_LOG_ID for future uploads
		go func(rawAPI *tg.Client) {
			preCtx, preCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer preCancel()
			currCfg := config.Get()
			if currCfg == nil || rawAPI == nil {
				return
			}
			if currCfg.DumpChatID != 0 {
				hash := telegram.ResolveUserChannelHash(preCtx, rawAPI, currCfg.DumpChatID)
				if hash != 0 {
					log.Printf("[AZML-Go User Client] Successfully grabbed & cached AccessHash for DUMP_CHAT_ID %d (AccessHash: %d)", currCfg.DumpChatID, hash)
				} else {
					log.Printf("[AZML-Go User Client] Warning: Could not resolve AccessHash for DUMP_CHAT_ID %d during pre-warm", currCfg.DumpChatID)
				}
			}
			if currCfg.LeechLogID != 0 && currCfg.LeechLogID != currCfg.DumpChatID {
				lHash := telegram.ResolveUserChannelHash(preCtx, rawAPI, currCfg.LeechLogID)
				if lHash != 0 {
					log.Printf("[AZML-Go User Client] Successfully grabbed & cached AccessHash for LEECH_LOG_ID %d (AccessHash: %d)", currCfg.LeechLogID, lHash)
				}
			}
		}(b.userRawAPI)

		return uname, res.user.ID, isPrem, nil

	case <-time.After(20 * time.Second):
		uCancel()
		return "", 0, false, fmt.Errorf("session connection timed out (20s)")
	case <-ctx.Done():
		uCancel()
		return "", 0, false, ctx.Err()
	}
}
func (b *BotApp) SetAuthorizeHandlers(auth, unauth, addSudo, rmSudo, addPrem, rmPrem, checkPrem func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)) {
	b.authHandler = auth
	b.unauthHandler = unauth
	b.addSudoHandler = addSudo
	b.rmSudoHandler = rmSudo
	b.addPremHandler = addPrem
	b.rmPremHandler = rmPrem
	b.checkPremHandler = checkPrem
}
func (b *BotApp) SetAdminHandlers(blacklist, rmBlacklist, users func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)) {
	b.blacklistHandler = blacklist
	b.rmBlacklistHandler = rmBlacklist
	b.usersHandler = users
}

// SetVTHandlers: the Video Tools (-vt) menu buttons and its text input (Rename / Trim).
func (b *BotApp) SetVTHandlers(cb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64), input func(uid int64, key, val string) bool) {
	b.vtCbHandler, b.vtInput = cb, input
}

// SetSaveHandler: the Save Message button.
func (b *BotApp) SetSaveHandler(h func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.saveCbHandler = h
}

// SetSelectHandler: buttons of the torrent file selection.
func (b *BotApp) SetSelectHandler(h func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.selectCbHandler = h
}

// SetTaskExtras: /history and the Retry button of failed tasks.
func (b *BotApp) SetTaskExtras(history func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext), retry func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.historyHandler = history
	b.retryCbHandler = retry
}
func (b *BotApp) SetHelpHandlers(
	start, help, ping, stats, logH, restart func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext),
	statsCb func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64),
) {
	b.startHandler = start
	b.helpHandler = help
	b.pingHandler = ping
	b.statsHandler = stats
	b.logHandler = logH
	b.restartHandler = restart
	b.statsCbHandler = statsCb
}
func (b *BotApp) SetExtraHandlers(speed, mi, bc, imdb, anime func(ctx context.Context, peer tg.InputPeerClass, msg *types.MessageContext)) {
	b.speedtestHandler = speed
	b.mediaInfoHandler = mi
	b.broadcastHandler = bc
	b.imdbHandler = imdb
	b.animeHandler = anime
}
func (b *BotApp) SetSelectHandlers(mega, gd func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64)) {
	b.megaSelectCbHandler = mega
	b.gdSelectCbHandler = gd
}
func (b *BotApp) GetRawAPI() *tg.Client                   { return b.rawAPI }
func (b *BotApp) GetUserRawAPI() *tg.Client               { return b.userRawAPI }
func (b *BotApp) GetSender() *telegram.MessageSender      { return b.sender }
func (b *BotApp) GetUploader() *uploader.TelegramUploader { return b.uploader }
func (b *BotApp) GetUserMap() *types.SafeUserMap          { return b.userMap }
func (b *BotApp) GetStartTime() time.Time                 { return b.startTime }
func (b *BotApp) SetSessionPool(p *pool.SessionPool)      { b.sessPool = p }
func (b *BotApp) GetSessionPool() *pool.SessionPool       { return b.sessPool }

func (b *BotApp) registerDispatcher() {
	b.dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewMessage) error {
		msg, ok := update.Message.(*tg.Message)
		if !ok || msg.Out {
			return nil
		}
		go b.routeMessage(ctx, msg, e)
		return nil
	})

	b.dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewChannelMessage) error {
		for chID, ch := range e.Channels {
			if ch.AccessHash != 0 && !ch.Min {
				b.cacheChannelAccessHash(chID, ch.AccessHash)
			}
		}
		msg, ok := update.Message.(*tg.Message)
		if !ok || msg.Out {
			return nil
		}
		go b.routeMessage(ctx, msg, e)
		return nil
	})

	b.dispatcher.OnBotCallbackQuery(func(ctx context.Context, e tg.Entities, update *tg.UpdateBotCallbackQuery) error {
		for chID, ch := range e.Channels {
			if ch.AccessHash != 0 && !ch.Min {
				b.cacheChannelAccessHash(chID, ch.AccessHash)
			}
		}
		for uID, u := range e.Users {
			if u.AccessHash != 0 {
				b.cacheAccessHash(uID, u.AccessHash)
			}
		}
		go b.routeCallback(ctx, update, e)
		return nil
	})

	// someone joined a channel the bot is admin in: his "join first" message updates now
	b.dispatcher.OnChannelParticipant(func(ctx context.Context, e tg.Entities, update *tg.UpdateChannelParticipant) error {
		if update.NewParticipant != nil {
			if _, left := update.NewParticipant.(*tg.ChannelParticipantLeft); !left {
				ext.GateKick(update.UserID)
			}
		}
		return nil
	})

	// buttons of ephemeral messages (seen only by one user) come as their own update
	b.dispatcher.OnEphemeralBotCallbackQuery(func(ctx context.Context, e tg.Entities, update *tg.UpdateEphemeralBotCallbackQuery) error {
		for uID, u := range e.Users {
			if u.AccessHash != 0 {
				b.cacheAccessHash(uID, u.AccessHash)
			}
		}
		cb := &tg.UpdateBotCallbackQuery{QueryID: update.QueryID, UserID: update.UserID, Peer: update.Peer, MsgID: update.MsgID, Data: update.Data, ChatInstance: update.ChatInstance}
		go func() {
			b.routeCallback(ctx, cb, e)
			// the answer is done: remove the ephemeral question
			if peer := b.getPeerWithEntities(update.Peer, e); peer != nil {
				user := &tg.InputUser{UserID: update.UserID, AccessHash: telegram.GetUserAccessHash(update.UserID)}
				_, _ = b.rawAPI.EphemeralDeleteMessage(context.Background(), &tg.EphemeralDeleteMessageRequest{Peer: peer, ReceiverID: user, ID: update.MsgID})
			}
		}()
		return nil
	})
}

func (b *BotApp) routeMessage(ctx context.Context, m *tg.Message, entities tg.Entities) {
	// like the Python bot (Pyrogram skip_updates): messages sent before this start are old
	// updates Telegram delivers again after a restart; running them would start the tasks again
	if m.Date > 0 && int64(m.Date) < b.startTime.Unix()-5 {
		return
	}
	peer := b.getPeerWithEntities(m.PeerID, entities)
	if peer == nil {
		log.Printf("[AZML-Go Bot Client] Could not resolve peer for msg %d", m.ID)
		return
	}

	// the chat id like the Python bot (Bot API form): groups are -100…, basic groups -…
	chatID := botAPIChatID(m.PeerID)
	senderID := chatID
	senderName := "User"
	senderUsername := ""
	senderLang := ""

	if m.FromID != nil {
		if uPeer, ok := m.FromID.(*tg.PeerUser); ok {
			senderID = uPeer.UserID
			if uObj, exists := entities.Users[senderID]; exists {
				senderName = uObj.FirstName
				senderUsername = uObj.Username
				senderLang = uObj.LangCode
				b.cacheAccessHash(senderID, uObj.AccessHash)
			}
		}
	} else if uPeer, ok := m.PeerID.(*tg.PeerUser); ok {
		senderID = uPeer.UserID
		if uObj, exists := entities.Users[senderID]; exists {
			senderName = uObj.FirstName
			senderUsername = uObj.Username
			senderLang = uObj.LangCode
			b.cacheAccessHash(senderID, uObj.AccessHash)
		}
	}

	text := strings.TrimSpace(m.Message)

	// Automatically record user has started/interacted in PM
	if chatID == senderID {
		defer ext.GateKick(senderID) // his "start the bot" message updates now
		u := b.userMap.GetOrCreate(senderID)
		if !u.StartedInPM {
			u.StartedInPM = true
			b.userMap.Set(u)
			go func(usr *types.UserSettings) {
				_ = ext.DB.SaveUser(usr)
			}(u)
		}
	}

	// Check if user is in an active pending setting prompt session (e.g. from /us)
	if sess, ok := ext.GetPendingSettingSession(senderID, chatID); ok && sess != nil && !((sess.Key == "ycookies" || sess.Key == "importset") && m.Media != nil) {
		inMsgID := m.ID
		if telegram.IsCancelCommand(text, b.botUsername) {
			ext.ClearPendingSettingSession(senderID)
			go func() {
				delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
			}()
			if sess.PromptMsgID > 0 && b.settingsUpdateHandler != nil && !strings.HasPrefix(sess.Key, "vt_") {
				u := b.userMap.GetOrCreate(senderID)
				b.settingsUpdateHandler(ctx, peer, sess.PromptMsgID, u, sess.Key)
			}
			return
		}
		if !strings.HasPrefix(text, "/") {
			ext.ClearPendingSettingSession(senderID)
			go func() {
				delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
			}()
			u := b.userMap.GetOrCreate(senderID)
			if sess.Key == "importset" { // a file is expected, not text
				ext.SetSettingNote(senderID, "× <i>Send the file, not text.</i>")
				if sess.PromptMsgID > 0 && b.settingsUpdateHandler != nil {
					b.settingsUpdateHandler(ctx, peer, sess.PromptMsgID, u, "importset")
				}
				return
			}
			b.applyUserSettingValue(ctx, peer, u, sess.Key, text, sess.PromptMsgID)
			return
		}
	}

	// Check if user is in an active pending bot setting prompt session (from /bs)
	if bSess, ok := ext.GetPendingBotSettingSession(senderID, chatID); ok && bSess != nil {
		inMsgID := m.ID
		if bSess.Key == "IMPORT_SETTINGS" && !telegram.IsCancelCommand(text, b.botUsername) {
			if docMedia, ok := m.Media.(*tg.MessageMediaDocument); ok && b.settingsImporter != nil {
				if doc, ok := docMedia.Document.(*tg.Document); ok && doc.Size <= 1<<20 {
					ext.ClearPendingBotSettingSession(senderID)
					var buf bytes.Buffer
					if _, err := tdDownloader.NewDownloader().Download(b.rawAPI, doc.AsInputDocumentFileLocation("")).Stream(ctx, &buf); err == nil {
						b.settingsImporter(ctx, peer, bSess.PromptMsgID, buf.Bytes())
					}
					_ = b.sender.DeleteMessage(ctx, peer, []int{inMsgID})
					return
				}
			}
			if !strings.HasPrefix(text, "/") { // text while a file is expected: keep waiting
				_ = b.sender.DeleteMessage(ctx, peer, []int{inMsgID})
				return
			}
		}
		if telegram.IsCancelCommand(text, b.botUsername) {
			ext.ClearPendingBotSettingSession(senderID)
			go func() {
				delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
			}()
			if b.bsettingCancelHandler != nil {
				b.bsettingCancelHandler(ctx, peer, bSess.PromptMsgID, bSess.Key, bSess.Page)
			}
			return
		}
		if !strings.HasPrefix(text, "/") {
			ext.ClearPendingBotSettingSession(senderID)
			go func() {
				delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
			}()
			if b.bsettingInputHandler != nil {
				b.bsettingInputHandler(ctx, peer, bSess.PromptMsgID, bSess.Key, text, bSess.Page)
			}
			return
		}
	}

	if sess, ok := ext.GetPendingThumbSession(senderID, chatID); ok && telegram.IsCancelCommand(text, b.botUsername) {
		inMsgID := m.ID
		ext.ClearPendingThumbSession(senderID)
		go func() {
			delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = b.sender.DeleteMessage(delCtx, peer, []int{inMsgID})
		}()
		if sess != nil && sess.CmdMsgID > 0 { // the /t prompt: it says so and goes with the command
			b.finishThumbPrompt(peer, sess, "× <i>Cancelled.</i>")
			return
		}
		if sess != nil && sess.PromptMsgID > 0 && b.settingsUpdateHandler != nil {
			u := b.userMap.GetOrCreate(senderID)
			b.settingsUpdateHandler(ctx, peer, sess.PromptMsgID, u, "thumb")
		}
		return
	}

	// 1. Handle incoming private media attachments (token.pickle, config.env, cookies.txt, accounts.zip, thumbnails)
	if m.Media != nil {
		if b.handleIncomingMedia(ctx, peer, m, senderID, chatID) {
			return
		}
		if text == "" {
			return
		}
	}

	// 2. ONLY respond to bot commands! Ignore all normal chatting and other bots' messages completely.
	if !telegram.BotCommands.IsAnyCommand(text, b.botUsername) {
		return
	}

	msgCtx := &types.MessageContext{
		ChatID:     chatID,
		MessageID:  m.ID,
		UserID:     senderID,
		SenderName: senderName,
		Username:   senderUsername,
		LangCode:   senderLang,
		Text:       text,
		HasMedia:   isFileMedia(m.Media),
	}
	if msgCtx.HasMedia {
		msgCtx.RawMedia = m.Media
	}

	if m.ReplyTo != nil {
		if header, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			msgCtx.IsReply = true
			msgCtx.ReplyToID = header.ReplyToMsgID

			// 1. Direct media attached to reply header if provided by Telegram
			if isFileMedia(header.ReplyMedia) {
				msgCtx.HasMedia = true
				msgCtx.RawMedia = header.ReplyMedia
			}

			// 2. Fetch replied message directly from this chat with generous timeout (8s) so concurrent tasks never miss media
			if msgCtx.RawMedia == nil {
				fetchCtx, fetchCancel := context.WithTimeout(ctx, 8*time.Second)
				var repliedMsg *tg.Message
			fetchLoop:
				for attempt := 0; attempt < 3; attempt++ {
					var err error
					repliedMsg, err = telegram.FetchMessage(fetchCtx, b.rawAPI, peer, msgCtx.ReplyToID)
					if err == nil && repliedMsg != nil {
						break
					}
					select {
					case <-fetchCtx.Done():
						break fetchLoop
					case <-time.After(250 * time.Millisecond):
					}
				}
				if repliedMsg != nil {
					msgCtx.ReplyText = repliedMsg.Message
					if isFileMedia(repliedMsg.Media) {
						msgCtx.HasMedia = true
						msgCtx.RawMedia = repliedMsg.Media
					}
				}
				fetchCancel()
			}
		}
	}

	// 3. PUBLIC COMMANDS: /start, /ping, /help (Always answered regardless of authorization)
	if telegram.MatchCommand(text, telegram.BotCommands.Start, b.botUsername) {
		if b.startHandler != nil {
			b.startHandler(ctx, peer, msgCtx)
		}
		return
	}
	if telegram.MatchCommand(text, telegram.BotCommands.Ping, b.botUsername) {
		if b.pingHandler != nil {
			b.pingHandler(ctx, peer, msgCtx)
		}
		return
	}
	if telegram.MatchCommand(text, telegram.BotCommands.Help, b.botUsername) {
		if b.helpHandler != nil {
			b.helpHandler(ctx, peer, msgCtx)
		}
		return
	}

	// 4. Blacklist check
	if telegram.IsBlacklisted(senderID, b.userMap) {
		return
	}

	// 5. Authorization check for leech, admin, settings, and task commands
	if !telegram.IsAuthorized(chatID, senderID, b.userMap) {
		log.Printf("[AZML-Go] Unauthorized command %q by user %d in chat %d", text, senderID, chatID)
		sentID, _ := b.sender.SendMessage(ctx, peer, "⌬ <b>Access Denied!</b>\nYou are not authorized to use this bot.\nContact the bot owner to get authorization.", m.ID, nil)
		if sentID > 0 {
			switch m.PeerID.(type) {
			case *tg.PeerChat, *tg.PeerChannel: // the answer and the command go together
				b.sender.AutoDeleteMessage(peer, sentID, 15)
				b.sender.AutoDeleteMessage(peer, m.ID, 15)
			}
		}
		return
	}

	// 6. no "start the bot in PM" check here, like the Python bot: /us, /status, /history ...
	// work for everyone authorized; the task commands (/leech /mirror /ytdl /cover) check the
	// PM and FSUB themselves and start by themselves once the user is done

	isCancel, _ := telegram.MatchCancelCommand(text, b.botUsername)

	// Dispatch commands - all check against current bot username
	switch {
	case telegram.MatchCommand(text, telegram.BotCommands.Leech, b.botUsername):
		isZip := strings.Contains(strings.ToLower(text), "zip") && !strings.Contains(strings.ToLower(text), "unzip")
		isUnzip := strings.Contains(strings.ToLower(text), "unzip")
		if b.leechHandler != nil {
			b.leechHandler(ctx, peer, msgCtx, isZip, isUnzip)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Mirror, b.botUsername):
		b.handleMirrorCommand(ctx, peer, msgCtx)
	case telegram.MatchCommand(text, telegram.BotCommands.Ytdl, b.botUsername):
		if b.ytdlHandler != nil {
			b.ytdlHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Status, b.botUsername):
		if b.statusHandler != nil {
			b.statusHandler(ctx, peer, msgCtx)
		}
	case isCancel:
		if b.cancelHandler != nil {
			b.cancelHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.CancelAll, b.botUsername):
		if b.cancelAllHandler != nil {
			b.cancelAllHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.UserSetting, b.botUsername):
		if b.settingsHandler != nil {
			b.settingsHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.AutoRename, b.botUsername):
		if b.autoRenameHandler != nil {
			b.autoRenameHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.BotSetting, b.botUsername):
		if b.bsettingHandler != nil {
			b.bsettingHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Authorize, b.botUsername):
		if b.authHandler != nil {
			b.authHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.UnAuthorize, b.botUsername):
		if b.unauthHandler != nil {
			b.unauthHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.AddSudo, b.botUsername):
		if b.addSudoHandler != nil {
			b.addSudoHandler(ctx, peer, msgCtx)
			go b.registerBotCommands(context.Background()) // he gets the admin commands
		}
	case telegram.MatchCommand(text, telegram.BotCommands.RmSudo, b.botUsername):
		if b.rmSudoHandler != nil {
			b.rmSudoHandler(ctx, peer, msgCtx)
			go b.registerBotCommands(context.Background())
		}
	case telegram.MatchCommand(text, telegram.BotCommands.AddPremium, b.botUsername):
		if b.addPremHandler != nil {
			b.addPremHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.RmPremium, b.botUsername):
		if b.rmPremHandler != nil {
			b.rmPremHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Blacklist, b.botUsername):
		if b.blacklistHandler != nil {
			b.blacklistHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.RmBlacklist, b.botUsername):
		if b.rmBlacklistHandler != nil {
			b.rmBlacklistHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.History, b.botUsername):
		if b.historyHandler != nil {
			b.historyHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Users, b.botUsername):
		if b.usersHandler != nil {
			b.usersHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Premium, b.botUsername):
		if b.checkPremHandler != nil {
			b.checkPremHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Help, b.botUsername):
		if b.helpHandler != nil {
			b.helpHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Ping, b.botUsername):
		if b.pingHandler != nil {
			b.pingHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Stats, b.botUsername):
		if b.statsHandler != nil {
			b.statsHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Log, b.botUsername):
		if b.logHandler != nil {
			b.logHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Cover, b.botUsername):
		if b.coverHandler != nil {
			b.coverHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Shell, b.botUsername):
		if b.shellHandler != nil {
			b.shellHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Restart, b.botUsername):
		if b.restartHandler != nil {
			b.restartHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Speedtest, b.botUsername):
		if b.speedtestHandler != nil {
			b.speedtestHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.MediaInfo, b.botUsername):
		if b.mediaInfoHandler != nil {
			b.mediaInfoHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Broadcast, b.botUsername):
		if b.broadcastHandler != nil {
			b.broadcastHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Imdb, b.botUsername):
		if b.imdbHandler != nil {
			b.imdbHandler(ctx, peer, msgCtx)
		}
	case telegram.MatchCommand(text, telegram.BotCommands.Anime, b.botUsername):
		if b.animeHandler != nil {
			b.animeHandler(ctx, peer, msgCtx)
		}
	}
}

func (b *BotApp) routeCallback(ctx context.Context, cb *tg.UpdateBotCallbackQuery, entities tg.Entities) {
	data := string(cb.Data)
	peer := b.getPeerWithEntities(cb.Peer, entities)
	if peer == nil {
		return
	}

	switch {
	case strings.HasPrefix(data, "stats") || strings.HasPrefix(data, "wzmlx") || strings.HasPrefix(data, "start_"):
		if b.statsCbHandler != nil {
			b.statsCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "status"):
		if b.statusCbHandler != nil {
			b.statusCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "canall"):
		if b.cancelCbHandler != nil {
			b.cancelCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "uset") || strings.HasPrefix(data, "userset"):
		if b.settingsCbHandler != nil {
			b.settingsCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "retry "):
		if b.retryCbHandler != nil {
			b.retryCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "vtool "):
		if b.vtCbHandler != nil {
			b.vtCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case data == "save":
		if b.saveCbHandler != nil {
			b.saveCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "chelp "):
		if b.cmdHelpCbHandler != nil {
			b.cmdHelpCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "sel "):
		if b.selectCbHandler != nil {
			b.selectCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "ytq "):
		if b.ytdlCbHandler != nil {
			b.ytdlCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "mirror "):
		b.handleMirrorCallback(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
	case strings.HasPrefix(data, "botset"):
		if b.bsettingCbHandler != nil {
			b.bsettingCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "megasel"):
		if b.megaSelectCbHandler != nil {
			b.megaSelectCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	case strings.HasPrefix(data, "gdsel"):
		if b.gdSelectCbHandler != nil {
			b.gdSelectCbHandler(ctx, peer, cb.QueryID, cb.MsgID, data, cb.UserID)
		}
	}
}

func (b *BotApp) Start(ctx context.Context) error {
	cfg := config.Get()

	// Start user session client if configured
	if cfg.UserSessionString != "" {
		go func() {
			log.Println("[AZML-Go User Client] Pre-warming User Session from DB/Config...")
			uname, uid, isPrem, err := b.ReloadUserSession(context.Background(), cfg.UserSessionString)
			if err != nil {
				log.Printf("[AZML-Go User Client] Startup login error for USER_SESSION_STRING: %v", err)
			} else {
				log.Printf("[AZML-Go User Client] User session active and pre-warmed on start: @%s (ID: %d, Premium: %v, Workers: 16)", uname, uid, isPrem)
			}
		}()
	} else {
		config.SetIsPremiumUser(false)
		log.Println("[AZML-Go User Client] No user session string configured (Max Split 1.91GB / 2GB upload mode active)")
	}

	log.Println("[AZML-Go Bot Client] Starting bot client...")
	return b.client.Run(ctx, func(ctx context.Context) error {
		// Log in as Bot
		status, err := b.client.Auth().Status(ctx)
		if err != nil {
			return err
		}

		if !status.Authorized {
			if _, err := b.client.Auth().Bot(ctx, cfg.BotToken); err != nil {
				return fmt.Errorf("bot authentication failed: %v", err)
			}
		}

		telegram.SetActiveTD(b.client, telegram.ActiveUserTD())
		botSelf, _ := b.client.Self(ctx)
		if botSelf != nil {
			b.botUsername = botSelf.Username
			telegram.SetBotUsername(botSelf.Username)
			uname := botSelf.Username
			if uname == "" {
				uname = botSelf.FirstName
			}
			log.Printf("[AZML-Go Bot Client] Logged in successfully with Bot Token as @%s (ID: %d)", uname, botSelf.ID)
		} else {
			log.Println("[AZML-Go Bot Client] Logged in successfully with Bot Token")
		}
		// downloads like wzgram: a second bot connection on Telegram's media servers
		go telegram.StartBotMediaClient(cfg.TelegramAPI, cfg.TelegramHash, cfg.BotToken)

		// uploads: the parts go over several connections of the bot (one connection ~2MB/s)
		if upPool, pErr := b.client.Pool(8); pErr == nil {
			if b.uploader != nil {
				b.uploader.SetBotUploadClients([]*tg.Client{tg.NewClient(upPool)})
			}
			log.Printf("[AZML-Go Bot Client] Upload pool ready (8 connections)")
		} else {
			log.Printf("[AZML-Go Bot Client] Upload pool failed: %v", pErr)
		}

		// Register all bot commands in Telegram's autocomplete list
		b.registerBotCommands(ctx)

		// Telegram pushes updates to a connection that asked for its state once
		if _, err := b.rawAPI.UpdatesGetState(ctx); err != nil {
			log.Printf("[AZML-Go Bot Client] updates.getState: %v", err)
		}

		// Ensure Aria2c is up with 16 connections
		if err := ext.DB.LoadAllUsers(b.userMap); err != nil {
			log.Printf("[AZML-Go Bot Client] Failed to load persisted user and authorization data: %v", err)
		}
		ext.RestoreThumbnails(b.userMap)
		log.Println("[AZML-Go Bot Client] AZML-Go started and listening for commands!")

		// Trigger startup notification
		go b.sendStartupNotification(ctx)

		// Pre-warm and cache Channel AccessHash for Main Bot
		go func() {
			preCtx, preCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer preCancel()
			currCfg := config.Get()
			if currCfg == nil || b.rawAPI == nil {
				return
			}
			if currCfg.DumpChatID != 0 {
				// re-check with the bot itself: an older version saved the user session's hash here
				telegram.ForgetChannelAccessHash(pool.RawChannelID(currCfg.DumpChatID))
				if currCfg.LeechLogID != 0 {
					telegram.ForgetChannelAccessHash(pool.RawChannelID(currCfg.LeechLogID))
				}
				dHash := telegram.ResolveChannelAccessHash(preCtx, b.rawAPI, currCfg.DumpChatID)
				if dHash != 0 {
					log.Printf("[AZML-Go Bot Client] Successfully pre-warmed & cached AccessHash for DUMP_CHAT_ID %d (AccessHash: %d)", currCfg.DumpChatID, dHash)
				}
			}
			if currCfg.LeechLogID != 0 && currCfg.LeechLogID != currCfg.DumpChatID {
				lHash := telegram.ResolveChannelAccessHash(preCtx, b.rawAPI, currCfg.LeechLogID)
				if lHash != 0 {
					log.Printf("[AZML-Go Bot Client] Successfully pre-warmed & cached AccessHash for LEECH_LOG_ID %d (AccessHash: %d)", currCfg.LeechLogID, lHash)
				}
			}
		}()

		<-ctx.Done()
		return ctx.Err()
	})
}

func (b *BotApp) sendStartupNotification(ctx context.Context) {
	time.Sleep(1 * time.Second)
	cfg := config.Get()
	now := time.Now()
	dateStr := now.Format("02/01/06")
	timeStr := now.Format("03:04:05 PM")
	timz := cfg.Timezone
	if timz == "" {
		timz = "Asia/Kolkata"
	}

	// 1. Check if .restartmsg exists
	restartData, err := os.ReadFile(".restartmsg")
	if err == nil && len(restartData) > 0 {
		_ = os.Remove(".restartmsg")
		parts := strings.Fields(string(restartData))
		if len(parts) >= 2 {
			chatID, _ := strconv.ParseInt(parts[0], 10, 64)
			msgID, _ := strconv.Atoi(parts[1])
			if chatID != 0 && msgID != 0 {
				restartSuccessText := fmt.Sprintf("⌬ <b><i>Restarted Successfully!</i></b>\n"+
					"<blockquote><b>╭</b> <b>Date:</b> %s\n"+
					"┠ <b>Time:</b> %s\n"+
					"┠ <b>TimeZone:</b> %s\n"+
					"<b>╰</b> <b>Version:</b> %s</blockquote>", dateStr, timeStr, timz, ext.BotVersion())

				peer := b.getPeerForChatID(chatID)
				if b.sender != nil && peer != nil {
					_ = b.sender.EditMessage(ctx, peer, msgID, restartSuccessText, nil)
				}
				return
			}
		}
	}

	// 2. Otherwise send Bot Started Successfully notification to Authorized Chats (GC) or Owner via MTProto
	startMsg := fmt.Sprintf("⌬ <b><i>Bot Started Successfully!</i></b>\n"+
		"<blockquote><b>╭</b> <b>Date:</b> %s\n"+
		"┠ <b>Time:</b> %s\n"+
		"┠ <b>TimeZone:</b> %s\n"+
		"<b>╰</b> <b>Version:</b> %s</blockquote>", dateStr, timeStr, timz, ext.BotVersion())

	sendNotification := func(targetID int64) {
		if targetID == 0 || b.sender == nil {
			return
		}
		peer := b.getPeerForChatID(targetID)
		if peer != nil {
			_, err := b.sender.SendMessage(ctx, peer, startMsg, 0, nil)
			if err != nil {
				log.Printf("[AZML-Go Bot Client] Error sending startup message to %d: %v", targetID, err)
			} else {
				log.Printf("[AZML-Go Bot Client] Startup notification sent to %d successfully via MTProto!", targetID)
			}
		}
	}

	// groups hear from the bot only when their tasks were stopped by the restart (like the Python
	// bot, INCOMPLETE_TASK_NOTIFIER); "Bot Started" goes to the owner's PM only
	b.notifyIncompleteTasks(ctx)
	if cfg.OwnerID != 0 {
		log.Printf("[AZML-Go Bot Client] Sending startup notification to the owner: %d", cfg.OwnerID)
		sendNotification(cfg.OwnerID)
	}
}

func (b *BotApp) registerBotCommands(ctx context.Context) {
	cfg := config.Get()
	if !cfg.SetCommands {
		return
	}
	cmds := make([]tg.BotCommand, 0)

	addCmd := func(cmd, desc string) {
		clean := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(cmd, "/")))
		if clean == "" {
			return
		}
		if len(clean) > 32 {
			clean = clean[:32]
		}
		if len(desc) < 3 {
			desc = desc + " command"
		}
		if len(desc) > 256 {
			desc = desc[:256]
		}
		cmds = append(cmds, tg.BotCommand{
			Command:     clean,
			Description: desc,
		})
	}

	bc := telegram.BotCommands
	// everyone: the order of the Python bot (Leech-TJ)
	if len(bc.Mirror) > 1 {
		addCmd(bc.Mirror[0], fmt.Sprintf("or /%s Upload to GoFile, PixelDrain and more", bc.Mirror[1]))
	}

	if len(bc.Leech) > 1 {
		addCmd(bc.Leech[0], fmt.Sprintf("or /%s Leech [links/media/rclone_path]", bc.Leech[1]))
	} else if len(bc.Leech) > 0 {
		addCmd(bc.Leech[0], "Leech [links/media/rclone_path]")
	}

	if cfg.ShowExtraCmds && len(bc.Leech) >= 6 {
		addCmd(bc.Leech[2], fmt.Sprintf("or /%s Leech and Zip [links/media]", bc.Leech[3]))
		addCmd(bc.Leech[4], fmt.Sprintf("or /%s Leech and Unzip [links/media]", bc.Leech[5]))
	}

	if len(bc.Ytdl) > 1 {
		addCmd(bc.Ytdl[0], fmt.Sprintf("or /%s Leech yt-dlp supported links", bc.Ytdl[1]))
	}

	if len(bc.Status) > 1 {
		addCmd(bc.Status[0], fmt.Sprintf("or /%s Get Bot All Status Stats Message", bc.Status[1]))
	}

	if len(bc.Stats) > 1 {
		addCmd(bc.Stats[0], fmt.Sprintf("or /%s Check Bot & System stats", bc.Stats[1]))
	}

	if len(bc.History) > 0 {
		addCmd(bc.History[0], "Your last finished tasks")
	}

	if len(bc.Cover) > 0 {
		addCmd(bc.Cover[0], "Reply to a video: send it again with your thumbnail as cover")
	}

	if len(bc.Cancel) > 0 {
		addCmd(bc.Cancel[0], "Cancel a Task of yours!")
	}

	if len(bc.CancelAll) > 0 {
		addCmd(bc.CancelAll[0], "Cancel all Tasks in whole Bots.")
	}

	if len(bc.Help) > 0 {
		addCmd(bc.Help[0], "Get detailed help about the Bot")
	}

	if len(bc.UserSetting) > 1 {
		addCmd(bc.UserSetting[0], fmt.Sprintf("or /%s User's Personal Settings (Open in PM)", bc.UserSetting[1]))
	}

	if len(bc.Imdb) > 0 {
		addCmd(bc.Imdb[0], "Search Movies/Series on IMDB.com")
	}

	if len(bc.Anime) > 1 {
		addCmd(bc.Anime[0], fmt.Sprintf("or /%s Search Animes on AniList.com", bc.Anime[1]))
	}

	if len(bc.Speedtest) > 1 {
		addCmd(bc.Speedtest[0], fmt.Sprintf("or /%s Check Server Up & Down Speed", bc.Speedtest[1]))
	}

	if len(bc.MediaInfo) > 1 {
		addCmd(bc.MediaInfo[0], fmt.Sprintf("or /%s Generate Mediainfo for Media or DL links", bc.MediaInfo[1]))
	}

	if len(bc.Ping) > 1 {
		addCmd(bc.Ping[0], fmt.Sprintf("or /%s Check Bot Ping & Latency", bc.Ping[1]))
	}

	userCount := len(cmds)
	// Owner / Sudo only, at the end; /bs and /restart last
	if len(bc.Broadcast) > 1 {
		addCmd(bc.Broadcast[0], fmt.Sprintf("or /%s Broadcast message to all users", bc.Broadcast[1]))
	}

	if len(bc.Log) > 0 {
		addCmd(bc.Log[0], "Get Bot Log (Owner/Sudo)")
	}

	if len(bc.Users) > 0 {
		addCmd(bc.Users[0], "Users, Sudo and Blacklist lists (Owner/Sudo)")
	}

	if len(bc.Authorize) > 1 {
		addCmd(bc.Authorize[0], fmt.Sprintf("or /%s Authorize a user or chat (Owner/Sudo)", bc.Authorize[1]))
	}

	if len(bc.UnAuthorize) > 1 {
		addCmd(bc.UnAuthorize[0], fmt.Sprintf("or /%s Unauthorize a user or chat (Owner/Sudo)", bc.UnAuthorize[1]))
	}

	if len(bc.Blacklist) > 1 {
		addCmd(bc.Blacklist[0], fmt.Sprintf("or /%s Blacklist a user (Owner/Sudo)", bc.Blacklist[1]))
	}

	if len(bc.BotSetting) > 1 {
		addCmd(bc.BotSetting[0], fmt.Sprintf("or /%s Bot Settings (Owner/Sudo)", bc.BotSetting[1]))
	}

	if len(bc.Restart) > 1 {
		addCmd(bc.Restart[0], fmt.Sprintf("or /%s Restart the Bot (Owner/Sudo)", bc.Restart[1]))
	}

	userCmds := cmds[:userCount]
	set := func(scope tg.BotCommandScopeClass, list []tg.BotCommand) error {
		_, err := b.rawAPI.BotsSetBotCommands(ctx, &tg.BotsSetBotCommandsRequest{Scope: scope, LangCode: "", Commands: list})
		return err
	}
	if err := set(&tg.BotCommandScopeDefault{}, userCmds); err != nil {
		log.Printf("[AZML-Go Bot Client] Failed to register bot commands with Telegram: %v", err)
		return
	}
	// the Owner and the Sudo users see also the admin commands: in their PM and in the
	// authorized groups (scopes per user, like a menu only they get)
	admins, chats := b.commandAdminsAndChats()
	// a user who is no longer Sudo (/rmsudo) gets the normal list back
	cmdAdminsMu.Lock()
	now := map[int64]bool{}
	for _, id := range admins {
		now[id] = true
	}
	for id := range cmdAdmins {
		if now[id] {
			continue
		}
		if hash := telegram.GetUserAccessHash(id); hash != 0 {
			_, _ = b.rawAPI.BotsResetBotCommands(ctx, &tg.BotsResetBotCommandsRequest{Scope: &tg.BotCommandScopePeer{Peer: &tg.InputPeerUser{UserID: id, AccessHash: hash}}})
			for _, cid := range chats {
				if cp := b.getPeerForChatID(cid); cp != nil {
					_, _ = b.rawAPI.BotsResetBotCommands(ctx, &tg.BotsResetBotCommandsRequest{Scope: &tg.BotCommandScopePeerUser{Peer: cp, UserID: &tg.InputUser{UserID: id, AccessHash: hash}}})
				}
			}
		}
	}
	cmdAdmins = now
	cmdAdminsMu.Unlock()
	n := 0
	for _, uid := range admins {
		hash := telegram.GetUserAccessHash(uid)
		if hash == 0 {
			continue // never seen: Telegram does not know him for this bot yet
		}
		user := &tg.InputUser{UserID: uid, AccessHash: hash}
		if set(&tg.BotCommandScopePeer{Peer: &tg.InputPeerUser{UserID: uid, AccessHash: hash}}, cmds) == nil {
			n++
		}
		for _, cid := range chats {
			if cp := b.getPeerForChatID(cid); cp != nil {
				_ = set(&tg.BotCommandScopePeerUser{Peer: cp, UserID: user}, cmds)
			}
		}
	}
	log.Printf("[AZML-Go Bot Client] Successfully registered %d commands in bot command list (%d more for %d Owner/Sudo)", len(userCmds), len(cmds)-len(userCmds), n)
}

var (
	cmdAdminsMu sync.Mutex
	cmdAdmins   = map[int64]bool{} // who got the admin command list (this run)
)

// commandAdminsAndChats: the Owner and Sudo users, and the authorized groups (config and /authorize).
func (b *BotApp) commandAdminsAndChats() (admins, chats []int64) {
	cfg := config.Get()
	seen := map[int64]bool{}
	addA := func(id int64) {
		if id > 0 && !seen[id] {
			seen[id] = true
			admins = append(admins, id)
		}
	}
	addA(cfg.OwnerID)
	for _, id := range cfg.SudoUsers {
		addA(id)
	}
	seenC := map[int64]bool{}
	addC := func(id int64) {
		if id < 0 && !seenC[id] {
			seenC[id] = true
			chats = append(chats, id)
		}
	}
	for _, id := range cfg.AuthorizedChats {
		addC(id)
	}
	if b.userMap != nil {
		for _, u := range b.userMap.GetAllUsers() {
			if u == nil {
				continue
			}
			if u.IsSudo {
				addA(u.UserID)
			}
			if u.IsAuth {
				addC(u.UserID)
			}
		}
	}
	return admins, chats
}

func (b *BotApp) cacheAccessHash(userID, hash int64) {
	if hash == 0 {
		return
	}
	accessHashMu.Lock()
	userAccessHashes[userID] = hash
	accessHashMu.Unlock()
	telegram.CacheUserAccessHash(userID, hash)
}

func (b *BotApp) cacheChannelAccessHash(channelID, hash int64) {
	if hash == 0 {
		return
	}
	accessHashMu.Lock()
	channelAccessHashes[channelID] = hash
	accessHashMu.Unlock()
	telegram.CacheChannelAccessHash(channelID, hash)
}

func (b *BotApp) getPeerWithEntities(p tg.PeerClass, entities tg.Entities) tg.InputPeerClass {
	switch v := p.(type) {
	case *tg.PeerUser:
		if u, ok := entities.Users[v.UserID]; ok && u.AccessHash != 0 {
			b.cacheAccessHash(u.ID, u.AccessHash)
			return u.AsInputPeer()
		}
		accessHashMu.RLock()
		hash := userAccessHashes[v.UserID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetUserAccessHash(v.UserID)
		}
		return &tg.InputPeerUser{UserID: v.UserID, AccessHash: hash}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: v.ChatID}
	case *tg.PeerChannel:
		if ch, ok := entities.Channels[v.ChannelID]; ok && ch.AccessHash != 0 && !ch.Min {
			b.cacheChannelAccessHash(ch.ID, ch.AccessHash)
			return ch.AsInputPeer()
		}
		accessHashMu.RLock()
		hash := channelAccessHashes[v.ChannelID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetChannelAccessHash(v.ChannelID)
		}
		return &tg.InputPeerChannel{ChannelID: v.ChannelID, AccessHash: hash}
	}
	return nil
}

func getPeer(p tg.PeerClass) tg.InputPeerClass {
	switch v := p.(type) {
	case *tg.PeerUser:
		accessHashMu.RLock()
		hash := userAccessHashes[v.UserID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetUserAccessHash(v.UserID)
		}
		return &tg.InputPeerUser{UserID: v.UserID, AccessHash: hash}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: v.ChatID}
	case *tg.PeerChannel:
		accessHashMu.RLock()
		hash := channelAccessHashes[v.ChannelID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetChannelAccessHash(v.ChannelID)
		}
		return &tg.InputPeerChannel{ChannelID: v.ChannelID, AccessHash: hash}
	}
	return nil
}

// botAPIChatID: the chat id as the Bot API / Pyrogram write it (-100… for groups and channels).
func botAPIChatID(p tg.PeerClass) int64 {
	switch v := p.(type) {
	case *tg.PeerUser:
		return v.UserID
	case *tg.PeerChat:
		return -v.ChatID
	case *tg.PeerChannel:
		return -1000000000000 - v.ChannelID
	}
	return 0
}

func getPeerID(p tg.PeerClass) int64 {
	switch v := p.(type) {
	case *tg.PeerUser:
		return v.UserID
	case *tg.PeerChat:
		return v.ChatID
	case *tg.PeerChannel:
		return v.ChannelID
	}
	return 0
}

func (b *BotApp) getPeerForChatID(chatID int64) tg.InputPeerClass {
	if chatID > 0 {
		accessHashMu.RLock()
		hash := userAccessHashes[chatID]
		accessHashMu.RUnlock()
		if hash == 0 {
			hash = telegram.GetUserAccessHash(chatID)
		}
		return &tg.InputPeerUser{UserID: chatID, AccessHash: hash}
	}
	rawChanID := pool.RawChannelID(chatID)
	accessHashMu.RLock()
	hash := channelAccessHashes[rawChanID]
	accessHashMu.RUnlock()
	if hash == 0 {
		hash = telegram.GetChannelAccessHash(rawChanID)
	}
	return &tg.InputPeerChannel{ChannelID: rawChanID, AccessHash: hash}
}

func (b *BotApp) handleIncomingMedia(ctx context.Context, peer tg.InputPeerClass, m *tg.Message, senderID, chatID int64) bool {
	// Case 1: Document media
	if docMedia, ok := m.Media.(*tg.MessageMediaDocument); ok {
		if doc, ok := docMedia.Document.(*tg.Document); ok {
			fileName := ""
			for _, attr := range doc.Attributes {
				if fnAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
					fileName = fnAttr.FileName
					break
				}
			}
			lowerName := strings.ToLower(fileName)

			// 0. /us → Import/Export Settings → Import: the azml_user_settings.json of Export
			if sess, ok := ext.GetPendingSettingSession(senderID, chatID); ok && sess != nil && sess.Key == "importset" {
				if b.userSettingsImporter == nil {
					return false
				}
				ext.ClearPendingSettingSession(senderID)
				_ = b.sender.DeleteMessage(ctx, peer, []int{m.ID}) // it has the user's keys
				if doc.Size > 4<<20 {
					ext.SetSettingNote(senderID, "× <i>The file is too big.</i>")
					if b.settingsUpdateHandler != nil {
						b.settingsUpdateHandler(ctx, peer, sess.PromptMsgID, b.userMap.GetOrCreate(senderID), "importset")
					}
					return true
				}
				var buf bytes.Buffer
				if _, err := tdDownloader.NewDownloader().Download(b.rawAPI, doc.AsInputDocumentFileLocation("")).Stream(ctx, &buf); err != nil {
					ext.SetSettingNote(senderID, "× <i>Download failed: "+telegram.EscapeHTML(err.Error())+"</i>")
					if b.settingsUpdateHandler != nil {
						b.settingsUpdateHandler(ctx, peer, sess.PromptMsgID, b.userMap.GetOrCreate(senderID), "importset")
					}
					return true
				}
				b.userSettingsImporter(ctx, peer, sess.PromptMsgID, senderID, buf.Bytes())
				return true
			}

			// 1. the user's own cookies.txt from /us → Universal → YT-DLP Cookies
			if sess, ok := ext.GetPendingSettingSession(senderID, chatID); ok && sess != nil && sess.Key == "ycookies" {
				if !strings.HasSuffix(lowerName, ".txt") || doc.Size > 2<<20 {
					_, _ = b.sender.SendMessage(ctx, peer, "Send a <code>cookies.txt</code> file (up to 2MB).", m.ID, nil)
					return true
				}
				ext.ClearPendingSettingSession(senderID)
				var buf bytes.Buffer
				if _, err := tdDownloader.NewDownloader().Download(b.rawAPI, doc.AsInputDocumentFileLocation("")).Stream(ctx, &buf); err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading cookies: %v", err), m.ID, nil)
					return true
				}
				u := b.userMap.GetOrCreate(senderID)
				u.YtCookies = buf.String()
				_ = ext.DB.SaveUser(u)
				_ = b.sender.DeleteMessage(ctx, peer, []int{m.ID})
				if sess.PromptMsgID > 0 && b.settingsUpdateHandler != nil {
					b.settingsUpdateHandler(ctx, peer, sess.PromptMsgID, u, "ycookies")
				}
				return true
			}

			// 1A. Private files for Owner / Sudo
			switch lowerName {
			case "token.pickle":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>token.pickle</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "token.pickle")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>token.pickle</b>: %v", err), m.ID, nil)
					return true
				}
				// Reload Google Drive OAuth credentials immediately
				gClient := downloader.GetGDriveClient()
				if gClient != nil {
					_ = gClient.LoadCredentials("token.pickle")
				}
				_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>token.pickle</b> received and saved! Google Drive OAuth credentials successfully reloaded.", m.ID, nil)
				return true

			case "config.env":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>config.env</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "config.env")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>config.env</b>: %v", err), m.ID, nil)
					return true
				}
				_, _ = config.LoadConfig("config.env")
				_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>config.env</b> received and saved! Configurations reloaded.", m.ID, nil)
				return true

			case "cookies.txt":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>cookies.txt</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "cookies.txt")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>cookies.txt</b>: %v", err), m.ID, nil)
					return true
				}
				_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>cookies.txt</b> received and saved for yt-dlp & aria2c!", m.ID, nil)
				return true

			case "accounts.zip":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>accounts.zip</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "accounts.zip")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>accounts.zip</b>: %v", err), m.ID, nil)
					return true
				}
				_, _ = b.sender.SendMessage(ctx, peer, "✓ <b>accounts.zip</b> received and saved successfully!", m.ID, nil)
				return true

			case "shorteners.txt":
				if !telegram.IsOwner(senderID) && !telegram.IsSudo(senderID, b.userMap) {
					_, _ = b.sender.SendMessage(ctx, peer, "Only Owner/Sudo can upload <code>shorteners.txt</code>!", m.ID, nil)
					return true
				}
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc == nil {
					return false
				}
				_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, "shorteners.txt")
				if err != nil {
					_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("× Error downloading <b>shorteners.txt</b>: %v", err), m.ID, nil)
					return true
				}
				ext.LoadShorteners()
				_, _ = b.sender.SendMessage(ctx, peer, fmt.Sprintf("✓ <b>shorteners.txt</b> received and saved! %d shortener(s) loaded.", len(ext.GetShorteners())), m.ID, nil)
				return true
			}

			// 1B. Custom Thumbnail document
			isImageDoc := strings.HasSuffix(lowerName, ".jpg") || strings.HasSuffix(lowerName, ".jpeg") || strings.HasSuffix(lowerName, ".png") || strings.HasSuffix(lowerName, ".webp") || strings.HasPrefix(doc.MimeType, "image/")
			if isImageDoc {
				caption := strings.ToLower(strings.TrimSpace(m.Message))
				isThumbCmd := telegram.IsThumbCommand(caption, b.botUsername)
				sess, isPending := ext.GetPendingThumbSession(senderID, chatID)
				if isThumbCmd || (isPending && sess != nil && !strings.HasPrefix(caption, "/")) {
					ext.ClearPendingThumbSession(senderID)
					_ = os.MkdirAll("Thumbnails", 0755)
					destThumb := fmt.Sprintf("Thumbnails/%d.jpg", senderID)
					dl := tdDownloader.NewDownloader()
					loc := doc.AsInputDocumentFileLocation("")
					if loc != nil {
						_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, destThumb)
						if err == nil {
							u := b.userMap.GetOrCreate(senderID)
							u.CustomThumb = destThumb
							u.ThumbData = ext.ThumbBytes(destThumb)
							_ = ext.DB.SaveUser(u)

							b.thumbSaved(ctx, peer, chatID, m.ID, sess, u)
							return true
						}
					}
				}
				return false
			}

			// 1C. Torrent file document
			if strings.HasSuffix(lowerName, ".torrent") {
				cfg := config.Get()
				_ = os.MkdirAll(cfg.DownloadDir, 0755)
				torrentPath := filepath.Join(cfg.DownloadDir, fmt.Sprintf("%d_%s.torrent", senderID, ext.RandomHex(4)))
				dl := tdDownloader.NewDownloader()
				loc := doc.AsInputDocumentFileLocation("")
				if loc != nil {
					_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, torrentPath)
					if err == nil && b.leechHandler != nil {
						msgCtx := &types.MessageContext{
							ChatID:     chatID,
							MessageID:  m.ID,
							UserID:     senderID,
							SenderName: torrentSenderName(b.userMap, senderID),
							Text:       fmt.Sprintf("/leech %s", torrentPath),
						}
						b.leechHandler(ctx, peer, msgCtx, false, false)
						return true
					}
				}
			}
		}
	}

	// Case 2: Photo media (Custom thumbnail for user)
	if photoMedia, ok := m.Media.(*tg.MessageMediaPhoto); ok {
		caption := strings.ToLower(strings.TrimSpace(m.Message))
		isThumbCmd := telegram.IsThumbCommand(caption, b.botUsername)
		sess, isPending := ext.GetPendingThumbSession(senderID, chatID)
		if isThumbCmd || (isPending && sess != nil && !strings.HasPrefix(caption, "/")) {
			ext.ClearPendingThumbSession(senderID)
			if photo, ok := photoMedia.Photo.(*tg.Photo); ok {
				_ = os.MkdirAll("Thumbnails", 0755)
				destThumb := fmt.Sprintf("Thumbnails/%d.jpg", senderID)
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
					_, err := dl.Download(b.rawAPI, loc).ToPath(ctx, destThumb)
					if err == nil {
						u := b.userMap.GetOrCreate(senderID)
						u.CustomThumb = destThumb
						u.ThumbData = ext.ThumbBytes(destThumb)
						_ = ext.DB.SaveUser(u)

						b.thumbSaved(ctx, peer, chatID, m.ID, sess, u)
						return true
					}
				}
			}
		}
		return false
	}

	return false
}

func (b *BotApp) applyUserSettingValue(ctx context.Context, peer tg.InputPeerClass, u *types.UserSettings, key, val string, promptMsgID int) {
	if strings.HasPrefix(key, "vt_") { // text for the -vt menu, not /us
		if b.vtInput != nil {
			b.vtInput(u.UserID, key, val)
		}
		return
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return
	}
	var fieldName string
	switch key {
	case "ytopt":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.YtOpt = ""
			val = "Cleared"
		} else {
			u.YtOpt = val
		}
		fieldName = "YT-DLP Options"
	case "ycookies":
		// cookies come as a file (handled with the incoming documents)
		return
	case "megalogin":
		parts := strings.Fields(val)
		if len(parts) != 2 {
			ext.SetSettingNote(u.UserID, "× <i>Send the email and the password separated by a space.</i>")
			break
		}
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := downloader.MegaLoginCheck(cctx, parts[0], parts[1])
		cancel()
		if err != nil {
			ext.SetSettingNote(u.UserID, "× <i>"+telegram.EscapeHTML(err.Error())+"</i>")
			break
		}
		u.MegaEmail, u.MegaPass = parts[0], parts[1]
		ext.SetSettingNote(u.UserID, "✓ <i>Logged in to MEGA.</i>")
		fieldName, val = "Mega Login", parts[0]
	case "ffcmds":
		name, args, ok := strings.Cut(val, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			ext.SetSettingNote(u.UserID, "× <i>Format:</i> <code>name: ffmpeg args</code>")
			break
		}
		cmds := map[string]string{}
		for k, v := range u.FFmpegCommands {
			cmds[k] = v
		}
		if args = strings.TrimSpace(args); args == "" {
			delete(cmds, name)
		} else {
			cmds[name] = args
		}
		u.FFmpegCommands = cmds
		fieldName = "FFmpeg Cmds"
	case "lexcluded":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.ExcludedExts = ""
			val = "Cleared"
		} else {
			var exts []string
			for _, e := range strings.Fields(strings.ReplaceAll(val, ",", " ")) {
				e = strings.ToLower(strings.TrimSpace(e))
				if !strings.HasPrefix(e, ".") {
					e = "." + e
				}
				exts = append(exts, e)
			}
			u.ExcludedExts = strings.Join(exts, " ")
			val = u.ExcludedExts
		}
		fieldName = "Excluded Extensions"
	case "lprefix":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.Prefix = ""
			val = "Cleared"
		} else {
			u.Prefix = val
		}
		fieldName = "Leech Prefix"
	case "lsuffix":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.Suffix = ""
			val = "Cleared"
		} else {
			u.Suffix = val
		}
		fieldName = "Leech Suffix"
	case "lcaption":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") || strings.EqualFold(val, "default") {
			u.Caption = ""
			val = "Default"
		} else {
			u.Caption = val
		}
		fieldName = "Leech Caption"
	case "lremname_auto":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.RemnameAuto = ""
			val = "Cleared"
		} else {
			u.RemnameAuto = val
		}
		fieldName = "AutoRename Template"
	case "lremname_regex":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.RemnameRegex = ""
			val = "Cleared"
		} else {
			u.RemnameRegex = val
		}
		fieldName = "Regex Remname"
	case "ldump":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			u.DumpChatID = 0
			val = "Cleared"
		} else {
			id, _ := strconv.ParseInt(val, 10, 64)
			u.DumpChatID = id
		}
		fieldName = "Leech Dump Channel"
	case "lmeta":
		if strings.EqualFold(val, "clear") || strings.EqualFold(val, "none") {
			if u.Metadata != nil {
				delete(u.Metadata, "all")
			}
			val = "Cleared"
		} else {
			if u.Metadata == nil {
				u.Metadata = make(map[string]string)
			}
			u.Metadata["all"] = val
		}
		fieldName = "Leech Metadata"
	case "split_size":
		sizeStr := strings.ToUpper(val)
		maxSplit := config.GetMaxSplitSize()
		var bytes int64
		if strings.HasSuffix(sizeStr, "GB") {
			f, _ := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "GB"), 64)
			bytes = int64(f * 1024 * 1024 * 1024)
		} else if strings.HasSuffix(sizeStr, "MB") {
			f, _ := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "MB"), 64)
			bytes = int64(f * 1024 * 1024)
		} else {
			bytes, _ = strconv.ParseInt(sizeStr, 10, 64)
		}
		if bytes > maxSplit || bytes <= 0 {
			bytes = maxSplit
		}
		u.SplitSize = bytes
		fieldName = "Leech Split Size"
		val = ext.GetReadableFileSize(u.SplitSize)
	default:
		// ddlkey_<host>: the user's own API key of a /mirror host
		if hostID := strings.TrimPrefix(key, "ddlkey_"); hostID != key {
			keys := maps.Clone(u.DDLKeys) // a new map: running tasks may be reading the old one
			if keys == nil {
				keys = map[string]string{}
			}
			keys[hostID] = val
			u.DDLKeys = keys
			fieldName = "API Key"
			val = "Saved"
		}
	}

	_ = ext.DB.SaveUser(u)
	if promptMsgID > 0 && b.settingsUpdateHandler != nil {
		b.settingsUpdateHandler(ctx, peer, promptMsgID, u, key)
	} else {
		confirmText := fmt.Sprintf("✓ <b>%s</b> updated to: <code>%s</code>", fieldName, val)
		_, _ = b.sender.SendMessage(ctx, peer, confirmText, 0, nil)
	}
}

func torrentSenderName(um *types.SafeUserMap, id int64) string {
	if u := um.Get(id); u != nil && u.Name != "" {
		return u.Name
	}
	return "User"
}

// notifyIncompleteTasks: INCOMPLETE_TASK_NOTIFIER, the tasks the restart stopped, per chat.
func (b *BotApp) notifyIncompleteTasks(ctx context.Context) {
	tasks := ext.DB.TakeRunningTasks()
	if len(tasks) == 0 || !config.Get().IncompleteTaskNotifier || b.sender == nil {
		return
	}
	byChat := map[int64][]ext.RunningTask{}
	for _, t := range tasks {
		byChat[t.ChatID] = append(byChat[t.ChatID], t)
	}
	for chatID, list := range byChat {
		peer := b.getPeerForChatID(chatID)
		if peer == nil {
			continue
		}
		var sb strings.Builder
		sb.WriteString("㊂ <b><u>Restarted :</u></b>\n\n<blockquote>These tasks were stopped by the restart, send them again:\n")
		for i, t := range list {
			name := telegram.EscapeHTML(t.Name)
			if link := ""; chatID < 0 {
				if id := strings.TrimPrefix(fmt.Sprint(chatID), "-100"); id != fmt.Sprint(chatID) && t.MessageID > 0 {
					link = fmt.Sprintf("https://t.me/c/%s/%d", id, t.MessageID)
					name = fmt.Sprintf("<a href=\"%s\">%s</a>", link, name)
				}
			}
			sb.WriteString(fmt.Sprintf("%d. %s — <a href=\"tg://user?id=%d\">%s</a>\n", i+1, name, t.UserID, telegram.EscapeHTML(t.User)))
		}
		sb.WriteString("</blockquote>")
		_, _ = b.sender.SendMessage(ctx, peer, sb.String(), 0, nil)
	}
}

// thumbSaved: the photo / image sent for the thumbnail goes; the /t prompt is edited to "saved"
// (and goes with the command), the /us menu is refreshed, else a short notice.
func (b *BotApp) thumbSaved(ctx context.Context, peer tg.InputPeerClass, chatID int64, photoMsgID int, sess *ext.PendingThumbSession, u *types.UserSettings) {
	_ = b.sender.DeleteMessage(ctx, peer, []int{photoMsgID}) // the photo itself goes at once, like the Python bot
	if sess != nil && sess.CmdMsgID > 0 {
		b.finishThumbPrompt(peer, sess, "✓ <b>Custom thumbnail saved!</b> <i>It will be attached to all your future leeched files.</i>")
		return
	}
	if sess != nil && sess.PromptMsgID > 0 && b.settingsUpdateHandler != nil {
		settingsPeer := peer
		if sess.ChatID != 0 && !ext.SameChat(sess.ChatID, chatID) {
			if p := b.getPeerForChatID(sess.ChatID); p != nil {
				settingsPeer = p
			}
		}
		b.settingsUpdateHandler(ctx, settingsPeer, sess.PromptMsgID, u, "thumb")
		return
	}
	if id, _ := b.sender.SendMessage(ctx, peer, "✓ <b>Custom thumbnail saved!</b> It will be attached to all your future leeched files.", 0, nil); id > 0 {
		b.sender.AutoDeleteMessage(peer, id, 5) // a short notice
	}
}

// finishThumbPrompt: the /t prompt shows the result, then it and the /t command go.
func (b *BotApp) finishThumbPrompt(peer tg.InputPeerClass, sess *ext.PendingThumbSession, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = b.sender.EditMessage(ctx, peer, sess.PromptMsgID, "㊂ <b><u>Custom Thumbnail :</u></b>\n\n"+text, nil)
	ids := []int{sess.PromptMsgID, sess.CmdMsgID}
	go func() {
		time.Sleep(5 * time.Second)
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dcancel()
		_ = b.sender.DeleteMessage(dctx, peer, ids)
	}()
}
