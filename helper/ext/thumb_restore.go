package ext

import (
	"log"
	"os"
	"path/filepath"

	"azml-go/types"
)

// RestoreThumbnails writes the thumbnails kept in the database back to disk
// (on Heroku the disk is empty after every restart).
func RestoreThumbnails(userMap *types.SafeUserMap) {
	restored := 0
	for _, u := range userMap.GetAllUsers() {
		if u == nil || u.CustomThumb == "" || len(u.ThumbData) == 0 {
			continue
		}
		if _, err := os.Stat(u.CustomThumb); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(u.CustomThumb), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(u.CustomThumb, u.ThumbData, 0o644); err == nil {
			restored++
		}
	}
	if restored > 0 {
		log.Printf("[DB] Restored %d thumbnail(s) from the database", restored)
	}
}

// maxThumbBytes: bigger images are not kept in the database (MongoDB documents are limited to 16 MB).
const maxThumbBytes = 1 << 20

// ThumbBytes reads a thumbnail to keep it in the database, nil when missing or too big.
func ThumbBytes(path string) []byte {
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxThumbBytes {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}
