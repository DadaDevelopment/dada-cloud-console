package agentruntime

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func TestLeakCorpus(t *testing.T) {
	path := os.Getenv("LEAK_CORPUS")
	if path == "" {
		t.Skip("LEAK_CORPUS not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	allowlist := ParseLinkAllowlist(os.Getenv("LEAK_LINK_ALLOWLIST"))
	total, flagged, links := 0, 0, 0
	for sc.Scan() {
		var row struct {
			Run  string `json:"run"`
			Turn int    `json:"turn"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		total++
		reason := leakReason(row.Text)
		if reason == "" {
			if reason = linkLeakReason(row.Text, allowlist, false); reason != "" {
				links++
			}
		}
		if reason != "" {
			flagged++
			text := []rune(row.Text)
			if len(text) > 160 {
				text = text[:160]
			}
			t.Logf("%s#%d [%s]: %s", row.Run, row.Turn, reason, string(text))
		}
	}
	t.Logf("flagged %d of %d, links outside allowlist %d", flagged, total, links)
}
