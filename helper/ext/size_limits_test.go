package ext

import (
	"strings"
	"testing"

	"azml-go/config"
)

func TestCheckSizeLimits(t *testing.T) {
	cfg := config.Get()
	old := *cfg
	defer func() { *cfg = old }()
	cfg.OwnerID, cfg.SudoUsers = 1, []int64{2}
	cfg.DirectLimit, cfg.LeechLimit, cfg.TorrentLimit, cfg.StorageThreshold, cfg.DailyLeechLimit = 100, 50, 0, 0, 0
	gb := int64(1024 * 1024 * 1024)

	if e := CheckSizeLimits(7, 234*gb, "direct", false, false, true); !strings.Contains(e, "Direct limit is 100") {
		t.Fatalf("direct: %q", e)
	}
	if e := CheckSizeLimits(7, 60*gb, "direct", false, false, true); !strings.Contains(e, "Leech limit is 50") {
		t.Fatalf("leech: %q", e)
	}
	if e := CheckSizeLimits(7, 60*gb, "direct", false, false, false); e != "" {
		t.Fatalf("mirror has no LEECH_LIMIT: %q", e)
	}
	if e := CheckSizeLimits(7, 234*gb, "torrent", false, false, false); e != "" {
		t.Fatalf("torrent without TORRENT_LIMIT: %q", e)
	}
	for _, id := range []int64{1, 2} {
		if e := CheckSizeLimits(id, 234*gb, "direct", false, false, true); e != "" {
			t.Fatalf("owner/sudo %d: %q", id, e)
		}
	}
}
