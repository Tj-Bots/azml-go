package ext

import (
	"sync"
	"time"
)

type PendingThumbSession struct {
	ChatID      int64
	PromptMsgID int
	CmdMsgID    int // the /t command (0 for the /us menu)
	ExpiresAt   time.Time
}

type thumbStateManager struct {
	mu      sync.RWMutex
	pending map[int64]*PendingThumbSession
}

var globalThumbState = &thumbStateManager{
	pending: make(map[int64]*PendingThumbSession),
}

// SetPendingThumbSession registers that userID is expected to upload a custom thumbnail within the given duration
func SetPendingThumbSession(userID int64, chatID int64, promptMsgID int, d time.Duration) {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	globalThumbState.pending[userID] = &PendingThumbSession{
		ChatID:      chatID,
		PromptMsgID: promptMsgID,
		ExpiresAt:   time.Now().Add(d),
	}
}

// IsPendingThumbSession checks if userID has an active pending thumbnail upload session
func IsPendingThumbSession(userID int64, chatID ...int64) bool {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	sess, exists := globalThumbState.pending[userID]
	if !exists {
		return false
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(globalThumbState.pending, userID)
		return false
	}
	if len(chatID) > 0 && chatID[0] != 0 && sess.ChatID != 0 {
		// If sent in user PM (chatID == userID), always accept
		if chatID[0] != userID && !SameChat(sess.ChatID, chatID[0]) {
			return false
		}
	}
	return true
}

// GetPendingThumbSession returns the active session for userID, if any
func GetPendingThumbSession(userID int64, chatID ...int64) (*PendingThumbSession, bool) {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	sess, exists := globalThumbState.pending[userID]
	if !exists {
		return nil, false
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(globalThumbState.pending, userID)
		return nil, false
	}
	if len(chatID) > 0 && chatID[0] != 0 && sess.ChatID != 0 {
		// If sent in user PM (chatID == userID), always accept
		if chatID[0] != userID && !SameChat(sess.ChatID, chatID[0]) {
			return nil, false
		}
	}
	return sess, true
}

// ClearPendingThumbSession removes any pending session for userID
func ClearPendingThumbSession(userID int64) {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	delete(globalThumbState.pending, userID)
}

// SameChat compares chat ids written in any form: a group can come as -1001234 (Bot API style)
// or as 1234 (the raw id of the update), and both are the same chat.
func SameChat(a, b int64) bool {
	return rawChatID(a) == rawChatID(b)
}

func rawChatID(id int64) int64 {
	if id >= 0 {
		return id
	}
	if id <= -1000000000000 {
		return -id - 1000000000000
	}
	return -id
}

// SetThumbCommandSession: /t waits for a photo; promptMsgID is the bot's prompt, cmdMsgID the /t.
func SetThumbCommandSession(userID, chatID int64, promptMsgID, cmdMsgID int, d time.Duration) {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	globalThumbState.pending[userID] = &PendingThumbSession{
		ChatID:      chatID,
		PromptMsgID: promptMsgID,
		CmdMsgID:    cmdMsgID,
		ExpiresAt:   time.Now().Add(d),
	}
}

// TakeThumbPrompt: true (and the session is removed) when the user still has the /t session of
// this prompt, also after it expired; false when it was saved or cancelled meanwhile.
func TakeThumbPrompt(userID int64, promptMsgID int) bool {
	globalThumbState.mu.Lock()
	defer globalThumbState.mu.Unlock()
	if s, ok := globalThumbState.pending[userID]; ok && s != nil && s.PromptMsgID == promptMsgID {
		delete(globalThumbState.pending, userID)
		return true
	}
	return false
}
