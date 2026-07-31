package box

import (
	"strings"
	"sync"
	"testing"
)

// TestSyncBufferKeepsEveryWriteFromBothStreams is the regression for a box that
// was rejected with "canary output missing dada-ready marker" while the same
// command run by hand in the same pod printed it: two goroutines (remote stdout
// and remote stderr) wrote into one unsynchronized bytes.Buffer and bytes went
// missing. Run under -race this fails outright on the old form.
func TestSyncBufferKeepsEveryWriteFromBothStreams(t *testing.T) {
	const writesPerStream = 200

	out := &syncBuffer{}
	var wg sync.WaitGroup
	for _, line := range []string{"dada-ready\n", "node=v18.19.1\n"} {
		wg.Add(1)
		go func(line string) {
			defer wg.Done()
			for i := 0; i < writesPerStream; i++ {
				if _, err := out.Write([]byte(line)); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}(line)
	}
	wg.Wait()

	got := out.String()
	for _, line := range []string{"dada-ready\n", "node=v18.19.1\n"} {
		if n := strings.Count(got, line); n != writesPerStream {
			t.Fatalf("line %q appeared %d times, want %d", line, n, writesPerStream)
		}
	}
}
