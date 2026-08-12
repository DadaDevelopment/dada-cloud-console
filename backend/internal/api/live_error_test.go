package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSetLiveErrorFramesTheFailure(t *testing.T) {
	resp := gin.H{"online": false}
	setLiveError(resp, "portainer: 502 Bad Gateway")

	if resp["live_error"] != "portainer: 502 Bad Gateway" {
		t.Fatalf("human message lost: %v", resp["live_error"])
	}
	if resp["live_error_scope"] != "platform_data_source" {
		t.Fatalf("scope missing: %v", resp["live_error_scope"])
	}
	note, _ := resp["live_error_note"].(string)
	if !strings.Contains(note, "not a diagnosis of the user's application") {
		t.Fatalf("note does not disclaim its own nature: %q", note)
	}
}

func TestSetLiveErrorIgnoresEmpty(t *testing.T) {
	resp := gin.H{"online": true}
	setLiveError(resp, "")
	for _, k := range []string{"live_error", "live_error_scope", "live_error_note"} {
		if _, ok := resp[k]; ok {
			t.Fatalf("empty error still set %s", k)
		}
	}
}

// TestLiveErrorIsOnlyEverSetThroughHelper keeps the framing from being bypassed
// by a future handler that writes the raw field directly: an HTTP 200 carrying
// a bare upstream error reads to the model as a finding about the user's app.
func TestLiveErrorIsOnlyEverSetThroughHelper(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "live_error.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, `"live_error"`) && strings.Contains(line, "=") {
				t.Errorf("%s:%d assigns live_error directly, use setLiveError: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
