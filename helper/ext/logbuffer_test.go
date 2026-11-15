package ext

import (
	"strings"
	"testing"
)

func TestRingLogKeepsTail(t *testing.T) {
	r := &RingLog{max: 50}
	for i := 0; i < 20; i++ {
		_, _ = r.Write([]byte("line number " + string(rune('A'+i)) + "\n"))
	}
	got := string(r.Bytes())
	if len(got) > 50 || !strings.HasSuffix(got, "line number T\n") || !strings.HasPrefix(got, "line number ") {
		t.Fatalf("unexpected buffer: %q", got)
	}
}
