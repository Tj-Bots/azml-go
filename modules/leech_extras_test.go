package modules

import "testing"

func TestCaptionFontAndExcluded(t *testing.T) {
	if got := applyCaptionFont("hi", "b"); got != "<b>hi</b>" {
		t.Fatal(got)
	}
	if got := applyCaptionFont("hi", "spoiler"); got != "<tg-spoiler>hi</tg-spoiler>" {
		t.Fatal(got)
	}
	if got := applyCaptionFont("hi", ""); got != "hi" {
		t.Fatal(got)
	}
	if !isExcludedFile("/x/Movie.NFO", ".nfo .txt") || isExcludedFile("/x/a.mkv", ".nfo") || isExcludedFile("/x/a.nfo", "") {
		t.Fatal("excluded check wrong")
	}
}
