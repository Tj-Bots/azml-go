package ext

import "sync"

// RingLog keeps the most recent bytes written to it, so /log can send them.
type RingLog struct {
	mu   sync.Mutex
	max  int
	data []byte
}

// LogBuffer holds the latest log output (roughly the last 512 KB).
var LogBuffer = &RingLog{max: 512 * 1024}

func (r *RingLog) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	if over := len(r.data) - r.max; over > 0 {
		r.data = r.data[over:]
		// start at a line boundary so the file does not begin in the middle of a line
		for i, c := range r.data {
			if c == '\n' {
				r.data = r.data[i+1:]
				break
			}
		}
	}
	return len(p), nil
}

// Bytes returns a copy of the buffered log.
func (r *RingLog) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.data...)
}

// Reset empties the buffer (/log clear).
func (r *RingLog) Reset() {
	r.mu.Lock()
	r.data = nil
	r.mu.Unlock()
}
