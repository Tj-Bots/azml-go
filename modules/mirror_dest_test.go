package modules

import (
	"os"
	"strings"
	"testing"

	"azml-go/config"
	"azml-go/helper/ext"
	"azml-go/types"
)

func TestMirrorDestError(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(wd) }()

	cfg := &config.Config{GDriveID: "ownerFolder"}
	user := &types.UserSettings{UserID: 1}

	// nothing set by the owner (no token.pickle) and nothing by him
	if e := mirrorDestError(cfg, user, ext.GDriveHostID, ""); !strings.Contains(e, "NO TOKEN") {
		t.Fatalf("no token: %q", e)
	}
	// USER mode without his token
	user.UserTokens = true
	if e := mirrorDestError(cfg, user, ext.GDriveHostID, ""); !strings.Contains(e, "NO TOKEN") {
		t.Fatalf("user no token: %q", e)
	}
	// USER mode with token but no GDrive ID: the owner's folder is not his
	user.TokenPickle = []byte("x")
	if e := mirrorDestError(cfg, user, ext.GDriveHostID, ""); !strings.Contains(e, "No Upload Destination") {
		t.Fatalf("user no id: %q", e)
	}
	if e := mirrorDestError(cfg, user, ext.GDriveHostID, "mtp:abc"); e != "" {
		t.Fatalf("mtp: %q", e)
	}
	// OWNER mode with the bot's token.pickle and GDRIVE_ID: fine
	user.UserTokens = false
	_ = os.WriteFile("token.pickle", []byte("x"), 0o600)
	if e := mirrorDestError(cfg, user, ext.GDriveHostID, ""); e != "" {
		t.Fatalf("owner: %q", e)
	}
	// RClone: no path, then no rclone.conf
	if e := mirrorDestError(cfg, user, ext.RcloneHostID, ""); !strings.Contains(e, "No Rclone Destination") {
		t.Fatalf("rc no dest: %q", e)
	}
	if e := mirrorDestError(cfg, user, ext.RcloneHostID, "r:path"); !strings.Contains(e, "rclone.conf not Exists") {
		t.Fatalf("rc no conf: %q", e)
	}
	_ = os.WriteFile("rclone.conf", []byte("x"), 0o600)
	if e := mirrorDestError(cfg, user, ext.RcloneHostID, "r:path"); e != "" {
		t.Fatalf("rc ok: %q", e)
	}
	// other hosts are not checked
	if e := mirrorDestError(cfg, user, "gofile", ""); e != "" {
		t.Fatalf("ddl: %q", e)
	}
}
