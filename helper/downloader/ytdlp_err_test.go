package downloader

import (
	"errors"
	"testing"
)

func TestYtdlpError(t *testing.T) {
	got := ytdlpError("[youtube] x: Downloading\nERROR: [youtube] x: Sign in to confirm you're not a bot\n", errors.New("exit status 1"))
	if got != "[youtube] x: Sign in to confirm you're not a bot" {
		t.Fatalf("got %q", got)
	}
	if ytdlpError("", errors.New("exit status 2")) != "yt-dlp failed: exit status 2" {
		t.Fatal("fallback")
	}
}
