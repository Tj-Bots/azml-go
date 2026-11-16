package ext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"azml-go/config"
)

// Engine names with versions for the status, like "MegaSDK v4.8.0" in the Python bot.

var (
	engineMu    sync.Mutex
	engineCache = map[string]string{}
)

func cachedEngine(key string, get func() string) string {
	engineMu.Lock()
	v, ok := engineCache[key]
	engineMu.Unlock()
	if ok {
		return v
	}
	v = get()
	engineMu.Lock()
	engineCache[key] = v
	engineMu.Unlock()
	return v
}

func moduleVersion(path string) string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == path {
				return d.Version
			}
		}
	}
	return ""
}

// EngineName: "telegram", "aria2", "ytdlp", "mega", "gdrive", "direct".
func EngineName(kind string) string {
	switch kind {
	case "telegram":
		return cachedEngine(kind, func() string {
			if v := moduleVersion("github.com/gotd/td"); v != "" {
				return "Gotd MTProto " + v
			}
			return "Gotd MTProto"
		})
	case "aria2":
		return cachedEngine(kind, func() string {
			if v := aria2Version(); v != "" {
				return "Aria2c v" + v
			}
			return "Aria2c"
		})
	case "ytdlp":
		return cachedEngine(kind, func() string {
			out, err := exec.Command("yt-dlp", "--version").Output()
			if v := strings.TrimSpace(string(out)); err == nil && v != "" {
				return "yt-dlp v" + v
			}
			return "yt-dlp"
		})
	case "mega":
		return "Mega API (8 connections)"
	case "gdrive":
		return "Google Drive API v3"
	case "direct":
		return "Go HTTP"
	}
	return kind
}

func aria2Version() string {
	port := config.Get().Aria2Port
	if port == 0 {
		port = 6810
	}
	body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": "v", "method": "aria2.getVersion", "params": []interface{}{}})
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Post(fmt.Sprintf("http://127.0.0.1:%d/jsonrpc", port), "application/json", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		Result struct {
			Version string `json:"version"`
		} `json:"result"`
	}
	if json.NewDecoder(resp.Body).Decode(&r) != nil {
		return ""
	}
	return r.Result.Version
}
