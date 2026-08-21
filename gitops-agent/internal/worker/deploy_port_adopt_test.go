package worker

import "testing"

// TestAdoptBuildDetectedPortFollowsAFrameworkChange is the affiliate-site case:
// the repo migrated from Vite to Next.js, the build detected nextjs on 3000,
// and the snapshot still carried the 4173 CreateApp guessed for the Vite app.
// Before adoption the render pointed the Service at 4173 and the app 502'd.
func TestAdoptBuildDetectedPortFollowsAFrameworkChange(t *testing.T) {
	cur := map[string]any{"framework": "nextjs", "port": float64(4173)}

	got, ok := adoptBuildDetectedPort(cur, false, 3000)
	if !ok {
		t.Fatalf("a guessed port must follow the framework the build detected")
	}
	if got != 3000 {
		t.Fatalf("adopted port = %v, want 3000", got)
	}
}

func TestAdoptBuildDetectedPortRefusals(t *testing.T) {
	cases := []struct {
		name     string
		cur      map[string]any
		worker   bool
		detected int
	}{
		{
			name: "a port the user typed is a contract, not a guess",
			cur: map[string]any{
				"framework": "nextjs", "port": float64(4173), "port_source": "user",
			},
			detected: 3000,
		},
		{
			name:     "a bespoke port on a legacy snapshot is left alone",
			cur:      map[string]any{"framework": "go", "port": float64(9317)},
			detected: 8080,
		},
		{
			name:     "worker apps have no HTTP port",
			cur:      map[string]any{"framework": "node", "port": float64(0), "worker": true},
			worker:   true,
			detected: 3000,
		},
		{
			name:     "a build that reports no port offers nothing",
			cur:      map[string]any{"framework": "vite", "port": float64(4173)},
			detected: 0,
		},
		{
			name:     "no change when the build agrees with the snapshot",
			cur:      map[string]any{"framework": "vite", "port": float64(4173)},
			detected: 4173,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := adoptBuildDetectedPort(tc.cur, tc.worker, tc.detected); ok {
				t.Fatalf("adoptBuildDetectedPort adopted %d, want refusal", tc.detected)
			}
		})
	}
}

func TestAdoptBuildDetectedPortHonoursRecordedAutoSource(t *testing.T) {
	cur := map[string]any{
		"framework": "go", "port": float64(9317), "port_source": "framework_default",
	}
	got, ok := adoptBuildDetectedPort(cur, false, 8080)
	if !ok || got != 8080 {
		t.Fatalf("recorded framework_default port must follow the build: got %v ok=%v", got, ok)
	}
}
