package ext

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"azml-go/config"
)

type userDailyStats struct {
	date      string
	taskCount int
	bytesUsed int64
}

var (
	dailyStatsLock sync.Mutex
	dailyStatsMap  = make(map[int64]*userDailyStats)
)

func getTodayKey() string {
	return time.Now().Format("2006-01-02")
}

// GetUserDailyStats returns the current task count and bytes leached by the user today
func GetUserDailyStats(userID int64) (tasks int, bytesLeached int64) {
	dailyStatsLock.Lock()
	defer dailyStatsLock.Unlock()

	today := getTodayKey()
	stat, ok := dailyStatsMap[userID]
	if !ok || stat.date != today {
		return 0, 0
	}
	return stat.taskCount, stat.bytesUsed
}

// RecordDailyUsage increments user's daily tasks and adds leached bytes
func RecordDailyUsage(userID int64, bytesUsed int64) {
	dailyStatsLock.Lock()
	defer dailyStatsLock.Unlock()

	today := getTodayKey()
	stat, ok := dailyStatsMap[userID]
	if !ok || stat.date != today {
		dailyStatsMap[userID] = &userDailyStats{
			date:      today,
			taskCount: 1,
			bytesUsed: bytesUsed,
		}
		return
	}
	stat.taskCount++
	stat.bytesUsed += bytesUsed
}

// CheckStorageThreshold verifies that taking 'size' bytes leaves at least 'STORAGE_THRESHOLD' GB free.
func CheckStorageThreshold(size int64, isArchive bool) bool {
	cfg := config.Get()
	thresholdBytes := cfg.StorageThreshold * 1024 * 1024 * 1024
	if thresholdBytes <= 0 {
		thresholdBytes = 500 * 1024 * 1024 // 500MB minimal cushion
	}

	freeBytes := GetFreeDiskSpace(cfg.DownloadDir)
	if freeBytes <= 0 {
		return true // Cannot determine, proceed
	}

	needed := size
	if isArchive {
		needed = size * 2 // Archives need extra space for uncompressed extraction
	}

	return (freeBytes - needed) >= thresholdBytes
}

// GetFreeDiskSpace returns free bytes on the disk of dirPath (it was RAM before, so
// STORAGE_THRESHOLD looked at the wrong number).
func GetFreeDiskSpace(dirPath string) int64 {
	if dirPath == "" {
		dirPath = "."
	}
	if _, err := os.Stat(dirPath); err != nil {
		dirPath = "."
	}
	if _, _, free, _ := GetDiskUsage(dirPath); free > 0 {
		return int64(free)
	}
	return 10 * 1024 * 1024 * 1024
}

// LimitExempt: the owner and the sudo users have no limits (like the Python bot).
func LimitExempt(userID int64) bool {
	cfg := config.Get()
	if userID != 0 && userID == cfg.OwnerID {
		return true
	}
	for _, sudo := range cfg.SudoUsers {
		if sudo == userID {
			return true
		}
	}
	return false
}

// CheckSizeLimits: like limit_checker of the Python bot, once the size of the task is known:
// the limit of its source (torrent / mega / ytdlp / direct), then LEECH_LIMIT (leech
// only), extract / archive, STORAGE_THRESHOLD and DAILY_LEECH_LIMIT. "" = fine.
func CheckSizeLimits(userID, size int64, kind string, isExtract, isCompress, isLeech bool) string {
	if size <= 0 || LimitExempt(userID) {
		return ""
	}
	cfg := config.Get()
	gb := int64(1024 * 1024 * 1024)
	over := func(limit int64, name string) string {
		if limit > 0 && size > limit*gb {
			return fmt.Sprintf("%s limit is %s.\nYour task size is %s.", name, GetReadableFileSize(limit*gb), GetReadableFileSize(size))
		}
		return ""
	}
	var e string
	switch kind {
	case "torrent":
		e = over(cfg.TorrentLimit, "Torrent")
	case "mega":
		e = over(cfg.MegaLimit, "Mega")
	case "ytdlp":
		e = over(cfg.YtdlpLimit, "YT-DLP")
	case "gdrive": // no GDRIVE_LIMIT in this bot: only the limits below
	default:
		e = over(cfg.DirectLimit, "Direct")
	}
	if e == "" && isLeech {
		e = over(cfg.LeechLimit, "Leech")
	}
	if e == "" && isExtract {
		e = over(cfg.ExtractLimit, "Extraction")
	}
	if e == "" && isCompress {
		e = over(cfg.ArchiveLimit, "Archive")
	}
	if e == "" && cfg.StorageThreshold > 0 && !CheckStorageThreshold(size, isExtract || isCompress) {
		e = fmt.Sprintf("You must leave %s free storage.", GetReadableFileSize(cfg.StorageThreshold*gb))
	}
	if e == "" && cfg.DailyLeechLimit > 0 {
		_, used := GetUserDailyStats(userID)
		if used+size > cfg.DailyLeechLimit*gb {
			e = fmt.Sprintf("Daily Leech Limit is %s.\nYou have used %s today. This task would exceed your daily limit.",
				GetReadableFileSize(cfg.DailyLeechLimit*gb), GetReadableFileSize(used))
		}
	}
	return e
}

// CheckTaskLimits verifies all user limits: single task, daily tasks, daily bandwidth, storage threshold
func CheckTaskLimits(userID int64, size int64, taskType string, isExtract, isCompress bool, isPlaylist int) string {
	cfg := config.Get()

	// Sudo users and Bot Owner are exempt from all limits
	if LimitExempt(userID) {
		return ""
	}

	// 1. Task-Specific Size Limits (in GB)
	if size > 0 {
		var limitBytes int64

		switch strings.ToLower(taskType) {
		case "torrent":
			if cfg.TorrentLimit > 0 {
				limitBytes = cfg.TorrentLimit * 1024 * 1024 * 1024
				if size > limitBytes {
					return fmt.Sprintf("Torrent limit is %s.\nYour task size is %s.", GetReadableFileSize(limitBytes), GetReadableFileSize(size))
				}
			}
		case "direct", "ddl":
			if cfg.DirectLimit > 0 {
				limitBytes = cfg.DirectLimit * 1024 * 1024 * 1024
				if size > limitBytes {
					return fmt.Sprintf("Direct download limit is %s.\nYour task size is %s.", GetReadableFileSize(limitBytes), GetReadableFileSize(size))
				}
			}
		case "ytdl", "ytdlp":
			if cfg.YtdlpLimit > 0 {
				limitBytes = cfg.YtdlpLimit * 1024 * 1024 * 1024
				if size > limitBytes {
					return fmt.Sprintf("YT-DLP limit is %s.\nYour task size is %s.", GetReadableFileSize(limitBytes), GetReadableFileSize(size))
				}
			}
		case "mega":
			if cfg.MegaLimit > 0 {
				limitBytes = cfg.MegaLimit * 1024 * 1024 * 1024
				if size > limitBytes {
					return fmt.Sprintf("Mega limit is %s.\nYour task size is %s.", GetReadableFileSize(limitBytes), GetReadableFileSize(size))
				}
			}
		}

		// Global Leech Limit
		if cfg.LeechLimit > 0 {
			leechMax := cfg.LeechLimit * 1024 * 1024 * 1024
			if size > leechMax {
				return fmt.Sprintf("Leech limit is %s.\nYour task size is %s.", GetReadableFileSize(leechMax), GetReadableFileSize(size))
			}
		}

		// Archive / Extract Limits
		if isExtract && cfg.ExtractLimit > 0 {
			extMax := cfg.ExtractLimit * 1024 * 1024 * 1024
			if size > extMax {
				return fmt.Sprintf("Extraction limit is %s.\nYour task size is %s.", GetReadableFileSize(extMax), GetReadableFileSize(size))
			}
		}
		if isCompress && cfg.ArchiveLimit > 0 {
			archMax := cfg.ArchiveLimit * 1024 * 1024 * 1024
			if size > archMax {
				return fmt.Sprintf("Archive compression limit is %s.\nYour task size is %s.", GetReadableFileSize(archMax), GetReadableFileSize(size))
			}
		}

		// 2. Storage Threshold Check (ensure server disk doesn't fill up)
		if !CheckStorageThreshold(size, isExtract || isCompress) {
			thresholdStr := GetReadableFileSize(cfg.StorageThreshold * 1024 * 1024 * 1024)
			return fmt.Sprintf("Storage threshold reached! Must leave %s free storage on server.", thresholdStr)
		}
	}

	// 3. Daily Task Count Limit
	tasksToday, bytesToday := GetUserDailyStats(userID)
	if cfg.DailyTaskLimit > 0 && tasksToday >= cfg.DailyTaskLimit {
		return fmt.Sprintf("Daily Task Limit is %d tasks.\nYou have exhausted your daily task limit for today.", cfg.DailyTaskLimit)
	}

	// 4. Daily Leech Data Limit
	if cfg.DailyLeechLimit > 0 && size > 0 {
		dailyMaxBytes := cfg.DailyLeechLimit * 1024 * 1024 * 1024
		if (bytesToday + size) > dailyMaxBytes {
			return fmt.Sprintf("Daily Leech Limit is %s.\nYou have used %s today. This task would exceed your daily limit.",
				GetReadableFileSize(dailyMaxBytes), GetReadableFileSize(bytesToday))
		}
	}

	return ""
}
