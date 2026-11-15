package bot

import "github.com/gotd/td/tg"

// isFileMedia reports whether a message carries something to download.
// A link preview (web page) or an empty media is not a file: the link in the text is used instead.
func isFileMedia(m tg.MessageMediaClass) bool {
	switch m.(type) {
	case nil, *tg.MessageMediaEmpty, *tg.MessageMediaWebPage:
		return false
	}
	return true
}
