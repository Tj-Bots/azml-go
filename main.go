package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"azml-go/bot"
	"azml-go/config"
	"azml-go/helper/downloader"
	"azml-go/helper/ext"
	"azml-go/helper/pool"
	"azml-go/helper/telegram"
	"azml-go/modules"
	"azml-go/web"

	"github.com/gotd/td/tg"
)

type istLogWriter struct {
	out io.Writer
	loc *time.Location
}

// botTokenRe: a bot token inside a log line (e.g. a Bot API URL in an error) is hidden.
var botTokenRe = regexp.MustCompile(`\d{6,}:[A-Za-z0-9_-]{30,}`)

func (w *istLogWriter) Write(p []byte) (n int, err error) {
	now := time.Now().In(w.loc).Format("2006/01/02 15:04:05 IST")
	line := botTokenRe.ReplaceAll(p, []byte("<bot-token>"))
	if _, err := fmt.Fprintf(w.out, "%s %s", now, line); err != nil {
		return 0, err
	}
	return len(p), nil
}

func setupISTLogging() {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.FixedZone("IST", 5*3600+30*60)
	}
	time.Local = loc
	_ = os.Setenv("TZ", "Asia/Kolkata")

	log.SetFlags(0)
	log.SetOutput(&istLogWriter{
		out: io.MultiWriter(os.Stdout, ext.LogBuffer),
		loc: loc,
	})
}

func setupEnvironment() {
	setupISTLogging()

	currentPath := os.Getenv("PATH")
	aptBinDirs := []string{"/app/.apt/usr/bin", "/app/.apt/bin", "/usr/local/bin", "/usr/bin", "/bin"}
	for _, dir := range aptBinDirs {
		if !strings.Contains(currentPath, dir) {
			currentPath = dir + string(os.PathListSeparator) + currentPath
		}
	}
	_ = os.Setenv("PATH", currentPath)

	currentLD := os.Getenv("LD_LIBRARY_PATH")
	aptLibDirs := []string{"/app/.apt/usr/lib", "/app/.apt/usr/lib/x86_64-linux-gnu", "/app/.apt/lib/x86_64-linux-gnu"}
	for _, dir := range aptLibDirs {
		if !strings.Contains(currentLD, dir) {
			currentLD = dir + string(os.PathListSeparator) + currentLD
		}
	}
	_ = os.Setenv("LD_LIBRARY_PATH", currentLD)
}

func main() {
	// the deploy variables (Heroku config vars / docker env) before config.env is read
	deployEnv := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			deployEnv[k] = v
		}
	}
	setupEnvironment()

	// Proactive container memory ceiling - 400MB soft target for Heroku 512MB dynos
	debug.SetMemoryLimit(400 * 1024 * 1024)

	// Immediately reclaim and release freed memory back to the OS
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			debug.FreeOSMemory()
		}
	}()

	log.Println("Starting AZML-Go [Dynamic Auto Memory Engine]...")

	// Load configuration
	cfg, err := config.LoadConfig("config.env")
	if err != nil {
		log.Printf("Warning: error loading config: %v", err)
	}

	// Automated Storage Cleanup: clean stale downloads on boot and start periodic cleaner
	ext.CleanDownloadsDir()
	ext.StartPeriodicStorageCleaner()

	// Initialize Database (MongoDB or local JSON fallback)
	ext.InitDB()
	if db := ext.GetDB(); db != nil {
		// access hashes saved in the database: channels and users resolve right after a restart
		peers := db.LoadPeers()
		telegram.LoadPeers(peers)
		telegram.SetPeerStore(db.SavePeer)
		log.Printf("[DB] Loaded %d saved peers", len(peers))
		// like the Python bot (deployConfig): the /bs values apply while the deploy variables
		// stay the same; when the Heroku config vars change, the new deploy wins and is saved
		var snap []string
		for _, k := range config.AllKeys() {
			if v := deployEnv[k]; v != "" {
				snap = append(snap, k+"="+v)
			}
		}
		snapshot := strings.Join(snap, "\n")
		oldSnapshot, _ := db.GetBotSetting("_DEPLOY_CONFIG")
		if oldSnapshot != "" && oldSnapshot != snapshot {
			log.Printf("[DB] Deploy variables changed: using them, the /bs values are not restored")
			_ = db.SaveBotSetting("_DEPLOY_CONFIG", snapshot)
			for _, k := range config.AllKeys() { // the old /bs values must not come back next time
				if !config.IsCoreKey(k) && !strings.HasPrefix(k, "UPSTREAM_") {
					_ = db.DeleteBotSetting(k)
				}
			}
			// UPSTREAM_* stay in the database, but a new deploy value replaces the old one there:
			// else the next start (after BootUpdate) restores the old branch over the deploy one
			for _, k := range []string{"UPSTREAM_REPO", "UPSTREAM_BRANCH"} {
				if v := deployEnv[k]; v != "" {
					_ = db.SaveBotSetting(k, v)
				}
			}
		} else if dbSettings, err := db.LoadAllBotSettings(); err == nil && len(dbSettings) > 0 {
			if oldSnapshot == "" {
				_ = db.SaveBotSetting("_DEPLOY_CONFIG", snapshot)
			}
			n := 0
			for k, v := range dbSettings {
				// the core variables come only from the deploy; any other value (also an empty
				// one from Reset) wins over it, like the Python bot
				if strings.HasPrefix(k, "_") || config.IsCoreKey(k) {
					continue
				}
				_ = config.UpdateConfigVar(k, v)
				n++
			}
			log.Printf("[DB] Restored %d persistent setting(s) from database", n)
			// rebuild the config so the vars edited in /bs that are read only at start apply too
			if reloaded, rErr := config.LoadConfig("config.env"); rErr == nil && reloaded != nil {
				cfg = reloaded
			}
		} else if oldSnapshot == "" {
			_ = db.SaveBotSetting("_DEPLOY_CONFIG", snapshot)
		}
		// TIMEZONE from /bs
		if loc, lErr := time.LoadLocation(cfg.Timezone); lErr == nil && cfg.Timezone != "" {
			time.Local = loc
		}
		// Ensure default repository and branch are persisted in DB
		if repo, _ := db.GetBotSetting("UPSTREAM_REPO"); repo == "" {
			_ = db.SaveBotSetting("UPSTREAM_REPO", "https://github.com/Tj-Bots/azml-go")
		}
		if branch, _ := db.GetBotSetting("UPSTREAM_BRANCH"); branch == "" {
			_ = db.SaveBotSetting("UPSTREAM_BRANCH", ext.DefaultUpstreamBranch)
		}
	}
	// Initialize commands with CMD_SUFFIX (after the settings from the database)
	telegram.InitCommands()

	config.SetOnConfigVarUpdated(func(key, val string) {
		if db := ext.GetDB(); db != nil {
			if err := db.SaveBotSetting(key, val); err != nil {
				log.Printf("[DB] Error saving setting %s to DB: %v", key, err)
			}
		}
	})
	cfg = config.Get()
	ext.LoadShorteners()

	// Ensure Aria2c is started with 16 connections
	ariaClient := downloader.GetAria2Client()
	_ = ariaClient.EnsureStarted()

	// Start Base URL Web Selector Server
	web.StartWebServer()

	// Like update.py of the Python bot: pull UPSTREAM_REPO and run its latest code on every
	// start. After the web server (Heroku wants $PORT within 60s) and before the bot takes tasks.
	ext.BootUpdate()

	// Create Bot Application with gotd MTProto client
	app := bot.NewBotApp()

	// Initialize Multi-Bot SessionPool if worker bot tokens are configured
	var sessPool *pool.SessionPool
	if len(cfg.WorkerBotTokens) > 0 {
		sessionsDir := filepath.Join(cfg.DownloadDir, "gotd_sessions")
		_ = os.MkdirAll(sessionsDir, 0777)
		sessPool = pool.NewSessionPool(sessionsDir)
		log.Printf("[Multi-Bot Pool] Initialized SessionPool with %d worker bot tokens. Warming up...", len(cfg.WorkerBotTokens))
		sessPool.WarmupTokens(cfg.TelegramAPI, cfg.TelegramHash, cfg.WorkerBotTokens, cfg.DumpChatID)
		app.SetSessionPool(sessPool)
	} else {
		log.Println("[Multi-Bot Pool] No BOT_TOKENS / MULTI_TOKENS configured (Single Bot Mode active)")
	}

	// Instantiate and wire modules
	sender := app.GetSender()
	uploader := app.GetUploader()
	userMap := app.GetUserMap()
	startTime := app.GetStartTime()
	ext.SetBotStartTime(startTime)

	if sessPool != nil {
		uploader.SetSessionPool(sessPool, cfg.DumpChatID)
	}

	statusMod := modules.NewStatusModule(sender)
	leechMod := modules.NewLeechModule(app.GetRawAPI(), app.GetUserRawAPI(), sender, uploader, userMap, statusMod, sessPool, cfg.DumpChatID)
	ytdlMod := modules.NewYtdlModule(sender, leechMod, userMap)
	cancelMod := modules.NewCancelModule(sender, userMap, statusMod)
	settingsMod := modules.NewSettingsModule(app.GetRawAPI(), sender, userMap)
	botSetMod := modules.NewBotSettingsModule(sender, userMap)
	botSetMod.SetUserSessionReloader(app.ReloadUserSession)
	botSetMod.SetUploader(uploader)
	botSetMod.SetLeechModule(leechMod)
	app.SetUserRawAPIUpdater(leechMod.SetUserRawAPI)

	authMod := modules.NewAuthorizeModule(sender, userMap)
	helpMod := modules.NewHelpModule(sender, userMap, startTime)
	speedMod := modules.NewSpeedtestModule(sender)
	miMod := modules.NewMediaInfoModule(sender)
	miMod.SetRawAPI(app.GetRawAPI)
	bcMod := modules.NewBroadcastModule(sender, userMap)
	imdbMod := modules.NewImdbModule(sender)
	animeMod := modules.NewAnimeModule(sender)

	app.SetLeechHandler(leechMod.HandleLeech)
	app.SetTaskExtras(leechMod.HandleHistory, leechMod.HandleRetryCallback)
	app.SetSelectHandler(leechMod.HandleSelectCallback)
	app.SetCmdHelpHandler(leechMod.HandleHelpCallback)
	app.SetToolHandlers(leechMod.HandleCover, modules.HandleShell(sender))
	app.SetSaveHandler(leechMod.HandleSaveCallback)
	app.SetVTHandlers(leechMod.HandleVTCallback, leechMod.HandleVTInput)
	app.SetYtdlHandler(ytdlMod.HandleYtdl, ytdlMod.HandleYtdlCallback)
	app.SetGateHandler(leechMod.TaskGate)
	app.SetStatusHandlers(statusMod.HandleStatus, statusMod.HandleCallback)
	app.SetCancelHandlers(cancelMod.HandleCancel, cancelMod.HandleCancelAll, cancelMod.HandleCallback)
	app.SetSettingsHandlers(settingsMod.HandleSettings, settingsMod.HandleCallback, settingsMod.HandleSettingUpdated)
	app.SetAutoRenameHandler(settingsMod.HandleAutoRename)
	app.SetBotSettingsHandlers(botSetMod.HandleBotSettings, botSetMod.HandleCallback)
	app.SetBotSettingsInputHandlers(botSetMod.HandleSessionValue, botSetMod.HandleSessionCancelled)
	app.SetSettingsImporter(botSetMod.ImportSettings)
	app.SetUserSettingsImporter(settingsMod.ImportUserSettings)
	app.SetAuthorizeHandlers(
		authMod.HandleAuthorize,
		authMod.HandleUnauthorize,
		authMod.HandleAddSudo,
		authMod.HandleRmSudo,
		authMod.HandleAddPremium,
		authMod.HandleRmPremium,
		authMod.HandleCheckPremium,
	)
	app.SetAdminHandlers(authMod.HandleBlacklist, authMod.HandleRmBlacklist, authMod.HandleUsers)
	app.SetHelpHandlers(
		helpMod.HandleStart,
		helpMod.HandleHelp,
		helpMod.HandlePing,
		helpMod.HandleStats,
		helpMod.HandleLog,
		helpMod.HandleRestart,
		helpMod.HandleStatsCallback,
	)
	app.SetExtraHandlers(
		speedMod.HandleSpeedtest,
		miMod.HandleMediaInfo,
		bcMod.HandleBroadcast,
		imdbMod.HandleImdb,
		animeMod.HandleAnime,
	)
	app.SetSelectHandlers(
		func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
			modules.HandleMegaSelectCallback(ctx, app.GetRawAPI(), sender, peer, queryID, msgID, data, fromUID)
		},
		func(ctx context.Context, peer tg.InputPeerClass, queryID int64, msgID int, data string, fromUID int64) {
			modules.HandleGDriveSelectCallback(ctx, app.GetRawAPI(), sender, peer, queryID, msgID, data, fromUID)
		},
	)

	// Context with graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	defer func() {
		if sessPool != nil {
			log.Println("[Multi-Bot Pool] Stopping all worker bot sessions...")
			sessPool.StopAll()
		}
	}()

	if err := app.Start(ctx); err != nil && err != context.Canceled {
		log.Fatalf("Fatal error running AZML-Go: %v", err)
	}
}
