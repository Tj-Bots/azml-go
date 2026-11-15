package bot

import "testing"

func TestSplitMirrorHost(t *testing.T) {
	text, host := splitMirrorHost("/mirror https://x.y/file -up buzzheavier -n name")
	if host != "buzzheavier" || text != "/mirror https://x.y/file -n name" {
		t.Fatalf("got %q %q", text, host)
	}
	text, host = splitMirrorHost("/m https://x.y/file")
	if host != "" || text != "/m https://x.y/file" {
		t.Fatalf("got %q %q", text, host)
	}
	if _, host = splitMirrorHost("/m link -up nope"); host != "" {
		t.Fatalf("unknown host accepted: %q", host)
	}
}
