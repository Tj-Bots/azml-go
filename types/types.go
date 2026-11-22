package types

import (
	"context"
	"sync"
	"time"

	"github.com/gotd/td/tg"
)

type UserSettings struct {
	MediaStore     bool              `json:"media_store,omitempty" bson:"media_store,omitempty"`
	MediaStoreSet  bool              `json:"media_store_set,omitempty" bson:"media_store_set,omitempty"` // chose in /us (else MEDIA_STORE)
	MegaEmail      string            `json:"mega_email,omitempty" bson:"mega_email,omitempty"`
	MegaPass       string            `json:"mega_pass,omitempty" bson:"mega_pass,omitempty"`
	MediaInfoSet   bool              `json:"mediainfo_set,omitempty" bson:"mediainfo_set,omitempty"` // the user chose in /us (else SHOW_MEDIAINFO)
	YtOpt          string            `json:"yt_opt,omitempty" bson:"yt_opt,omitempty"`               // extra yt-dlp options
	YtCookies      string            `json:"yt_cookies,omitempty" bson:"yt_cookies,omitempty"`       // the user's cookies.txt
	UserID         int64             `json:"user_id" bson:"_id"`
	Name           string            `json:"name" bson:"name"`
	IsAuth         bool              `json:"is_auth" bson:"is_auth"`
	IsSudo         bool              `json:"is_sudo" bson:"is_sudo"`
	IsBlacklist    bool              `json:"is_blacklist" bson:"is_blacklist"`
	IsPremium      bool              `json:"is_premium" bson:"is_premium"`
	ExpiryTime     int64             `json:"expiry_time" bson:"expiry_time"`
	Token          string            `json:"token" bson:"token"`
	TokenTime      int64             `json:"token_time" bson:"token_time"`
	Prefix         string            `json:"prefix" bson:"prefix"`
	Suffix         string            `json:"suffix" bson:"suffix"`
	RemnameAuto    string            `json:"remname_auto" bson:"remname_auto"`
	RemnameRegex   string            `json:"remname_regex" bson:"remname_regex"`
	RenameMethod   string            `json:"rename_method" bson:"rename_method"` // "auto" or "regex"
	Autorename     bool              `json:"autorename" bson:"autorename"`
	Caption        string            `json:"caption" bson:"caption"`
	CustomThumb    string            `json:"custom_thumb" bson:"custom_thumb"`
	SplitSize      int64             `json:"split_size" bson:"split_size"`
	EqualSplits    bool              `json:"equal_splits" bson:"equal_splits"`
	MediaGroup     bool              `json:"media_group" bson:"media_group"`
	AsDoc          bool              `json:"as_doc" bson:"as_doc"`
	DumpChatID     int64             `json:"dump_chat_id" bson:"dump_chat_id"`
	Metadata       map[string]string `json:"metadata" bson:"metadata"`
	FFmpegCommands map[string]string `json:"ffmpeg_cmds" bson:"ffmpeg_cmds"`
	AutoThumb      bool              `json:"auto_thumb" bson:"auto_thumb"`
	AutoThumbSet   bool              `json:"auto_thumb_set,omitempty" bson:"auto_thumb_set,omitempty"` // chose in /us (else AUTO_THUMBNAIL)
	BotPM          bool              `json:"bot_pm" bson:"bot_pm"`
	StartedInPM    bool              `json:"started_in_pm" bson:"started_in_pm"`
	TopicIDs       []int             `json:"topic_ids" bson:"topic_ids"`
	MirrorHost     string            `json:"mirror_host" bson:"mirror_host"`
	DefaultUpload  string            `json:"default_upload,omitempty" bson:"default_upload,omitempty"` // gd / rc / ddl (else DEFAULT_UPLOAD)
	RcloneConf     []byte            `json:"rclone_conf,omitempty" bson:"rclone_conf,omitempty"`       // his own rclone.conf (-up mrcc:remote:path)
	MPrefix        string            `json:"mprefix,omitempty" bson:"mprefix,omitempty"`               // Mirror Prefix
	MSuffix        string            `json:"msuffix,omitempty" bson:"msuffix,omitempty"`               // Mirror Suffix
	MRemname       string            `json:"mremname,omitempty" bson:"mremname,omitempty"`             // Mirror Remname (a:b|c)
	RclonePath     string            `json:"rclone_path,omitempty" bson:"rclone_path,omitempty"`       // his RClone Path (-up rc)
	RcloneFlags    string            `json:"rclone_flags,omitempty" bson:"rclone_flags,omitempty"`     // his RClone Flags
	StopDup        string            `json:"stop_dup,omitempty" bson:"stop_dup,omitempty"`             // on / off (else STOP_DUPLICATE)
	ExcludedExts   string            `json:"excluded_exts" bson:"excluded_exts"`
	CapFont        string            `json:"cap_font" bson:"cap_font"`
	MediaInfo      bool              `json:"mediainfo" bson:"mediainfo"`                       // MediaInfo button under leeched files
	Screenshots    int               `json:"screenshots" bson:"screenshots"`                   // screenshots per video, 0 = off
	SampleSec      int               `json:"sample_sec" bson:"sample_sec"`                     // sample video length, 0 = off
	DDLKeys        map[string]string `json:"ddl_keys" bson:"ddl_keys"`                         // per user API keys of the /mirror hosts
	History        []HistoryEntry    `json:"history" bson:"history"`                           // the last finished tasks (newest first)
	ThumbData      []byte            `json:"thumb_data,omitempty" bson:"thumb_data,omitempty"` // the thumbnail image, so it survives restarts
}

// HistoryEntry is one finished task in /history.
type HistoryEntry struct {
	Name string `json:"name" bson:"name"`
	Size int64  `json:"size" bson:"size"`
	Mode string `json:"mode" bson:"mode"`
	Link string `json:"link" bson:"link"`
	Time int64  `json:"time" bson:"time"`
}

type TaskStatus string

const (
	StatusDownloading TaskStatus = "Downloading"
	StatusUploading   TaskStatus = "Uploading"
	StatusSplitting   TaskStatus = "Splitting"
	StatusFFmpeg      TaskStatus = "FFmpeg"
	StatusExtracting  TaskStatus = "Extracting"
	StatusArchiving   TaskStatus = "Archiving"
	StatusQueuedDL    TaskStatus = "QueueDL"
	StatusQueuedUP    TaskStatus = "QueueUp"
	StatusPaused      TaskStatus = "Paused"
	StatusProcessing  TaskStatus = "Processing" // a step between download and upload (the engine says which)
)

type DownloadTask interface {
	GID() string
	Name() string
	Size() int64
	DownloadedBytes() int64
	Speed() float64
	ETA() string
	Progress() float64
	Status() TaskStatus
	Engine() string
	Cancel() error
	StartTime() time.Time
	MessageID() int
	ChatID() int64
	UserID() int64
}

type TaskListener interface {
	OnDownloadStart() error
	OnDownloadProgress(current, total int64)
	OnDownloadComplete() error
	OnDownloadError(err string) error
	OnUploadStart() error
	OnUploadProgress(current, total int64)
	OnUploadComplete() error
	OnUploadError(err string) error
	Context() context.Context
	Cancel()
}

type LeechOptions struct {
	CustomName  string
	IsZip       bool
	IsExtract   bool
	Screenshots int
	ThumbURL    string
	ExtractStrm bool
	FFmpegKey   string
	AsDoc       bool
	AsMedia     bool
	UserDump    int64
	Headers     string
	Username    string
	Password    string
	Bulk        bool
	BulkRange   string
	Select      bool
	Multi       int
	MediaGroup  bool
	IsMirror    bool
	MirrorHost  string
	RcloneDest  string   // TJ RClone: remote:path (mrcc: = the user's rclone.conf)
	Ytdl        bool     // download the link with yt-dlp
	VT          bool     // -vt Video Tools menu
	SameDir     string   // -m <folder name>
	SplitSize   int64    // -sp
	ExtractAud  string   // -ea [format]
	ConvVideo   string   // -cv
	ConvAudio   string   // -ca
	Join        bool     // -j
	YtdlFormat  string   // yt-dlp -f
	YtdlArgs    []string // extra yt-dlp options (audio extraction)
}

type MessageContext struct {
	SameDirKey  string // -m: the shared folder of this -i / -b run
	Internal    bool   // started by the bot itself (-i run): no time interval check
	YtdlQuality string // set for /ytdl tasks (used by Retry)
	IsZip       bool
	IsUnzip     bool
	IsMirror    bool
	MirrorHost  string
	RcloneDest  string // TJ RClone: remote:path (mrcc: = the user's rclone.conf)
	ChatID      int64
	MessageID   int
	UserID      int64
	SenderName  string
	Username    string
	LangCode    string // Telegram language of the sender (e.g. "he")
	Text        string
	ReplyToID   int
	ReplyText   string
	ReplyUserID int64
	IsReply     bool
	HasMedia    bool
	MediaType   string
	FileID      string
	FileName    string
	FileSize    int64
	RawMedia    tg.MessageMediaClass
}

type SafeUserMap struct {
	sync.RWMutex
	users map[int64]*UserSettings
}

func NewSafeUserMap() *SafeUserMap {
	return &SafeUserMap{
		users: make(map[int64]*UserSettings),
	}
}

func (m *SafeUserMap) Get(userID int64) *UserSettings {
	m.RLock()
	defer m.RUnlock()
	if u, ok := m.users[userID]; ok {
		return u
	}
	return nil
}

func (m *SafeUserMap) GetOrCreate(userID int64) *UserSettings {
	m.Lock()
	defer m.Unlock()
	if u, ok := m.users[userID]; ok {
		return u
	}
	u := &UserSettings{
		UserID:       userID,
		Autorename:   false,
		RenameMethod: "auto",
		AsDoc:        false, // upload as media by default
		Metadata:     make(map[string]string),
	}
	m.users[userID] = u
	return u
}

func (m *SafeUserMap) Set(u *UserSettings) {
	m.Lock()
	defer m.Unlock()
	m.users[u.UserID] = u
}

func (m *SafeUserMap) GetAllUsers() []*UserSettings {
	m.RLock()
	defer m.RUnlock()
	var list []*UserSettings
	for _, u := range m.users {
		list = append(list, u)
	}
	return list
}

// MediaInfoOn: the user's choice in /us, or SHOW_MEDIAINFO from /bs when he never chose.
func (u *UserSettings) MediaInfoOn(botDefault bool) bool {
	if u == nil {
		return botDefault
	}
	if u.MediaInfoSet || u.MediaInfo { // MediaInfo=true from before this field existed
		return u.MediaInfo
	}
	return botDefault
}

// AutoThumbOn: the user's choice in /us, or AUTO_THUMBNAIL from /bs.
func (u *UserSettings) AutoThumbOn(botDefault bool) bool {
	if u == nil {
		return botDefault
	}
	if !u.AutoThumbSet {
		return botDefault || u.AutoThumb // older saves turned it on without the "set" mark
	}
	return u.AutoThumb
}

// MediaStoreOn: the user's choice in /us, or MEDIA_STORE from /bs.
func (u *UserSettings) MediaStoreOn(botDefault bool) bool {
	if u == nil || !u.MediaStoreSet {
		return botDefault
	}
	return u.MediaStore
}

// MirrorText: the text values of /us → Mirror Settings (mprefix, msuffix, mremname, rcpath, rcflags).
func (u *UserSettings) MirrorText(key string) string {
	switch key {
	case "mprefix":
		return u.MPrefix
	case "msuffix":
		return u.MSuffix
	case "mremname":
		return u.MRemname
	case "rcpath":
		return u.RclonePath
	case "rcflags":
		return u.RcloneFlags
	}
	return ""
}

// SetMirrorText sets one of them ("" clears it).
func (u *UserSettings) SetMirrorText(key, val string) {
	switch key {
	case "mprefix":
		u.MPrefix = val
	case "msuffix":
		u.MSuffix = val
	case "mremname":
		u.MRemname = val
	case "rcpath":
		u.RclonePath = val
	case "rcflags":
		u.RcloneFlags = val
	}
}

// StopDupOn: his Stop Duplicate, else the bot's STOP_DUPLICATE.
func (u *UserSettings) StopDupOn(botDefault bool) bool {
	if u != nil && u.StopDup != "" {
		return u.StopDup == "on"
	}
	return botDefault
}
