package ext

import "sync"

var (
	noteMu sync.Mutex
	notes  = map[int64]string{}
)

// SetSettingNote leaves a one-time line for the next /us page of the user (e.g. a login error).
func SetSettingNote(uid int64, note string) {
	noteMu.Lock()
	notes[uid] = note
	noteMu.Unlock()
}

// TakeSettingNote returns and clears it.
func TakeSettingNote(uid int64) string {
	noteMu.Lock()
	defer noteMu.Unlock()
	n := notes[uid]
	delete(notes, uid)
	return n
}
