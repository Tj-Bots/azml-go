package modules

import (
	"testing"

	"azml-go/config"
	"azml-go/types"
)

func TestGDriveTarget(t *testing.T) {
	cfg := &config.Config{GDriveID: "BOT", IndexURL: "https://bot.idx/0:", DisableDriveLink: true}
	tok := []byte(`{"client_id":"c","client_secret":"s","refresh_token":"r"}`)

	o, err := gdriveTarget(cfg, &types.UserSettings{}, "")
	if err != nil || o.Client != nil || o.ParentID != "BOT" || o.IndexURL != cfg.IndexURL || !o.Private {
		t.Fatalf("owner: %+v %v", o, err)
	}
	u := &types.UserSettings{UserID: 1, GDriveID: "MINE", IndexURL: "https://my.idx/0:"}
	if o, _ := gdriveTarget(cfg, u, ""); o.Client != nil || o.ParentID != "MINE" || o.IndexURL != u.IndexURL {
		t.Fatalf("owner token, his folder: %+v", o)
	}
	u.UserTokens = true
	if _, err := gdriveTarget(cfg, u, ""); err == nil {
		t.Fatal("USER mode without token.pickle must fail")
	}
	u.TokenPickle = tok
	if o, err := gdriveTarget(cfg, u, ""); err != nil || o.Client == nil || o.ParentID != "MINE" || o.Private {
		t.Fatalf("user: %+v %v", o, err)
	}
	if o, err := gdriveTarget(cfg, &types.UserSettings{UserID: 2, TokenPickle: tok}, "mtp:F1"); err != nil || o.Client == nil || o.ParentID != "F1" {
		t.Fatalf("mtp: %+v %v", o, err)
	}
	if _, err := gdriveTarget(cfg, &types.UserSettings{UserID: 3, UserTokens: true, TokenPickle: tok}, ""); err == nil {
		t.Fatal("USER mode without his GDrive ID must fail")
	}
	if o, _ := gdriveTarget(cfg, &types.UserSettings{}, "tp:F2"); o.Client != nil || o.ParentID != "F2" {
		t.Fatalf("tp: %+v", o)
	}
}
