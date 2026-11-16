package modules

import "testing"

func TestIsHLSLink(t *testing.T) {
	for l, want := range map[string]bool{
		"https://x.com/a/master.m3u8":          true,
		"https://x.com/a/index.M3U8?token=1":   true,
		"https://x.com/hls/playlist.m3u8/clip": true,
		"https://x.com/v.mp4":                  false,
		"https://x.com/get?format=m3u8":        true,
		"magnet:?xt=urn:btih:abc":              false,
	} {
		if isHLSLink(l) != want {
			t.Errorf("%s: want %v", l, want)
		}
	}
}
