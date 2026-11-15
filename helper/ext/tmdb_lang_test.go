package ext

import "testing"

func TestSelectBestImageLanguage(t *testing.T) {
	he, en := "he", "en"
	res := tmdbImagesResult{Posters: []tmdbImageItem{{FilePath: "/none"}, {FilePath: "/en", ISO6391: &en}, {FilePath: "/he", ISO6391: &he}}}
	if got := selectBestImagePath(res, true, "he"); got != "/he" {
		t.Fatalf("hebrew user got %s", got)
	}
	if got := selectBestImagePath(res, true, ""); got != "/en" {
		t.Fatalf("no language got %s", got)
	}
	if imageLanguages("he-IL") != "he,en,null" || imageLanguages("en") != "en,null" {
		t.Fatal("languages")
	}
}
