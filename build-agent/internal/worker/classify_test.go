package worker

import "testing"

func TestClassifyFailure(t *testing.T) {
	cases := []struct {
		name       string
		console    string
		wantCode   string
		wantDetail string
	}{
		{
			name: "no dockerfile template error",
			console: "[2026-07-23T21:20:35.511Z] ERROR: framework 'dockerfile' has no template and repo ships no Dockerfile — add a Dockerfile to the repo or extend dadaBuildPipeline\n" +
				"[2026-07-23T21:20:35.552Z] Finished: FAILURE\n",
			wantCode:   buildFailNoDockerfile,
			wantDetail: "ERROR: framework 'dockerfile' has no template and repo ships no Dockerfile — add a Dockerfile to the repo or extend dadaBuildPipeline",
		},
		{
			name: "buildkit user code failure",
			console: "[2026-07-24T01:00:00.000Z] #12 3.212 error: unknown package\n" +
				"[2026-07-24T01:00:01.000Z] ERROR: failed to solve: process \"/bin/sh -c pip install -r requirements.txt\" did not complete successfully: exit code: 1\n" +
				"[2026-07-24T01:00:02.000Z] ERROR: script returned exit code 1\n" +
				"Finished: FAILURE\n",
			wantCode:   buildFailDockerfileBuild,
			wantDetail: "ERROR: failed to solve: process \"/bin/sh -c pip install -r requirements.txt\" did not complete successfully: exit code: 1",
		},
		{
			name: "unmatched failure falls back to last ERROR line",
			console: "[2026-07-24T02:00:00.000Z] fatal: Remote branch gone not found in upstream origin\n" +
				"[2026-07-24T02:00:01.000Z] ERROR: script returned exit code 128\n" +
				"[2026-07-24T02:00:02.000Z] Finished: FAILURE\n",
			wantCode:   buildFailGeneric,
			wantDetail: "script returned exit code 128",
		},
		{
			name:       "no error lines at all",
			console:    "[2026-07-24T03:00:00.000Z] something odd\nFinished: FAILURE\n",
			wantCode:   "",
			wantDetail: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, detail := classifyFailure(tc.console)
			if code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
			if detail != tc.wantDetail {
				t.Fatalf("detail = %q, want %q", detail, tc.wantDetail)
			}
		})
	}
}
