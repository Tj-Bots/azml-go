package ext

import "testing"

func TestDebridHosts(t *testing.T) {
	if !hostIn("https://mega.nz/file/abc#def", debridLinkSites) {
		t.Fatal("mega must be a debrid-link host")
	}
	if hostIn("https://example.com/file.mkv", debridLinkSites) || hostIn("not a url", realDebridSites) {
		t.Fatal("unknown host matched")
	}
}

func TestPixeldrainLink(t *testing.T) {
	for in, want := range map[string]string{
		"https://pixeldrain.dev/u/tE1FVfwf": "https://pixeldrain.dev/api/file/tE1FVfwf?download",
		"https://pixeldrain.com/u/abc":      "https://pixeldrain.com/api/file/abc?download",
	} {
		if got, err := GenerateDirectLink(in); err != nil || got != want {
			t.Errorf("%s -> %s %v", in, got, err)
		}
	}
}
