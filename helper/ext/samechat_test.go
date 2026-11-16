package ext

import "testing"

func TestSameChat(t *testing.T) {
	if !SameChat(-1002530958965, 2530958965) || !SameChat(-123, 123) || !SameChat(55, 55) || SameChat(-1002530958965, 2530958966) {
		t.Fatal("SameChat")
	}
}
