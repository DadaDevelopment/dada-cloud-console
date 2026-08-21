package worker

import (
	"os"
	"path/filepath"
	"testing"
)

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
			name: "empty framework gets its own verdict, not the no-Dockerfile one",
			console: "[2026-08-13T09:12:03.100Z] ERROR: framework '' has no template and repo ships no Dockerfile — add a Dockerfile to the repo or extend dadaBuildPipeline\n" +
				"[2026-08-13T09:12:03.140Z] Finished: FAILURE\n",
			wantCode:   buildFailFrameworkUndetected,
			wantDetail: frameworkUndetectedDetail,
		},
		{
			name: "buildkit user code failure reports what the step printed, not the wrapper",
			console: "[2026-07-24T01:00:00.000Z] #12 3.212 error: unknown package\n" +
				"[2026-07-24T01:00:01.000Z] ERROR: failed to solve: process \"/bin/sh -c pip install -r requirements.txt\" did not complete successfully: exit code: 1\n" +
				"[2026-07-24T01:00:02.000Z] ERROR: script returned exit code 1\n" +
				"Finished: FAILURE\n",
			wantCode:   buildFailDockerfileBuild,
			wantDetail: "error: unknown package",
		},
		{
			name: "unmatched failure reports the cause above the exit-code wrapper",
			console: "[2026-07-24T02:00:00.000Z] fatal: Remote branch gone not found in upstream origin\n" +
				"[2026-07-24T02:00:01.000Z] ERROR: script returned exit code 128\n" +
				"[2026-07-24T02:00:02.000Z] Finished: FAILURE\n",
			wantCode:   buildFailGeneric,
			wantDetail: "fatal: Remote branch gone not found in upstream origin",
		},
		{
			name: "library checkout that ran out of retries is ours, not the user's code",
			console: "[2026-08-13T10:39:29.467Z] ERROR: Maximum checkout retry attempts reached, aborting\n" +
				"[2026-08-13T10:39:29.467Z] org.codehaus.groovy.control.MultipleCompilationErrorsException: startup failed:\n" +
				"[2026-08-13T10:39:29.467Z] WorkflowScript: Loading libraries failed\n" +
				"[2026-08-13T10:39:29.525Z] Finished: FAILURE\n",
			wantCode:   buildFailPlatformError,
			wantDetail: "ERROR: Maximum checkout retry attempts reached, aborting",
		},
		{
			name: "private repo without a stored credential",
			console: "[2026-08-02T12:51:22.520Z] + git clone --depth 1 --branch master https://github.com/DadaDevelopment/dada-development-site.git src\n" +
				"[2026-08-02T12:51:22.520Z] Cloning into 'src'...\n" +
				"[2026-08-02T12:51:22.783Z] fatal: could not read Username for 'https://github.com': No such device or address\n" +
				"[2026-08-02T12:51:23.653Z] ERROR: script returned exit code 128\n" +
				"[2026-08-02T12:51:23.697Z] Finished: FAILURE\n",
			wantCode:   buildFailGitAuth,
			wantDetail: "fatal: could not read Username for 'https://github.com': No such device or address",
		},
		{
			name: "revoked token on an https clone",
			console: "[2026-08-02T09:00:00.000Z] remote: Invalid username or password.\n" +
				"[2026-08-02T09:00:00.100Z] fatal: Authentication failed for 'https://github.com/acme/app.git/'\n" +
				"[2026-08-02T09:00:01.000Z] ERROR: script returned exit code 128\n" +
				"Finished: FAILURE\n",
			wantCode:   buildFailGitAuth,
			wantDetail: "fatal: Authentication failed for 'https://github.com/acme/app.git/'",
		},
		{
			name: "registry auth inside the image build is not a git failure",
			console: "[2026-08-02T10:00:00.000Z] #8 1.2 npm error code E401\n" +
				"[2026-08-02T10:00:00.500Z] #8 1.3 npm error Incorrect or missing password.\n" +
				"[2026-08-02T10:00:01.000Z] ERROR: failed to solve: process \"/bin/sh -c npm ci\" did not complete successfully: exit code: 1\n" +
				"Finished: FAILURE\n",
			wantCode:   buildFailDockerfileBuild,
			wantDetail: "npm error Incorrect or missing password.",
		},
		{
			name: "excerpt block names the failing step and its last output line",
			console: "[2026-08-08T00:11:40.000Z] #12 [stage-1 4/6] RUN pip install --no-cache-dir -r requirements.txt\n" +
				"[2026-08-08T00:11:41.000Z] #12 1.902 Collecting aiogram\n" +
				"[2026-08-08T00:11:45.000Z] #12 5.678 ERROR: Could not find a version that satisfies the requirement sqlite3 (from versions: none)\n" +
				"[2026-08-08T00:11:45.000Z] #12 5.679 ERROR: No matching distribution found for sqlite3\n" +
				"[2026-08-08T00:11:45.000Z] #12 ERROR: process \"/bin/sh -c pip install --no-cache-dir -r requirements.txt\" did not complete successfully: exit code: 1\n" +
				"[2026-08-08T00:11:46.000Z] ------\n" +
				"[2026-08-08T00:11:46.000Z]  > [stage-1 4/6] RUN pip install --no-cache-dir -r requirements.txt:\n" +
				"[2026-08-08T00:11:46.000Z] 5.678 ERROR: Could not find a version that satisfies the requirement sqlite3 (from versions: none)\n" +
				"[2026-08-08T00:11:46.000Z] 5.679 ERROR: No matching distribution found for sqlite3\n" +
				"[2026-08-08T00:11:46.000Z] ------\n" +
				"[2026-08-08T00:11:46.000Z] ERROR: failed to solve: process \"/bin/sh -c pip install --no-cache-dir -r requirements.txt\" did not complete successfully: exit code: 1\n" +
				"Finished: FAILURE\n",
			wantCode:   buildFailDockerfileBuild,
			wantDetail: "[stage-1 4/6] RUN pip install --no-cache-dir -r requirements.txt: ERROR: No matching distribution found for sqlite3",
		},
		{
			name: "step output without an excerpt still names the step",
			console: "[2026-08-05T10:00:00.000Z] #9 [builder 3/5] RUN go build ./...\n" +
				"[2026-08-05T10:00:04.000Z] #9 4.101 ./main.go:14:2: undefined: helper\n" +
				"[2026-08-05T10:00:04.000Z] #9 ERROR: process \"/bin/sh -c go build ./...\" did not complete successfully: exit code: 2\n" +
				"[2026-08-05T10:00:05.000Z] ERROR: failed to solve: process \"/bin/sh -c go build ./...\" did not complete successfully: exit code: 2\n" +
				"Finished: FAILURE\n",
			wantCode:   buildFailDockerfileBuild,
			wantDetail: "[builder 3/5] RUN go build ./...: ./main.go:14:2: undefined: helper",
		},
		{
			name: "credentials echoed by the failing step never reach error_message",
			console: "[2026-08-05T11:00:00.000Z] ------\n" +
				"[2026-08-05T11:00:00.000Z]  > [stage-1 3/6] RUN npm ci:\n" +
				"[2026-08-05T11:00:00.000Z] 2.100 npm error 401 Unauthorized https://deploy:s3cr3t@nexus.dada-tuda.ru/repository/npm/\n" +
				"[2026-08-05T11:00:00.000Z] ------\n" +
				"[2026-08-05T11:00:01.000Z] ERROR: failed to solve: process \"/bin/sh -c npm ci\" did not complete successfully: exit code: 1\n" +
				"Finished: FAILURE\n",
			wantCode:   buildFailDockerfileBuild,
			wantDetail: "[stage-1 3/6] RUN npm ci: npm error 401 Unauthorized https://***@nexus.dada-tuda.ru/repository/npm/",
		},
		{
			name: "an excerpt carrying only the wrapper falls back to the wrapper line",
			console: "[2026-08-05T12:00:00.000Z] ------\n" +
				"[2026-08-05T12:00:00.000Z]  > [stage-1 2/6] RUN make:\n" +
				"[2026-08-05T12:00:00.000Z] ------\n" +
				"[2026-08-05T12:00:01.000Z] ERROR: failed to solve: process \"/bin/sh -c make\" did not complete successfully: exit code: 1\n" +
				"Finished: FAILURE\n",
			wantCode:   buildFailDockerfileBuild,
			wantDetail: "ERROR: failed to solve: process \"/bin/sh -c make\" did not complete successfully: exit code: 1",
		},
		{
			name: "npm without a package.json is billed to the generated Dockerfile, not to the user (backlog 0455, 0462)",
			console: "[2026-08-19T00:11:40.000Z] #12 [5/6] RUN npm install\n" +
				"[2026-08-19T00:11:41.000Z] #12 1.100 npm error code ENOENT\n" +
				"[2026-08-19T00:11:41.000Z] #12 1.100 npm error syscall open\n" +
				"[2026-08-19T00:11:41.000Z] #12 1.100 npm error path /app/package.json\n" +
				"[2026-08-19T00:11:41.000Z] #12 1.100 npm error enoent Could not read package.json: Error: ENOENT: no such file or directory, open '/app/package.json'\n" +
				"[2026-08-19T00:11:41.000Z] #12 1.100 npm error A complete log of this run can be found in: /root/.npm/_logs/2026-08-19T00_11_41_100Z-debug-0.log\n" +
				"[2026-08-19T00:11:41.000Z] #12 ERROR: process \"/bin/sh -c npm install\" did not complete successfully: exit code: 1\n" +
				"[2026-08-19T00:11:42.000Z] ------\n" +
				"[2026-08-19T00:11:42.000Z]  > [5/6] RUN npm install:\n" +
				"[2026-08-19T00:11:42.000Z] 1.100 npm error code ENOENT\n" +
				"[2026-08-19T00:11:42.000Z] 1.100 npm error syscall open\n" +
				"[2026-08-19T00:11:42.000Z] 1.100 npm error path /app/package.json\n" +
				"[2026-08-19T00:11:42.000Z] 1.100 npm error enoent Could not read package.json: Error: ENOENT: no such file or directory, open '/app/package.json'\n" +
				"[2026-08-19T00:11:42.000Z] 1.100 npm error A complete log of this run can be found in: /root/.npm/_logs/2026-08-19T00_11_41_100Z-debug-0.log\n" +
				"[2026-08-19T00:11:42.000Z] ------\n" +
				"[2026-08-19T00:11:43.000Z] ERROR: failed to solve: process \"/bin/sh -c npm install\" did not complete successfully: exit code: 1\n" +
				"Finished: FAILURE\n",
			wantCode:   buildFailMissingManifest,
			wantDetail: missingManifestNpmDetail,
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

// TestClassifyFailureRealJenkinsLog runs the verbatim console of dada-build
// #264 -- the build bruzas.85@mail.ru lost on 2026-08-08 before deleting the
// app two minutes later -- through the classifier. The fixture is the reason
// this parser reads fence pairs instead of the four lines above the wrapper:
// the real log puts a commit WARNING and a Dockerfile source excerpt between
// the two, prints an empty decoy block for the cache manifest import, and ends
// the failing step's output with a pip upgrade notice.
func TestClassifyFailureRealJenkinsLog(t *testing.T) {
	console, err := os.ReadFile(filepath.Join("testdata", "jenkins-pip-sqlite3.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	code, detail := classifyFailure(string(console))
	if code != buildFailDockerfileBuild {
		t.Fatalf("code = %q, want %q", code, buildFailDockerfileBuild)
	}
	const want = "[4/7] RUN pip install --no-cache-dir -r requirements.txt: ERROR: No matching distribution found for sqlite3"
	if detail != want {
		t.Fatalf("detail = %q, want %q", detail, want)
	}
}

// TestClassifyFailureRealJenkinsPnpmLog is the second live shape: dada-build
// #257, where the failing step signs off with `For help, run: pnpm help
// install` and the cause sits one line above it. Together with the pip fixture
// it pins the rule that the last line of an excerpt is not the answer.
func TestClassifyFailureRealJenkinsPnpmLog(t *testing.T) {
	console, err := os.ReadFile(filepath.Join("testdata", "jenkins-pnpm-lockfile.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	code, detail := classifyFailure(string(console))
	if code != buildFailDockerfileBuild {
		t.Fatalf("code = %q, want %q", code, buildFailDockerfileBuild)
	}
	const want = "[build-stage 4/6] RUN npm install -g pnpm && pnpm i --frozen-lockfile: " +
		"[ERROR] Cannot verify the identity of the @pnpm/exe.linux-x64 native binary: it is missing from pnpm-lock.yaml."
	if detail != want {
		t.Fatalf("detail = %q, want %q", detail, want)
	}
}
