package modules

import (
	"encoding/json"
	"testing"
)

func TestFormatMediaInfoNodes(t *testing.T) {
	var nodes []mediaInfoNode
	if err := json.Unmarshal([]byte(formatMediaInfoHTML("a.mkv", "General\nFormat : Matroska\n\nVideo\nCodec : AVC")), &nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 5 || nodes[0].Tag != "h4" || nodes[1].Tag != "h4" || nodes[2].Tag != "pre" || nodes[3].Tag != "h4" || nodes[4].Tag != "pre" {
		t.Fatalf("unexpected nodes: %+v", nodes)
	}
}
