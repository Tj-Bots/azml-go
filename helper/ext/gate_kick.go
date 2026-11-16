package ext

import "sync"

// Gate kicks: a "join first / start the bot" message waits for its user; when the bot sees
// him join a channel or start the bot, it re-checks at once instead of at the next tick.

var (
	gateMu    sync.Mutex
	gateKicks = map[int64]chan struct{}{}
)

// GateWatch returns the channel a gate message waits on, and a function that drops it.
func GateWatch(userID int64) (<-chan struct{}, func()) {
	gateMu.Lock()
	ch, ok := gateKicks[userID]
	if !ok {
		ch = make(chan struct{}, 1)
		gateKicks[userID] = ch
	}
	gateMu.Unlock()
	return ch, func() {
		gateMu.Lock()
		if gateKicks[userID] == ch {
			delete(gateKicks, userID)
		}
		gateMu.Unlock()
	}
}

// GateKick wakes the gate message of this user (if any).
func GateKick(userID int64) {
	gateMu.Lock()
	ch, ok := gateKicks[userID]
	gateMu.Unlock()
	if ok {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
