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
