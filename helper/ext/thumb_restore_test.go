package ext

import (
	"os"
	"path/filepath"
	"testing"

	"azml-go/types"
)

func TestRestoreThumbnails(t *testing.T) {
	m := types.NewSafeUserMap()
	path := filepath.Join(t.TempDir(), "Thumbnails", "1.jpg")
	m.Set(&types.UserSettings{UserID: 1, CustomThumb: path, ThumbData: []byte("jpg")})
	RestoreThumbnails(m)
	if b, err := os.ReadFile(path); err != nil || string(b) != "jpg" {
		t.Fatalf("not restored: %v %q", err, b)
	}
}
