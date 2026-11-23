package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	BotToken                  string
	OwnerID                   int64
	OwnerUsername             string
	TelegramAPI               int
	TelegramHash              string
	UserSessionString         string
	DatabaseURL               string
	DownloadDir               string
	CmdSuffix                 string
	AuthorizedChats           []int64
	SudoUsers                 []int64
	BlacklistUsers            []int64
	StatusLimit               int
	StatusUpdateInterval      int
	AutoDeleteMessageDuration int
	LeechSplitSize            int64
	EqualSplits               bool
	AsDocument                bool
	MediaGroup                bool
	BotPM                     bool
	CapFont                   string
	LeechFilenamePrefix       string
	LeechFilenameSuffix       string
	LeechFilenameCaption      string
	LeechFilenameRemname      string
	LeechFilenameRemnameAuto  string
	LeechFilenameRemnameRegex string
	LeechLogID                int64
	LinksLogID                int64
	TorrentTimeout            int
	Aria2Port                 int
	BaseURL                   string
	BaseURLPort               int
	MegaEmail                 string
	MegaPassword              string
	TokenTimeout              int64
	LoginPass                 string
	Shortener                 string
	ShortenerAPI              string
	ShortenerEnabled          bool
	TokenInPM                 bool
	ScreenshotsMode           bool
	FsubIDs                   string
	MirrorLogID               int64
	GDriveWorker              string
	MediaStore                bool
	IncompleteTaskNotifier    bool
	DisableVT                 bool
	VtCustomCmds              string
	BotMaxTasks               int
	UserMaxTasks              int
	PremiumMaxTasks           int // tasks a premium user may run at once (0 = no limit)
	RealDebridAPI             string
	DebridLinkAPI             string
	SafeMode                  bool
	PmAccess                  bool
	SetCommands               bool
	WebPincode                bool
	YtDlpOptions              string
	UserTasksLimit            int
	PlaylistLimit             int
	ExtensionFilter           string
	GDriveID                  string
	IndexURL                  string
	IsTeamDrive               bool
	StopDuplicate             bool
	DisableDriveLink          bool
	DefaultUpload             string
	RclonePath                string
	RcloneFlags               string
	ExcepChats                string
	DeleteLinks               bool
	RichMessages              bool
	CleanLogMsg               bool
	UserTimeInterval          int
	DisableTorrents           bool
	ShowMediaInfo             bool
	SaveMsg                   bool
	SourceLink                bool
	Metadata                  string
	TitleName                 string
	AuthorName                string
	AuthorURL                 string
	BotTheme                  string
	ShowExtraCmds             bool
	Timezone                  string
	EnableFFmpegCmds          bool
	FFmpegEncoding            bool
	TMDBAccessToken           string
	AutoThumbnail             bool
	CoverImage                string
	DDLAPI                    string
	QueueAll                  int
	QueueDownload             int
	QueueUpload               int
	LeechLimit                int64 // GB
	TorrentLimit              int64 // GB
	DirectLimit               int64 // GB
	YtdlpLimit                int64 // GB
	MegaLimit                 int64 // GB
	ArchiveLimit              int64 // GB
	ExtractLimit              int64 // GB
	DailyLeechLimit           int64 // GB
	DailyTaskLimit            int   // count
	StorageThreshold          int64 // GB
	WorkerBotTokens           []string
	DumpChatID                int64
	UpstreamRepo              string
	UpstreamBranch            string
	UpdateOnStartup           bool
	GoFileToken               string
	PixelDrainKey             string
	BuzzHeavierToken          string
	DevUploadsKey             string
	StreamTapeAPI             string // "login:key"
	VikingFileUser            string
}

var (
	GlobalConfig  Config
	configLock    sync.RWMutex
	MaxSplitSize  int64 = 2097152000 // 1.91 GB default
	IsPremiumUser bool  = false

	configUpdatedHookMu sync.RWMutex
	configUpdatedHook   func(key, val string)
)

func SetOnConfigVarUpdated(fn func(key, val string)) {
	configUpdatedHookMu.Lock()
	defer configUpdatedHookMu.Unlock()
	configUpdatedHook = fn
}

func SetIsPremiumUser(isPrem bool) {
	configLock.Lock()
	defer configLock.Unlock()
	IsPremiumUser = isPrem
	if isPrem {
		MaxSplitSize = 4194304000 // 3.91 GB
	} else {
		MaxSplitSize = 2097152000 // 1.91 GB
	}
	if GlobalConfig.LeechSplitSize == 0 || GlobalConfig.LeechSplitSize > MaxSplitSize || GlobalConfig.LeechSplitSize == 2097152000 || GlobalConfig.LeechSplitSize == 4194304000 {
		GlobalConfig.LeechSplitSize = MaxSplitSize
	}
}

func GetMaxSplitSize() int64 {
	configLock.RLock()
	defer configLock.RUnlock()
	return MaxSplitSize
}

// syncLogDump: one channel like the Python bot. LEECH_LOG_ID gets the files; an old DUMP_CHAT_ID
// is used when LEECH_LOG_ID is empty, and the code that still says "dump" means the same channel.
func syncLogDump() {
	if GlobalConfig.LeechLogID == 0 {
		GlobalConfig.LeechLogID = GlobalConfig.DumpChatID
	}
	GlobalConfig.DumpChatID = GlobalConfig.LeechLogID
}

// noEnv: buildConfig ignores the environment and gives the defaults (for Reset in /bs).
var noEnv bool

func lookupEnv(key string) string {
	if noEnv {
		return ""
	}
	return os.Getenv(key)
}

// buildConfig reads every variable (or its default).
func buildConfig() Config {
	return Config{
		BotToken:                  getEnv("BOT_TOKEN", ""),
		OwnerID:                   getEnvInt64("OWNER_ID", 0),
		OwnerUsername:             strings.TrimPrefix(getEnv("OWNER_USERNAME", ""), "@"),
		TelegramAPI:               getEnvInt("TELEGRAM_API", 0),
		TelegramHash:              getEnv("TELEGRAM_HASH", ""),
		UserSessionString:         getEnv("USER_SESSION_STRING", ""),
		DatabaseURL:               getEnv("DATABASE_URL", ""),
		DownloadDir:               cleanDir(getEnv("DOWNLOAD_DIR", "/usr/src/app/downloads/")),
		CmdSuffix:                 getEnv("CMD_SUFFIX", ""),
		AuthorizedChats:           parseIDList(getEnv("AUTHORIZED_CHATS", "")),
		SudoUsers:                 parseIDList(getEnv("SUDO_USERS", "")),
		BlacklistUsers:            parseIDList(getEnv("BLACKLIST_USERS", "")),
		StatusLimit:               getEnvInt("STATUS_LIMIT", 4),
		StatusUpdateInterval:      getEnvInt("STATUS_UPDATE_INTERVAL", 10),
		AutoDeleteMessageDuration: getEnvInt("AUTO_DELETE_MESSAGE_DURATION", 60),
		LeechSplitSize:            getEnvInt64("LEECH_SPLIT_SIZE", 2097152000), // ~2GB
		EqualSplits:               getEnvBool("EQUAL_SPLITS", false),
		AsDocument:                getEnvBool("AS_DOCUMENT", false),
		MediaGroup:                getEnvBool("MEDIA_GROUP", false),
		BotPM:                     getEnvBool("BOT_PM", false),
		CapFont:                   getEnv("CAP_FONT", ""),
		LeechFilenamePrefix:       getEnv("LEECH_FILENAME_PREFIX", ""),
		LeechFilenameSuffix:       getEnv("LEECH_FILENAME_SUFFIX", ""),
		LeechFilenameCaption:      getEnv("LEECH_FILENAME_CAPTION", ""),
		LeechFilenameRemname:      getEnv("LEECH_FILENAME_REMNAME", ""),
		LeechFilenameRemnameAuto:  getEnv("LEECH_FILENAME_REMNAME_AUTO", ""),
		LeechFilenameRemnameRegex: getEnv("LEECH_FILENAME_REMNAME_REGEX", ""),
		LeechLogID:                getEnvInt64("LEECH_LOG_ID", 0),
		LinksLogID:                getEnvInt64("LINKS_LOG_ID", 0),
		TorrentTimeout:            getEnvInt("TORRENT_TIMEOUT", 600),
		Aria2Port:                 getEnvInt("ARIA2_PORT", 6810),
		BaseURL:                   getEnv("BASE_URL", ""),
		BaseURLPort:               getEnvInt("PORT", getEnvInt("BASE_URL_PORT", 23434)),
		MegaEmail:                 getEnv("MEGA_EMAIL", ""),
		MegaPassword:              getEnv("MEGA_PASSWORD", ""),
		TokenTimeout:              getEnvInt64("TOKEN_TIMEOUT", 0),
		LoginPass:                 getEnv("LOGIN_PASS", ""),
		Shortener:                 getEnv("SHORTENER", ""),
		ShortenerAPI:              getEnv("SHORTENER_API", ""),
		ShortenerEnabled:          getEnvBool("SHORTENER_ENABLED", true),
		TokenInPM:                 getEnvBool("TOKEN_IN_PM", true),
		ScreenshotsMode:           getEnvBool("SCREENSHOTS_MODE", false),
		FsubIDs:                   getEnv("FSUB_IDS", ""),
		MirrorLogID:               getEnvInt64("MIRROR_LOG_ID", 0),
		GDriveWorker:              getEnv("GDRIVE_WORKER", "https://gdrive.tjbot.workers.dev"),
		MediaStore:                getEnvBool("MEDIA_STORE", false),
		IncompleteTaskNotifier:    getEnvBool("INCOMPLETE_TASK_NOTIFIER", false),
		DisableVT:                 getEnvBool("DISABLE_VT", false),
		VtCustomCmds:              getEnv("VT_CUSTOM_CMDS", ""),
		BotMaxTasks:               getEnvInt("BOT_MAX_TASKS", 15),
		UserMaxTasks:              getEnvInt("USER_MAX_TASKS", 3),
		PremiumMaxTasks:           getEnvInt("PREMIUM_MAX_TASKS", 6),
		RealDebridAPI:             getEnv("REAL_DEBRID_API", ""),
		DebridLinkAPI:             getEnv("DEBRID_LINK_API", ""),
		SafeMode:                  getEnvBool("SAFE_MODE", false),
		PmAccess:                  getEnvBool("PM_ACCESS", false),
		SetCommands:               getEnvBool("SET_COMMANDS", true),
		WebPincode:                getEnvBool("WEB_PINCODE", true),
		YtDlpOptions:              getEnv("YT_DLP_OPTIONS", ""),
		UserTasksLimit:            getEnvInt("USER_TASKS_LIMIT", 0),
		PlaylistLimit:             getEnvInt("PLAYLIST_LIMIT", 0),
		ExtensionFilter:           getEnv("EXTENSION_FILTER", ""),
		GDriveID:                  strings.TrimSpace(getEnv("GDRIVE_ID", "")),
		IndexURL:                  strings.TrimRight(strings.TrimSpace(getEnv("INDEX_URL", "")), "/"),
		IsTeamDrive:               getEnvBool("IS_TEAM_DRIVE", false),
		StopDuplicate:             getEnvBool("STOP_DUPLICATE", false),
		DisableDriveLink:          getEnvBool("DISABLE_DRIVE_LINK", false),
		DefaultUpload:             strings.ToLower(strings.TrimSpace(getEnv("DEFAULT_UPLOAD", "gd"))),
		RclonePath:                strings.TrimSpace(getEnv("RCLONE_PATH", "")),
		RcloneFlags:               strings.TrimSpace(getEnv("RCLONE_FLAGS", "")),
		ExcepChats:                getEnv("EXCEP_CHATS", ""),
		DeleteLinks:               getEnvBool("DELETE_LINKS", false),
		RichMessages:              getEnvBool("RICH_MESSAGES", false),
		CleanLogMsg:               getEnvBool("CLEAN_LOG_MSG", false),
		UserTimeInterval:          getEnvInt("USER_TIME_INTERVAL", 0),
		DisableTorrents:           getEnvBool("DISABLE_TORRENTS", true),
		ShowMediaInfo:             getEnvBool("SHOW_MEDIAINFO", false),
		SaveMsg:                   getEnvBool("SAVE_MSG", false),
		SourceLink:                getEnvBool("SOURCE_LINK", false),
		Metadata:                  getEnv("METADATA", ""),
		TitleName:                 getEnv("TITLE_NAME", ""),
		AuthorName:                getEnv("AUTHOR_NAME", "Tj Bots"),
		AuthorURL:                 getEnv("AUTHOR_URL", "https://t.me/TJ_Bots"),
		BotTheme:                  getEnv("BOT_THEME", "minimal"),
		ShowExtraCmds:             getEnvBool("SHOW_EXTRA_CMDS", true),
		Timezone:                  getEnv("TIMEZONE", "Asia/Kolkata"),
		EnableFFmpegCmds:          !getEnvBool("DISABLE_FF_MODE", !getEnvBool("ENABLE_FFMPEG_CMDS", true)),
		FFmpegEncoding:            getEnvBool("FFMPEG_ENCODING", false),
		TMDBAccessToken:           getEnv("TMDB_ACCESS_TOKEN", ""),
		AutoThumbnail:             getEnvBool("AUTO_THUMBNAIL", false),
		CoverImage:                getEnv("COVER_IMAGE", "https://graph.org/file/60f9f8bcb97d27f76f5c0.jpg"),
		DDLAPI:                    getEnv("DDL_API", getEnv("THUNDER_API", "")),
		QueueAll:                  getEnvInt("QUEUE_ALL", 10),
		QueueDownload:             getEnvInt("QUEUE_DOWNLOAD", 5),
		QueueUpload:               getEnvInt("QUEUE_UPLOAD", 5),
		LeechLimit:                getEnvInt64("LEECH_LIMIT", 0),
		TorrentLimit:              getEnvInt64("TORRENT_LIMIT", 0),
		DirectLimit:               getEnvInt64("DIRECT_LIMIT", 0),
		YtdlpLimit:                getEnvInt64("YTDLP_LIMIT", 0),
		MegaLimit:                 getEnvInt64("MEGA_LIMIT", 0),
		ArchiveLimit:              getEnvInt64("ARCHIVE_LIMIT", 0),
		ExtractLimit:              getEnvInt64("EXTRACT_LIMIT", 0),
		DailyLeechLimit:           getEnvInt64("DAILY_LEECH_LIMIT", 0),
		DailyTaskLimit:            getEnvInt("DAILY_TASK_LIMIT", 0),
		StorageThreshold:          getEnvInt64("STORAGE_THRESHOLD", 1), // Default 1GB safety cushion
		DumpChatID:                getEnvInt64("DUMP_CHAT_ID", getEnvInt64("BIN_CHANNEL", getEnvInt64("STORAGE_CHANNEL", getEnvInt64("STORAGE_CHANNELS", getEnvInt64("LEECH_LOG_ID", 0))))),
		UpstreamRepo:              getEnv("UPSTREAM_REPO", "https://github.com/Tj-Bots/azml-go"),
		UpstreamBranch:            getEnv("UPSTREAM_BRANCH", "new"),
		UpdateOnStartup:           getEnvBool("UPDATE_ON_STARTUP", false),
		GoFileToken:               getEnv("GOFILE_TOKEN", ""),
		PixelDrainKey:             getEnv("PIXELDRAIN_KEY", ""),
		BuzzHeavierToken:          getEnv("BUZZHEAVIER_TOKEN", ""),
		DevUploadsKey:             getEnv("DEVUPLOADS_KEY", ""),
		StreamTapeAPI:             getEnv("STREAMTAPE_API", ""),
		VikingFileUser:            getEnv("VIKINGFILE_USER", ""),
	}
}

func LoadConfig(path string) (*Config, error) {
	configLock.Lock()
	defer configLock.Unlock()

	_ = godotenv.Load(path)
	_ = godotenv.Load(".env")
	_ = godotenv.Load("config.env")

	GlobalConfig = buildConfig()

	// Resolve main bot token
	mainToken := strings.Trim(strings.TrimSpace(getEnv("BOT_TOKEN", getEnv("MAIN_BOT_TOKEN", ""))), `"'`)
	if mainToken == "" {
		for _, candidate := range parseTokenString(getEnv("BOT_TOKENS", getEnv("MULTI_TOKENS", getEnv("MULTI_TOKEN", "")))) {
			if candidate != "" {
				mainToken = candidate
				break
			}
		}
	}
	GlobalConfig.BotToken = mainToken

	// Collect multi-bot worker tokens (strictly excluding the main BotToken)
	seenTokens := make(map[string]bool)
	var workerTokens []string

	tokenSources := []string{
		getEnv("BOT_TOKENS", ""),
		getEnv("MULTI_TOKENS", ""),
		getEnv("MULTI_TOKEN", ""),
		getEnv("TOKENS", ""),
		getEnv("WORKER_TOKENS", ""),
	}

	for _, src := range tokenSources {
		for _, t := range parseTokenString(src) {
			if t != "" && t != mainToken && !seenTokens[t] {
				workerTokens = append(workerTokens, t)
				seenTokens[t] = true
			}
		}
	}

	for i := 1; i <= 100; i++ {
		key := fmt.Sprintf("MULTI_TOKEN%d", i)
		if val := strings.Trim(strings.TrimSpace(os.Getenv(key)), `"'`); val != "" {
			if val != mainToken && !seenTokens[val] {
				workerTokens = append(workerTokens, val)
				seenTokens[val] = true
			}
		}
	}
	syncLogDump()
	GlobalConfig.WorkerBotTokens = workerTokens
	if len(workerTokens) > 0 {
		log.Printf("[Config] Loaded %d Multi-Bot Worker tokens for parallel transfers (Main Bot excluded, Dump: %d)", len(workerTokens), GlobalConfig.DumpChatID)
	}

	if err := os.MkdirAll(GlobalConfig.DownloadDir, 0777); err != nil {
		fallback := filepath.Join(os.TempDir(), "downloads") + string(filepath.Separator)
		if fErr := os.MkdirAll(fallback, 0777); fErr == nil {
			log.Printf("[Config] DownloadDir %s not writable (%v), falling back to %s", GlobalConfig.DownloadDir, err, fallback)
			GlobalConfig.DownloadDir = fallback
		}
	}
	_ = os.MkdirAll("Thumbnails", 0777)

	return &GlobalConfig, nil
}

func Get() *Config {
	configLock.RLock()
	defer configLock.RUnlock()
	return &GlobalConfig
}

func getEnv(key, defaultVal string) string {
	if val := lookupEnv(key); val != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	val := lookupEnv(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return defaultVal
	}
	return n
}

func parseTokenString(raw string) []string {
	var tokens []string
	raw = strings.ReplaceAll(raw, ";", ",")
	raw = strings.ReplaceAll(raw, "\n", ",")
	raw = strings.ReplaceAll(raw, "\r", ",")
	raw = strings.ReplaceAll(raw, "\t", ",")
	for _, part := range strings.Split(raw, ",") {
		for _, token := range strings.Fields(part) {
			token = strings.Trim(strings.TrimSpace(token), `"'`)
			if token != "" {
				tokens = append(tokens, token)
			}
		}
	}
	return tokens
}

func getEnvInt64(key string, defaultVal int64) int64 {
	val := lookupEnv(key)
	if val == "" {
		return defaultVal
	}
	val = strings.Trim(strings.TrimSpace(val), `"'`)
	if strings.Contains(val, ",") {
		val = strings.TrimSpace(strings.Split(val, ",")[0])
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return defaultVal
	}
	return n
}

func getEnvBool(key string, defaultVal bool) bool {
	val := strings.ToLower(strings.TrimSpace(lookupEnv(key)))
	if val == "" {
		return defaultVal
	}
	return val == "true" || val == "1" || val == "yes" || val == "t"
}

func parseIDList(val string) []int64 {
	var ids []int64
	parts := strings.Fields(val)
	for _, p := range parts {
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func cleanDir(dir string) string {
	if !strings.HasSuffix(dir, "/") && !strings.HasSuffix(dir, "\\") {
		dir += "/"
	}
	return dir
}

func UpdateConfigVar(key, val string) error {
	configLock.Lock()
	defer configLock.Unlock()

	key = strings.ToUpper(strings.TrimSpace(key))
	handled := true
	switch key {
	case "OWNER_USERNAME":
		GlobalConfig.OwnerUsername = strings.TrimPrefix(strings.TrimSpace(val), "@")
	case "STATUS_LIMIT":
		GlobalConfig.StatusLimit, _ = strconv.Atoi(val)
	case "STATUS_UPDATE_INTERVAL":
		GlobalConfig.StatusUpdateInterval, _ = strconv.Atoi(val)
	case "AUTO_DELETE_MESSAGE_DURATION":
		GlobalConfig.AutoDeleteMessageDuration, _ = strconv.Atoi(val)
	case "LEECH_SPLIT_SIZE":
		valInt, _ := strconv.ParseInt(val, 10, 64)
		if valInt > MaxSplitSize || valInt <= 0 {
			valInt = MaxSplitSize
		}
		GlobalConfig.LeechSplitSize = valInt
	case "AS_DOCUMENT":
		GlobalConfig.AsDocument = val == "true" || val == "1"
	case "EQUAL_SPLITS":
		GlobalConfig.EqualSplits = val == "true" || val == "1"
	case "MEDIA_GROUP":
		GlobalConfig.MediaGroup = val == "true" || val == "1"
	case "CAP_FONT":
		GlobalConfig.CapFont = val
	case "LEECH_FILENAME_PREFIX":
		GlobalConfig.LeechFilenamePrefix = val
	case "LEECH_FILENAME_SUFFIX":
		GlobalConfig.LeechFilenameSuffix = val
	case "LEECH_FILENAME_CAPTION":
		GlobalConfig.LeechFilenameCaption = val
	case "LEECH_FILENAME_REMNAME_AUTO":
		GlobalConfig.LeechFilenameRemnameAuto = val
	case "LEECH_FILENAME_REMNAME_REGEX":
		GlobalConfig.LeechFilenameRemnameRegex = val
	case "LEECH_LOG_ID":
		GlobalConfig.LeechLogID, _ = strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		GlobalConfig.DumpChatID = GlobalConfig.LeechLogID
	case "TORRENT_TIMEOUT":
		GlobalConfig.TorrentTimeout, _ = strconv.Atoi(val)
	case "SHORTENER":
		GlobalConfig.Shortener = val
	case "SHORTENER_API":
		GlobalConfig.ShortenerAPI = val
	case "SHORTENER_ENABLED":
		GlobalConfig.ShortenerEnabled = val == "true" || val == "1"
	case "TOKEN_TIMEOUT":
		GlobalConfig.TokenTimeout, _ = strconv.ParseInt(val, 10, 64)
	case "SAVE_MSG":
		GlobalConfig.SaveMsg = val == "true" || val == "1"
	case "SOURCE_LINK":
		GlobalConfig.SourceLink = val == "true" || val == "1"
	case "SHOW_MEDIAINFO":
		GlobalConfig.ShowMediaInfo = val == "true" || val == "1"
	case "USER_TIME_INTERVAL":
		GlobalConfig.UserTimeInterval, _ = strconv.Atoi(val)
	case "CLEAN_LOG_MSG":
		GlobalConfig.CleanLogMsg = val == "true" || val == "1"
	case "RICH_MESSAGES":
		GlobalConfig.RichMessages = val == "true" || val == "1"
	case "DELETE_LINKS":
		GlobalConfig.DeleteLinks = val == "true" || val == "1"
	case "EXCEP_CHATS":
		GlobalConfig.ExcepChats = val
	case "EXTENSION_FILTER":
		GlobalConfig.ExtensionFilter = val
	case "GDRIVE_ID":
		GlobalConfig.GDriveID = strings.TrimSpace(val)
	case "INDEX_URL":
		GlobalConfig.IndexURL = strings.TrimRight(strings.TrimSpace(val), "/")
	case "IS_TEAM_DRIVE":
		GlobalConfig.IsTeamDrive = isTrueVal(val)
	case "STOP_DUPLICATE":
		GlobalConfig.StopDuplicate = isTrueVal(val)
	case "DISABLE_DRIVE_LINK":
		GlobalConfig.DisableDriveLink = isTrueVal(val)
	case "DEFAULT_UPLOAD":
		GlobalConfig.DefaultUpload = strings.ToLower(strings.TrimSpace(val))
	case "RCLONE_PATH":
		GlobalConfig.RclonePath = strings.TrimSpace(val)
	case "RCLONE_FLAGS":
		GlobalConfig.RcloneFlags = strings.TrimSpace(val)
	case "LEECH_FILENAME_REMNAME":
		GlobalConfig.LeechFilenameRemname = val
	case "PLAYLIST_LIMIT":
		GlobalConfig.PlaylistLimit, _ = strconv.Atoi(val)
	case "USER_TASKS_LIMIT":
		GlobalConfig.UserTasksLimit, _ = strconv.Atoi(val)
	case "YT_DLP_OPTIONS":
		GlobalConfig.YtDlpOptions = val
	case "WEB_PINCODE":
		GlobalConfig.WebPincode = val == "true" || val == "1"
	case "SET_COMMANDS":
		GlobalConfig.SetCommands = val == "true" || val == "1"
	case "PM_ACCESS":
		GlobalConfig.PmAccess = val == "true" || val == "1"
	case "SAFE_MODE":
		GlobalConfig.SafeMode = val == "true" || val == "1"
	case "DEBRID_LINK_API":
		GlobalConfig.DebridLinkAPI = val
	case "REAL_DEBRID_API":
		GlobalConfig.RealDebridAPI = val
	case "VT_CUSTOM_CMDS":
		GlobalConfig.VtCustomCmds = val
	case "DISABLE_VT":
		GlobalConfig.DisableVT = val == "true" || val == "1"
	case "INCOMPLETE_TASK_NOTIFIER":
		GlobalConfig.IncompleteTaskNotifier = val == "true" || val == "1"
	case "MEDIA_STORE":
		GlobalConfig.MediaStore = val == "true" || val == "1"
	case "GDRIVE_WORKER":
		GlobalConfig.GDriveWorker = val
	case "LINKS_LOG_ID":
		GlobalConfig.LinksLogID, _ = strconv.ParseInt(val, 10, 64)
	case "MIRROR_LOG_ID":
		GlobalConfig.MirrorLogID, _ = strconv.ParseInt(val, 10, 64)
	case "FSUB_IDS":
		GlobalConfig.FsubIDs = val
	case "SCREENSHOTS_MODE":
		GlobalConfig.ScreenshotsMode = val == "true" || val == "1"
	case "DISABLE_TORRENTS":
		GlobalConfig.DisableTorrents = val == "true" || val == "1"
	case "DISABLE_FF_MODE": // name of our Python bot: True = -ff blocked
		GlobalConfig.EnableFFmpegCmds = !(val == "true" || val == "1")
	case "ENABLE_FFMPEG_CMDS":
		GlobalConfig.EnableFFmpegCmds = val == "true" || val == "1"
	case "FFMPEG_ENCODING":
		GlobalConfig.FFmpegEncoding = val == "true" || val == "1"
	case "TMDB_ACCESS_TOKEN":
		GlobalConfig.TMDBAccessToken = val
	case "AUTO_THUMBNAIL":
		GlobalConfig.AutoThumbnail = val == "true" || val == "1"
	case "COVER_IMAGE":
		GlobalConfig.CoverImage = val
	case "DDL_API", "THUNDER_API":
		GlobalConfig.DDLAPI = val
	case "BASE_URL":
		GlobalConfig.BaseURL = val
	case "BASE_URL_PORT":
		GlobalConfig.BaseURLPort, _ = strconv.Atoi(val)
	case "BOT_MAX_TASKS":
		GlobalConfig.BotMaxTasks, _ = strconv.Atoi(val)
	case "QUEUE_ALL":
		GlobalConfig.QueueAll, _ = strconv.Atoi(val)
	case "USER_MAX_TASKS":
		GlobalConfig.UserMaxTasks, _ = strconv.Atoi(val)
	case "PREMIUM_MAX_TASKS":
		GlobalConfig.PremiumMaxTasks, _ = strconv.Atoi(val)
	case "QUEUE_DOWNLOAD":
		GlobalConfig.QueueDownload, _ = strconv.Atoi(val)
	case "QUEUE_UPLOAD":
		GlobalConfig.QueueUpload, _ = strconv.Atoi(val)
	case "LEECH_LIMIT":
		GlobalConfig.LeechLimit, _ = strconv.ParseInt(val, 10, 64)
	case "TORRENT_LIMIT":
		GlobalConfig.TorrentLimit, _ = strconv.ParseInt(val, 10, 64)
	case "DIRECT_LIMIT":
		GlobalConfig.DirectLimit, _ = strconv.ParseInt(val, 10, 64)
	case "YTDLP_LIMIT":
		GlobalConfig.YtdlpLimit, _ = strconv.ParseInt(val, 10, 64)
	case "MEGA_LIMIT":
		GlobalConfig.MegaLimit, _ = strconv.ParseInt(val, 10, 64)
	case "ARCHIVE_LIMIT":
		GlobalConfig.ArchiveLimit, _ = strconv.ParseInt(val, 10, 64)
	case "EXTRACT_LIMIT":
		GlobalConfig.ExtractLimit, _ = strconv.ParseInt(val, 10, 64)
	case "DAILY_LEECH_LIMIT":
		GlobalConfig.DailyLeechLimit, _ = strconv.ParseInt(val, 10, 64)
	case "DAILY_TASK_LIMIT":
		GlobalConfig.DailyTaskLimit, _ = strconv.Atoi(val)
	case "USER_SESSION_STRING":
		GlobalConfig.UserSessionString = strings.TrimSpace(val)
	case "DUMP_CHAT_ID": // old name: one channel now, like the Python bot (LEECH_LOG_ID wins)
		if GlobalConfig.LeechLogID == 0 {
			GlobalConfig.LeechLogID, _ = strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		}
		GlobalConfig.DumpChatID = GlobalConfig.LeechLogID
	case "STORAGE_THRESHOLD":
		GlobalConfig.StorageThreshold, _ = strconv.ParseInt(val, 10, 64)
	case "TIMEZONE": // applies at once (status times, logs, restart message)
		val = strings.TrimSpace(val)
		if val == "" {
			val = "Asia/Kolkata"
		}
		GlobalConfig.Timezone = val
		if loc, err := time.LoadLocation(val); err == nil {
			time.Local = loc
		}
	case "UPSTREAM_REPO":
		GlobalConfig.UpstreamRepo = strings.TrimSpace(val)
	case "UPSTREAM_BRANCH":
		GlobalConfig.UpstreamBranch = strings.TrimSpace(val)
	case "GOFILE_TOKEN":
		GlobalConfig.GoFileToken = strings.TrimSpace(val)
	case "PIXELDRAIN_KEY":
		GlobalConfig.PixelDrainKey = strings.TrimSpace(val)
	case "BUZZHEAVIER_TOKEN":
		GlobalConfig.BuzzHeavierToken = strings.TrimSpace(val)
	case "DEVUPLOADS_KEY":
		GlobalConfig.DevUploadsKey = strings.TrimSpace(val)
	case "STREAMTAPE_API":
		GlobalConfig.StreamTapeAPI = strings.TrimSpace(val)
	case "VIKINGFILE_USER":
		GlobalConfig.VikingFileUser = strings.TrimSpace(val)
	default:
		handled = false
	}

	_ = os.Setenv(key, val)
	if !handled {
		// a variable read only at start: rebuild from the environment so it applies now
		keep := GlobalConfig
		GlobalConfig = buildConfig()
		GlobalConfig.BotToken, GlobalConfig.WorkerBotTokens, GlobalConfig.DownloadDir = keep.BotToken, keep.WorkerBotTokens, keep.DownloadDir
		syncLogDump()
	}
	_ = saveVarToFile("config.env", key, val)
	if _, err := os.Stat(".env"); err == nil {
		_ = saveVarToFile(".env", key, val)
	}

	configUpdatedHookMu.RLock()
	hook := configUpdatedHook
	configUpdatedHookMu.RUnlock()
	if hook != nil {
		hook(key, val)
	}

	return nil
}

func MaskSession(sess string) string {
	sess = strings.TrimSpace(sess)
	if sess == "" {
		return "Not Set"
	}
	if len(sess) > 12 {
		return sess[:6] + "..." + sess[len(sess)-4:] + " (Set)"
	}
	return "*** (Set)"
}

func saveVarToFile(filename, key, val string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			content := fmt.Sprintf("%s=%s\n", key, val)
			return os.WriteFile(filename, []byte(content), 0644)
		}
		return err
	}

	lines := strings.Split(string(data), "\n")
	found := false
	targetPrefix := key + "="
	targetPrefixSpace := key + " "

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, targetPrefix) || strings.HasPrefix(trimmed, targetPrefixSpace) {
			lines[i] = fmt.Sprintf("%s=%s", key, val)
			found = true
			break
		}
	}

	if !found {
		lines = append(lines, fmt.Sprintf("%s=%s", key, val))
	}

	output := strings.Join(lines, "\n")
	return os.WriteFile(filename, []byte(output), 0644)
}

func GetConfigMap() map[string]string {
	configLock.RLock()
	defer configLock.RUnlock()
	return configMapLocked()
}

func configMapLocked() map[string]string {
	return map[string]string{
		"TIMEZONE":                     GlobalConfig.Timezone,
		"MIRROR_LOG_ID":                strconv.FormatInt(GlobalConfig.MirrorLogID, 10),
		"LINKS_LOG_ID":                 strconv.FormatInt(GlobalConfig.LinksLogID, 10),
		"GDRIVE_WORKER":                GlobalConfig.GDriveWorker,
		"MEDIA_STORE":                  strconv.FormatBool(GlobalConfig.MediaStore),
		"INCOMPLETE_TASK_NOTIFIER":     strconv.FormatBool(GlobalConfig.IncompleteTaskNotifier),
		"DISABLE_VT":                   strconv.FormatBool(GlobalConfig.DisableVT),
		"VT_CUSTOM_CMDS":               GlobalConfig.VtCustomCmds,
		"DISABLE_FF_MODE":              strconv.FormatBool(!GlobalConfig.EnableFFmpegCmds),
		"CAP_FONT":                     GlobalConfig.CapFont,
		"REAL_DEBRID_API":              GlobalConfig.RealDebridAPI,
		"DEBRID_LINK_API":              GlobalConfig.DebridLinkAPI,
		"SAFE_MODE":                    strconv.FormatBool(GlobalConfig.SafeMode),
		"PM_ACCESS":                    strconv.FormatBool(GlobalConfig.PmAccess),
		"SET_COMMANDS":                 strconv.FormatBool(GlobalConfig.SetCommands),
		"WEB_PINCODE":                  strconv.FormatBool(GlobalConfig.WebPincode),
		"YT_DLP_OPTIONS":               GlobalConfig.YtDlpOptions,
		"USER_TASKS_LIMIT":             strconv.Itoa(GlobalConfig.UserTasksLimit),
		"PLAYLIST_LIMIT":               strconv.Itoa(GlobalConfig.PlaylistLimit),
		"LEECH_FILENAME_REMNAME":       GlobalConfig.LeechFilenameRemname,
		"EXTENSION_FILTER":             GlobalConfig.ExtensionFilter,
		"GDRIVE_ID":                    GlobalConfig.GDriveID,
		"INDEX_URL":                    GlobalConfig.IndexURL,
		"IS_TEAM_DRIVE":                strconv.FormatBool(GlobalConfig.IsTeamDrive),
		"STOP_DUPLICATE":               strconv.FormatBool(GlobalConfig.StopDuplicate),
		"DISABLE_DRIVE_LINK":           strconv.FormatBool(GlobalConfig.DisableDriveLink),
		"DEFAULT_UPLOAD":               GlobalConfig.DefaultUpload,
		"RCLONE_PATH":                  GlobalConfig.RclonePath,
		"RCLONE_FLAGS":                 GlobalConfig.RcloneFlags,
		"EXCEP_CHATS":                  GlobalConfig.ExcepChats,
		"DELETE_LINKS":                 strconv.FormatBool(GlobalConfig.DeleteLinks),
		"RICH_MESSAGES":                strconv.FormatBool(GlobalConfig.RichMessages),
		"CLEAN_LOG_MSG":                strconv.FormatBool(GlobalConfig.CleanLogMsg),
		"USER_TIME_INTERVAL":           strconv.Itoa(GlobalConfig.UserTimeInterval),
		"SAVE_MSG":                     strconv.FormatBool(GlobalConfig.SaveMsg),
		"SOURCE_LINK":                  strconv.FormatBool(GlobalConfig.SourceLink),
		"SHOW_MEDIAINFO":               strconv.FormatBool(GlobalConfig.ShowMediaInfo),
		"FSUB_IDS":                     GlobalConfig.FsubIDs,
		"DISABLE_TORRENTS":             strconv.FormatBool(GlobalConfig.DisableTorrents),
		"OWNER_USERNAME":               GlobalConfig.OwnerUsername,
		"USER_SESSION_STRING":          MaskSession(GlobalConfig.UserSessionString),
		"DUMP_CHAT_ID":                 strconv.FormatInt(GlobalConfig.DumpChatID, 10),
		"LEECH_LOG_ID":                 strconv.FormatInt(GlobalConfig.LeechLogID, 10),
		"STATUS_LIMIT":                 strconv.Itoa(GlobalConfig.StatusLimit),
		"STATUS_UPDATE_INTERVAL":       strconv.Itoa(GlobalConfig.StatusUpdateInterval),
		"AUTO_DELETE_MESSAGE_DURATION": strconv.Itoa(GlobalConfig.AutoDeleteMessageDuration),
		"LEECH_SPLIT_SIZE":             strconv.FormatInt(GlobalConfig.LeechSplitSize, 10),
		"AS_DOCUMENT":                  strconv.FormatBool(GlobalConfig.AsDocument),
		"EQUAL_SPLITS":                 strconv.FormatBool(GlobalConfig.EqualSplits),
		"MEDIA_GROUP":                  strconv.FormatBool(GlobalConfig.MediaGroup),
		"AUTO_THUMBNAIL":               strconv.FormatBool(GlobalConfig.AutoThumbnail),
		"LEECH_FILENAME_PREFIX":        GlobalConfig.LeechFilenamePrefix,
		"LEECH_FILENAME_SUFFIX":        GlobalConfig.LeechFilenameSuffix,
		"LEECH_FILENAME_CAPTION":       GlobalConfig.LeechFilenameCaption,
		"TORRENT_TIMEOUT":              strconv.Itoa(GlobalConfig.TorrentTimeout),
		"BASE_URL":                     GlobalConfig.BaseURL,
		"BOT_MAX_TASKS":                strconv.Itoa(GlobalConfig.BotMaxTasks),
		"USER_MAX_TASKS":               strconv.Itoa(GlobalConfig.UserMaxTasks),
		"PREMIUM_MAX_TASKS":            strconv.Itoa(GlobalConfig.PremiumMaxTasks),
		"LEECH_LIMIT":                  strconv.FormatInt(GlobalConfig.LeechLimit, 10),
		"TORRENT_LIMIT":                strconv.FormatInt(GlobalConfig.TorrentLimit, 10),
		"DIRECT_LIMIT":                 strconv.FormatInt(GlobalConfig.DirectLimit, 10),
		"YTDLP_LIMIT":                  strconv.FormatInt(GlobalConfig.YtdlpLimit, 10),
		"MEGA_LIMIT":                   strconv.FormatInt(GlobalConfig.MegaLimit, 10),
		"DAILY_LEECH_LIMIT":            strconv.FormatInt(GlobalConfig.DailyLeechLimit, 10),
		"DAILY_TASK_LIMIT":             strconv.Itoa(GlobalConfig.DailyTaskLimit),
		"SHORTENER":                    GlobalConfig.Shortener,
		"SHORTENER_API":                GlobalConfig.ShortenerAPI,
		"SHORTENER_ENABLED":            strconv.FormatBool(GlobalConfig.ShortenerEnabled),
		"TOKEN_TIMEOUT":                strconv.FormatInt(GlobalConfig.TokenTimeout, 10),
		"UPSTREAM_REPO":                GlobalConfig.UpstreamRepo,
		"UPSTREAM_BRANCH":              GlobalConfig.UpstreamBranch,
		"GOFILE_TOKEN":                 GlobalConfig.GoFileToken,
		"PIXELDRAIN_KEY":               GlobalConfig.PixelDrainKey,
		"BUZZHEAVIER_TOKEN":            GlobalConfig.BuzzHeavierToken,
		"DEVUPLOADS_KEY":               GlobalConfig.DevUploadsKey,
		"STREAMTAPE_API":               GlobalConfig.StreamTapeAPI,
		"VIKINGFILE_USER":              GlobalConfig.VikingFileUser,
	}
}

// StartupValues are the vars read only at start, with their current (or default) value,
// so /bs can show them instead of "None".
func StartupValues() map[string]string {
	configLock.RLock()
	defer configLock.RUnlock()
	return startupValuesLocked()
}

func startupValuesLocked() map[string]string {
	c := GlobalConfig
	ids := func(l []int64) string {
		var out []string
		for _, v := range l {
			out = append(out, strconv.FormatInt(v, 10))
		}
		return strings.Join(out, " ")
	}
	b := strconv.FormatBool
	return map[string]string{
		"DOWNLOAD_DIR":                 c.DownloadDir,
		"CMD_SUFFIX":                   c.CmdSuffix,
		"AUTHORIZED_CHATS":             ids(c.AuthorizedChats),
		"BLACKLIST_USERS":              ids(c.BlacklistUsers),
		"BOT_PM":                       b(c.BotPM),
		"CAP_FONT":                     c.CapFont,
		"LEECH_FILENAME_REMNAME_AUTO":  c.LeechFilenameRemnameAuto,
		"LEECH_FILENAME_REMNAME_REGEX": c.LeechFilenameRemnameRegex,
		"ARIA2_PORT":                   strconv.Itoa(c.Aria2Port),
		"BASE_URL_PORT":                strconv.Itoa(c.BaseURLPort),
		"MEGA_EMAIL":                   c.MegaEmail,
		"MEGA_PASSWORD":                c.MegaPassword,
		"TOKEN_IN_PM":                  b(c.TokenInPM),
		"SCREENSHOTS_MODE":             b(c.ScreenshotsMode),
		"SHOW_MEDIAINFO":               b(c.ShowMediaInfo),
		"METADATA":                     c.Metadata,
		"AUTHOR_NAME":                  c.AuthorName,
		"AUTHOR_URL":                   c.AuthorURL,
		"SHOW_EXTRA_CMDS":              b(c.ShowExtraCmds),
		"TIMEZONE":                     c.Timezone,
		"ENABLE_FFMPEG_CMDS":           b(c.EnableFFmpegCmds),
		"FFMPEG_ENCODING":              b(c.FFmpegEncoding),
		"TMDB_ACCESS_TOKEN":            c.TMDBAccessToken,
		"DDL_API":                      c.DDLAPI,
		"QUEUE_ALL":                    strconv.Itoa(c.QueueAll),
		"QUEUE_DOWNLOAD":               strconv.Itoa(c.QueueDownload),
		"QUEUE_UPLOAD":                 strconv.Itoa(c.QueueUpload),
		"ARCHIVE_LIMIT":                strconv.FormatInt(c.ArchiveLimit, 10),
		"EXTRACT_LIMIT":                strconv.FormatInt(c.ExtractLimit, 10),
		"STORAGE_THRESHOLD":            strconv.FormatInt(c.StorageThreshold, 10),
	}
}

// DefaultValue is the value a variable has when nothing sets it (the code default), for Reset.
func DefaultValue(key string) (string, bool) {
	configLock.Lock()
	defer configLock.Unlock()
	saved := GlobalConfig
	noEnv = true
	GlobalConfig = buildConfig()
	noEnv = false
	m := configMapLocked()
	for k, v := range startupValuesLocked() {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	GlobalConfig = saved
	v, ok := m[key]
	return v, ok
}

// AllKeys: every variable the bot knows.
func AllKeys() []string {
	configLock.RLock()
	defer configLock.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]string{configMapLocked(), startupValuesLocked()} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	for _, k := range CoreKeys {
		if !seen[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// CoreKeys: read only from the deploy (Heroku / config.env), never from /bs, like the Python bot.
var CoreKeys = []string{"BOT_TOKEN", "OWNER_ID", "TELEGRAM_API", "TELEGRAM_HASH", "DATABASE_URL", "DATABASE_NAME"}

func IsCoreKey(k string) bool {
	for _, c := range CoreKeys {
		if c == k {
			return true
		}
	}
	return false
}

func isTrueVal(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "true" || v == "1" || v == "yes" || v == "on"
}
