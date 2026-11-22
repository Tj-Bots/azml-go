package downloader

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"azml-go/helper/ext"
	"azml-go/types"
)

var (
	ytProgressReg = regexp.MustCompile(`\[download\]\s+(\d+\.?\d*)%\s+of\s+~?(\d+\.?\d*)([KMGTP]?i?B)\s+at\s+(\d+\.?\d*)([KMGTP]?i?B/s)\s+ETA\s+(\d+:\d+)`)
	ytDestReg     = regexp.MustCompile(`\[download\] Destination: (.+)`)
	ytMergerReg   = regexp.MustCompile(`\[Merger\] Merging formats into "(.+)"`)
)

type YtDlpDownloader struct{}

func NewYtDlpDownloader() *YtDlpDownloader {
	return &YtDlpDownloader{}
}

func (y *YtDlpDownloader) GetInfo(ctx context.Context, link string) (title string, isPlaylist bool, count int, err error) {
	cmd := exec.CommandContext(ctx, ext.YtdlBin(), "--dump-single-json", "--flat-playlist", link)
	out, err := cmd.Output()
	if err != nil {
		return "", false, 0, err
	}

	var data struct {
		Title   string        `json:"title"`
		Type    string        `json:"_type"`
		Entries []interface{} `json:"entries"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return "", false, 0, err
	}

	if data.Type == "playlist" || len(data.Entries) > 0 {
		return data.Title, true, len(data.Entries), nil
	}
	return data.Title, false, 1, nil
}

func (y *YtDlpDownloader) Download(ctx context.Context, link, outDir, customFormat string, onProgress func(cur, total int64, speed float64, eta string), extraArgs ...string) (string, error) {
	_ = os.MkdirAll(outDir, 0755)

	outTmpl := filepath.Join(outDir, "%(title)s.%(ext)s")
	args := []string{
		"-o", outTmpl,
		"--newline",
		"--no-warnings",
		"--no-part",
		"--concurrent-fragments", "16", // Max speed parallel fragment download
		"--trim-filenames", "180",
		"--no-mtime",
	}

	if customFormat != "" {
		args = append(args, "-f", customFormat)
	} else {
		args = append(args, "-f", "bv*+ba/b")
	}

	if _, err := os.Stat("cookies.txt"); err == nil {
		args = append(args, "--cookies", "cookies.txt")
	}
	args = append(args, ext.YtdlFFmpegArgs()...) // tjff / tjprobe
	args = append(args, extraArgs...)

	args = append(args, link)

	cmd := exec.CommandContext(ctx, ext.YtdlBin(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr strings.Builder
	cmd.Stderr = &tailWriter{b: &stderr, max: 4000}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(stdout)
	var downloadedFile string

	for scanner.Scan() {
		line := scanner.Text()
		if m := ytDestReg.FindStringSubmatch(line); len(m) > 1 {
			downloadedFile = m[1]
		}
		if m := ytMergerReg.FindStringSubmatch(line); len(m) > 1 {
			downloadedFile = m[1]
		}
		if m := ytProgressReg.FindStringSubmatch(line); len(m) > 6 {
			pct, _ := strconv.ParseFloat(m[1], 64)
			sizeVal, _ := strconv.ParseFloat(m[2], 64)
			unit := m[3]
			speedVal, _ := strconv.ParseFloat(m[4], 64)
			eta := m[6]

			totalBytes := parseBytes(sizeVal, unit)
			curBytes := int64((pct / 100.0) * float64(totalBytes))
			speedBytes := float64(parseBytes(speedVal, m[5]))

			if onProgress != nil {
				onProgress(curBytes, totalBytes, speedBytes, eta)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%s", ytdlpError(stderr.String(), err))
	}
	// with audio extraction the final file has another extension
	if downloadedFile != "" {
		if _, err := os.Stat(downloadedFile); err != nil {
			downloadedFile = ""
		}
	}

	if downloadedFile != "" {
		return downloadedFile, nil
	}

	// Scan outDir for any created file
	files, _ := os.ReadDir(outDir)
	if len(files) > 0 {
		return filepath.Join(outDir, files[0].Name()), nil
	}

	return "", errors.New("yt-dlp completed but output file not found")
}

func parseBytes(val float64, unit string) int64 {
	unit = strings.ToUpper(unit)
	multiplier := float64(1)
	switch {
	case strings.HasPrefix(unit, "K"):
		multiplier = 1024
	case strings.HasPrefix(unit, "M"):
		multiplier = 1024 * 1024
	case strings.HasPrefix(unit, "G"):
		multiplier = 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "T"):
		multiplier = 1024 * 1024 * 1024 * 1024
	}
	return int64(val * multiplier)
}

type YtDlpDownloadTask struct {
	name      string
	size      int64
	curr      int64
	speed     float64
	eta       string
	startTime time.Time
	status    types.TaskStatus
	cancelFn  context.CancelFunc
	mu        sync.RWMutex
}

func (t *YtDlpDownloadTask) GID() string              { return "ytdlp" }
func (t *YtDlpDownloadTask) Name() string             { return t.name }
func (t *YtDlpDownloadTask) Size() int64              { return t.size }
func (t *YtDlpDownloadTask) DownloadedBytes() int64   { return t.curr }
func (t *YtDlpDownloadTask) Speed() float64           { return t.speed }
func (t *YtDlpDownloadTask) Status() types.TaskStatus { return t.status }
func (t *YtDlpDownloadTask) Engine() string           { return "yt-dlp [16frags]" }
func (t *YtDlpDownloadTask) StartTime() time.Time     { return t.startTime }
func (t *YtDlpDownloadTask) MessageID() int           { return 0 }
func (t *YtDlpDownloadTask) ChatID() int64            { return 0 }
func (t *YtDlpDownloadTask) UserID() int64            { return 0 }
func (t *YtDlpDownloadTask) Progress() float64 {
	if t.size <= 0 {
		return 0
	}
	return (float64(t.curr) / float64(t.size)) * 100
}
func (t *YtDlpDownloadTask) ETA() string {
	if t.eta != "" {
		return t.eta
	}
	if t.speed <= 0 || t.size <= 0 {
		return "-"
	}
	rem := t.size - t.curr
	if rem <= 0 {
		return "0s"
	}
	return ext.GetReadableTime(int64(float64(rem) / t.speed))
}
func (t *YtDlpDownloadTask) Cancel() error {
	if t.cancelFn != nil {
		t.cancelFn()
	}
	return nil
}

// tailWriter keeps only the end of a long output.
type tailWriter struct {
	b   *strings.Builder
	max int
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.b.Write(p)
	if t.b.Len() > t.max*2 {
		rest := t.b.String()[t.b.Len()-t.max:]
		t.b.Reset()
		t.b.WriteString(rest)
	}
	return len(p), nil
}

// ytdlpError: the last "ERROR:" line of yt-dlp, or the exit error.
func ytdlpError(stderr string, err error) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "ERROR:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "ERROR:"))
		}
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		return strings.TrimSpace(lines[len(lines)-1])
	}
	return "yt-dlp failed: " + err.Error()
}

// YtFormat is one format from yt-dlp -J.
type YtFormat struct {
	ID       string  `json:"format_id"`
	Ext      string  `json:"ext"`
	VideoExt string  `json:"video_ext"`
	AudioExt string  `json:"audio_ext"`
	Vcodec   string  `json:"vcodec"`
	Acodec   string  `json:"acodec"`
	Height   int     `json:"height"`
	Fps      float64 `json:"fps"`
	Tbr      float64 `json:"tbr"`
	Size     int64   `json:"filesize"`
	Approx   int64   `json:"filesize_approx"`
}

// YtFormatInfo is what the quality menu needs.
type YtFormatInfo struct {
	Title      string
	IsPlaylist bool
	Count      int
	Duration   float64
	Formats    []YtFormat
}

// GetFormats reads the formats of a link (yt-dlp -J).
func (y *YtDlpDownloader) GetFormats(ctx context.Context, link string, extraArgs ...string) (*YtFormatInfo, error) {
	args := []string{"-J", "--no-warnings", "--flat-playlist"}
	if _, err := os.Stat("cookies.txt"); err == nil {
		args = append(args, "--cookies", "cookies.txt")
	}
	args = append(args, extraArgs...)
	args = append(args, link)
	cmd := exec.CommandContext(ctx, ext.YtdlBin(), args...)
	var stderr strings.Builder
	cmd.Stderr = &tailWriter{b: &stderr, max: 4000}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s", ytdlpError(stderr.String(), err))
	}
	return parseYtJSON(out)
}

func parseYtJSON(out []byte) (*YtFormatInfo, error) {
	var data struct {
		Title    string        `json:"title"`
		Type     string        `json:"_type"`
		Entries  []interface{} `json:"entries"`
		Duration float64       `json:"duration"`
		Formats  []YtFormat    `json:"formats"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, err
	}
	info := &YtFormatInfo{Title: data.Title, Duration: data.Duration, Formats: data.Formats}
	if data.Type == "playlist" || len(data.Entries) > 0 {
		info.IsPlaylist = true
		info.Count = len(data.Entries)
		info.Formats = nil
	}
	return info, nil
}

// YtQuality turns a menu choice ("best", "1080", "audio", "mp3") into yt-dlp options.
func YtQuality(choice string) (format string, extra []string) {
	if strings.HasPrefix(choice, "ba/b-") {
		parts := strings.Split(choice, "-")
		extra = []string{"-x"}
		if len(parts) > 1 && parts[1] != "" {
			extra = append(extra, "--audio-format", parts[1])
		}
		if len(parts) > 2 && parts[2] != "" {
			q := parts[2]
			if parts[1] == "mp3" && q != "0" && len(q) > 1 {
				q += "K" // bitrate (64, 128, 320); 0-10 is VBR quality
			}
			extra = append(extra, "--audio-quality", q)
		}
		return "ba/b", extra
	}
	switch choice {
	case "", "best":
		return "bv*+ba/b", nil
	case "audio":
		return "ba/b", []string{"-x"}
	case "mp3":
		return "ba/b", []string{"-x", "--audio-format", "mp3", "--audio-quality", "320K"}
	}
	h := strings.TrimSuffix(strings.ToLower(choice), "p")
	if _, err := strconv.Atoi(h); err == nil {
		return fmt.Sprintf("bv*[height<=%s]+ba/b[height<=%s]/b", h, h), nil
	}
	return choice, nil // a raw yt-dlp format given by the user
}
