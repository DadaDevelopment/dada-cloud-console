package agentruntime

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestEmDashCorpusEval(t *testing.T) {
	path := os.Getenv("EMDASH_CORPUS")
	if path == "" {
		t.Skip("EMDASH_CORPUS not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	total, before, after, newLeaks := 0, 0, 0, 0
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
		if strings.Contains(row.Text, "—") {
			before++
		}
		fixed := stripEmDash(row.Text)
		if strings.Contains(fixed, "—") {
			after++
			t.Logf("STILL HAS DASH %s#%d: %s", row.Run, row.Turn, fixed)
		}
		if leakReason(row.Text) == "" && leakReason(fixed) != "" {
			newLeaks++
			t.Logf("NEW LEAK FLAG %s#%d: %s -> %s", row.Run, row.Turn, row.Text, fixed)
		}
	}
	t.Logf("total %d, em dash before %d, em dash after %d, new leak flags introduced by fix %d", total, before, after, newLeaks)
	if after != 0 {
		t.Fatalf("em dash still present in %d replies after stripEmDash", after)
	}
	if newLeaks != 0 {
		t.Fatalf("stripEmDash introduced %d new leak false positives", newLeaks)
	}
}
