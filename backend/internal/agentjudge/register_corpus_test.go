package agentjudge

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func TestRegisterCorpus(t *testing.T) {
	path := os.Getenv("REGISTER_CORPUS")
	if path == "" {
		t.Skip("REGISTER_CORPUS not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var row struct {
			File   string   `json:"file"`
			Client []string `json:"client"`
			Reply  string   `json:"reply"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		reg := ClientRegister(row.Client)
		if w := RegisterMismatch(row.Reply, reg); w != "" {
			t.Logf("%s [%s] «%s» :: %.160s", row.File, reg, w, row.Reply)
		}
	}
}
