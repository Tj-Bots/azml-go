package downloader

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TJ RClone (tjrc): /mirror -up rc or -up remote:path, like the Python bot: the file is copied
// with rclone (installed as tjrc) to the remote of rclone.conf.

// RcloneBin: the rclone binary of the Dockerfile.
const RcloneBin = "tjrc"

// RcloneBinary: tjrc, else rclone when it is installed under its own name.
func RcloneBinary() string {
	for _, b := range []string{RcloneBin, "rclone"} {
		if p, err := exec.LookPath(b); err == nil {
			return p
		}
	}
	return RcloneBin
}

// RcloneSession: one task; with a folder name all its files go into remote:path/<folder>.
type RcloneSession struct {
	FolderName string
}

// RcloneResult: where the file is and its link (when the remote gives one).
type RcloneResult struct {
	Path string
	Link string
}

// UploadToRclone: rclone copy <file> <dest>[/<folder>], the progress from its JSON stats.
func UploadToRclone(ctx context.Context, path, dest, confPath, flags string, sess *RcloneSession, onProgress func(sent, total int64, speed float64)) (RcloneResult, error) {
	dest = strings.TrimRight(strings.TrimSpace(dest), "/")
	if dest == "" || !strings.Contains(dest, ":") {
		return RcloneResult{}, fmt.Errorf("no RClone path: set RCLONE_PATH or use -up remote:path")
	}
	if sess != nil && sess.FolderName != "" {
		dest += "/" + sess.FolderName
	}
	args := []string{"copy", path, dest, "--config", confPath, "--use-json-log", "--stats", "2s", "--stats-log-level", "NOTICE", "--log-level", "NOTICE"}
	if f := strings.Fields(flags); len(f) > 0 {
		args = append(args, f...)
	}
	cmd := exec.CommandContext(ctx, RcloneBinary(), args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RcloneResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return RcloneResult{}, fmt.Errorf("%s: %v (is rclone installed?)", RcloneBin, err)
	}
	var lastErr string
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var l struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
			Stats *struct {
				Bytes      int64   `json:"bytes"`
				TotalBytes int64   `json:"totalBytes"`
				Speed      float64 `json:"speed"`
			} `json:"stats"`
		}
		if json.Unmarshal(line, &l) != nil {
			if s := strings.TrimSpace(string(line)); s != "" {
				lastErr = s
			}
			continue
		}
		if l.Stats != nil && onProgress != nil {
			onProgress(l.Stats.Bytes, l.Stats.TotalBytes, l.Stats.Speed)
		}
		if l.Level == "error" || l.Level == "critical" {
			lastErr = l.Msg
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return RcloneResult{}, ctx.Err()
		}
		if lastErr == "" {
			lastErr = err.Error()
		}
		return RcloneResult{}, fmt.Errorf("rclone: %s", lastErr)
	}
	res := RcloneResult{Path: dest + "/" + filepath.Base(path)}
	if sess != nil && sess.FolderName != "" {
		res.Path = dest
	}
	// a share link, when the remote has one (Drive, OneDrive, Mega ...)
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	lc := exec.CommandContext(lctx, RcloneBinary(), "link", res.Path, "--config", confPath)
	lc.Stdout = &out
	if lc.Run() == nil {
		if l := strings.TrimSpace(out.String()); strings.HasPrefix(l, "http") {
			res.Link = l
		}
	}
	return res, nil
}
