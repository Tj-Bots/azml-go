package ext

import (
	"os"
	"os/exec"
	"sync"
)

// TJ names of the tools: the Dockerfile installs yt-dlp as tjdl, ffmpeg as tjff,
// ffprobe as tjprobe and aria2c as tjurl, so the processes and the logs show them.
// Without them (local run, old image) the usual names are used.
const (
	YtdlBinName    = "tjdl"
	FFmpegBinName  = "tjff"
	FFprobeBinName = "tjprobe"
	// YtdlFFmpegDir holds tiny ffmpeg / ffprobe scripts that exec tjff / tjprobe:
	// yt-dlp looks for ffprobe next to ffmpeg by name, so it gets this folder
	// (--ffmpeg-location) and the running processes are still tjff / tjprobe.
	YtdlFFmpegDir = "/usr/local/lib/tjdl"
)

var binCache sync.Map

func pickBin(tj, orig string) string {
	if v, ok := binCache.Load(tj); ok {
		return v.(string)
	}
	name := orig
	if _, err := exec.LookPath(tj); err == nil {
		name = tj
	}
	binCache.Store(tj, name)
	return name
}

// YtdlBin is tjdl (yt-dlp).
func YtdlBin() string { return pickBin(YtdlBinName, "yt-dlp") }

// FFmpegBin is tjff (ffmpeg).
func FFmpegBin() string { return pickBin(FFmpegBinName, "ffmpeg") }

// FFprobeBin is tjprobe (ffprobe).
func FFprobeBin() string { return pickBin(FFprobeBinName, "ffprobe") }

// YtdlFFmpegArgs: --ffmpeg-location for yt-dlp when the TJ folder is there.
func YtdlFFmpegArgs() []string {
	if fi, err := os.Stat(YtdlFFmpegDir); err == nil && fi.IsDir() {
		return []string{"--ffmpeg-location", YtdlFFmpegDir}
	}
	return nil
}
