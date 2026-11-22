package ext

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// MakeSampleVideo cuts a short sample from the middle of a video (no re-encoding,
// so it starts at the nearest keyframe and can be a little longer than asked).
// It returns the sample path, or "" when the file is not a video.
func MakeSampleVideo(videoPath string, seconds int) string {
	if seconds <= 0 {
		return ""
	}
	details, err := ProbeMedia(videoPath)
	if err != nil || details == nil || details.VideoCodec == "" || details.Duration <= seconds {
		return ""
	}
	start := details.Duration/2 - seconds/2
	out := filepath.Join(filepath.Dir(videoPath), "SAMPLE."+filepath.Base(videoPath))
	cmd := exec.Command(FFmpegBin(),
		"-hide_banner", "-loglevel", "error",
		"-ss", strconv.Itoa(start),
		"-i", videoPath,
		"-t", strconv.Itoa(seconds),
		"-map", "0:v:0", "-map", "0:a?",
		"-c", "copy",
		"-avoid_negative_ts", "make_zero",
		out, "-y",
	)
	if err := cmd.Run(); err != nil {
		_ = os.Remove(out)
		return ""
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		_ = os.Remove(out)
		return ""
	}
	return out
}

// SampleName is the shown name of a sample file.
func SampleName(name string) string { return fmt.Sprintf("SAMPLE.%s", name) }
