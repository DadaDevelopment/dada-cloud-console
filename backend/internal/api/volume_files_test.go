package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakePodFS is an in-memory stand-in for a container filesystem. dirLinks maps
// a path to what the container would physically resolve it to, which is how the
// symlink-escape cases are expressed without a cluster.
type fakePodFS struct {
	entries  map[string]cloudtask.FileEntry
	contents map[string]string
	dirLinks map[string]string
	links    map[string]string

	wrote   map[string]string
	removed []string
	moved   [][2]string
	made    []string
}

func newFakePodFS() *fakePodFS {
	return &fakePodFS{
		entries: map[string]cloudtask.FileEntry{
			"/data":           {Name: "data", Kind: cloudtask.FileKindDir, Mode: "drwxr-xr-x", ModTime: 100},
			"/data/notes.txt": {Name: "notes.txt", Kind: cloudtask.FileKindFile, Size: 5, Mode: "-rw-r--r--", ModTime: 200},
			"/data/logo.png":  {Name: "logo.png", Kind: cloudtask.FileKindFile, Size: 4, Mode: "-rw-r--r--", ModTime: 200},
			"/data/uploads":   {Name: "uploads", Kind: cloudtask.FileKindDir, Mode: "drwxr-xr-x", ModTime: 150},
			"/data/escape":    {Name: "escape", Kind: cloudtask.FileKindSymlink, Size: 11, Mode: "lrwxrwxrwx", ModTime: 150},
		},
		contents: map[string]string{
			"/data/notes.txt": "hello",
			"/data/logo.png":  "\x89PNG",
		},
		dirLinks: map[string]string{"/data/outside": "/etc"},
		links:    map[string]string{"/data/escape": "/etc/passwd"},
		wrote:    map[string]string{},
	}
}

func (f *fakePodFS) Enabled() bool { return true }

// disabledPodFS stands for an environment without pod exec. It is spelled out
// instead of calling cloudtask.NewPodFS(), which reports itself enabled
// whenever the process happens to run inside a cluster - as CI does.
type disabledPodFS struct{ *fakePodFS }

func (disabledPodFS) Enabled() bool { return false }

func (f *fakePodFS) FindRunningPod(context.Context, string, string) (string, string, error) {
	return "pod-1", "app", nil
}

// ResolvePath mirrors what "cd && pwd -P" does in a container: a symlinked
// directory anywhere in the path rewrites everything below it, not just an
// exact match on the link itself.
func (f *fakePodFS) ResolvePath(_ context.Context, _ cloudtask.PodTarget, p string) (string, error) {
	for link, target := range f.dirLinks {
		if p == link {
			return target, nil
		}
		if strings.HasPrefix(p, link+"/") {
			return target + strings.TrimPrefix(p, link), nil
		}
	}
	return p, nil
}

func (f *fakePodFS) ResolveLink(_ context.Context, _ cloudtask.PodTarget, p string) (string, error) {
	if target, ok := f.links[p]; ok {
		return target, nil
	}
	return "", errors.New("not a symlink")
}

func (f *fakePodFS) List(_ context.Context, _ cloudtask.PodTarget, dir string) ([]cloudtask.FileEntry, bool, error) {
	var out []cloudtask.FileEntry
	for p, e := range f.entries {
		if p == dir {
			continue
		}
		if strings.TrimSuffix(dir, "/")+"/"+e.Name == p {
			out = append(out, e)
		}
	}
	return out, false, nil
}

func (f *fakePodFS) Stat(_ context.Context, _ cloudtask.PodTarget, p string) (cloudtask.FileEntry, error) {
	e, ok := f.entries[p]
	if !ok {
		return cloudtask.FileEntry{}, errors.New("no such file or directory")
	}
	return e, nil
}

func (f *fakePodFS) ReadFile(_ context.Context, _ cloudtask.PodTarget, p string, _ int64, out io.Writer) error {
	body, ok := f.contents[p]
	if !ok {
		return errors.New("no such file or directory")
	}
	_, err := io.WriteString(out, body)
	return err
}

func (f *fakePodFS) WriteFile(_ context.Context, _ cloudtask.PodTarget, p string, in io.Reader) error {
	body, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	f.wrote[p] = string(body)
	return nil
}

func (f *fakePodFS) Mkdir(_ context.Context, _ cloudtask.PodTarget, p string) error {
	f.made = append(f.made, p)
	return nil
}

func (f *fakePodFS) Move(_ context.Context, _ cloudtask.PodTarget, from, to string) error {
	f.moved = append(f.moved, [2]string{from, to})
	return nil
}

func (f *fakePodFS) Remove(_ context.Context, _ cloudtask.PodTarget, p string, _ bool) error {
	f.removed = append(f.removed, p)
	return nil
}

func (f *fakePodFS) TarDir(_ context.Context, _ cloudtask.PodTarget, _ string, out io.Writer) error {
	_, err := io.WriteString(out, "tarball")
	return err
}

func testAppFSSession() *appFSSession {
	return &appFSSession{
		projectID: uuid.New(),
		envID:     uuid.New(),
		appName:   "app",
		target:    cloudtask.PodTarget{Namespace: "ns", Pod: "pod-1", Container: "app"},
		root:      "/data",
	}
}

func TestAppFSResolveConfinesToVolume(t *testing.T) {
	fs := newFakePodFS()
	s := testAppFSSession()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"root", "/", "/data"},
		{"empty", "", "/data"},
		{"child", "/uploads", "/data/uploads"},
		{"child without slash", "uploads", "/data/uploads"},
		{"parent walk is clamped", "../../etc/passwd", "/data/etc/passwd"},
		{"absolute escape is clamped", "/../../etc/passwd", "/data/etc/passwd"},
		{"dot segments", "/uploads/./../notes.txt", "/data/notes.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.resolve(t.Context(), fs, tc.in)
			if err != nil {
				t.Fatalf("resolve(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("resolve(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAppFSResolveRejectsSymlinkedDirectoryEscape(t *testing.T) {
	fs := newFakePodFS()
	s := testAppFSSession()

	if _, err := s.resolve(t.Context(), fs, "/outside"); !errors.Is(err, errFSOutsideVolume) {
		t.Fatalf("resolve(/outside) error = %v, want errFSOutsideVolume", err)
	}
}

func TestAppFSResolveRejectsNulByte(t *testing.T) {
	fs := newFakePodFS()
	s := testAppFSSession()

	if _, err := s.resolve(t.Context(), fs, "/notes\x00.txt"); !errors.Is(err, errFSOutsideVolume) {
		t.Fatalf("error = %v, want errFSOutsideVolume", err)
	}
}

func TestAppFSResolveLeafRejectsSymlinkPointingOutside(t *testing.T) {
	fs := newFakePodFS()
	s := testAppFSSession()

	if _, _, err := s.resolveLeaf(t.Context(), fs, "/escape"); !errors.Is(err, errFSOutsideVolume) {
		t.Fatalf("resolveLeaf(/escape) error = %v, want errFSOutsideVolume", err)
	}
}

func TestAppFSRelativeRendersVolumePaths(t *testing.T) {
	s := testAppFSSession()
	if got := s.relative("/data"); got != "/" {
		t.Errorf("relative(root) = %q, want /", got)
	}
	if got := s.relative("/data/uploads/a.txt"); got != "/uploads/a.txt" {
		t.Errorf("relative(child) = %q, want /uploads/a.txt", got)
	}
}

func TestWithinRoot(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{"/data", "/data", true},
		{"/data", "/data/x", true},
		{"/data", "/data-backup/x", false},
		{"/data", "/etc/passwd", false},
		{"/", "/etc/passwd", true},
	}
	for _, tc := range cases {
		if got := withinRoot(tc.root, tc.path); got != tc.want {
			t.Errorf("withinRoot(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
		}
	}
}

func TestIsBinaryContent(t *testing.T) {
	if isBinaryContent("plain text\nwith unicode: привет") {
		t.Error("text classified as binary")
	}
	if !isBinaryContent("\x89PNG\x00\x1a") {
		t.Error("NUL-containing content not classified as binary")
	}
	if !isBinaryContent("\xff\xfe\xfd") {
		t.Error("invalid UTF-8 not classified as binary")
	}
}

func TestContentDispositionAttachment(t *testing.T) {
	got := contentDispositionAttachment("отчёт \"2026\".csv")
	if !strings.HasPrefix(got, `attachment; filename="`) {
		t.Fatalf("header = %q", got)
	}
	if strings.Contains(got, `"2026"`) {
		t.Errorf("quotes leaked into the ASCII fallback: %q", got)
	}
	if !strings.Contains(got, "filename*=UTF-8''") {
		t.Errorf("missing RFC 5987 form: %q", got)
	}
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Errorf("header contains a newline: %q", got)
	}
}

func TestClassifyFSError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"outside volume", errFSOutsideVolume, http.StatusBadRequest},
		{"missing path", &cloudtask.PodExecError{Stderr: "cat: /data/x: No such file or directory", Err: errors.New("exit 1")}, http.StatusNotFound},
		{"permission", &cloudtask.PodExecError{Stderr: "cat: /data/x: Permission denied", Err: errors.New("exit 1")}, http.StatusConflict},
		{"no space", &cloudtask.PodExecError{Stderr: "cat: write error: No space left on device", Err: errors.New("exit 1")}, http.StatusInsufficientStorage},
		{"read-only", &cloudtask.PodExecError{Stderr: "mv: Read-only file system", Err: errors.New("exit 1")}, http.StatusConflict},
		{"no shell", &cloudtask.PodExecError{Stderr: "", Err: errors.New(`exec: "sh": executable file not found in $PATH`)}, http.StatusConflict},
		{"unknown", &cloudtask.PodExecError{Stderr: "something odd", Err: errors.New("exit 3")}, http.StatusBadGateway},
		{"transport", errors.New("dial tcp: connection refused"), http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, msg := classifyFSError(tc.err, "failed")
			if status != tc.want {
				t.Fatalf("status = %d, want %d (msg %q)", status, tc.want, msg)
			}
			if msg == "" {
				t.Fatal("empty message")
			}
		})
	}
}

func newAppFilesCtx(t *testing.T, method, suffix string, projectID, envID uuid.UUID, appName string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	url := "/api/v1/projects/" + projectID.String() + "/environments/" + envID.String() +
		"/apps/" + appName + "/volume/files" + suffix
	c.Request = httptest.NewRequest(method, url, body)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
	}
	auth.SetClaims(c, &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}})
	return c, rec
}

func newAppFilesHandler(pool *pgxpool.Pool, fs cloudtask.PodFS) *Handler {
	return &Handler{pool: pool, podFS: fs}
}

func TestListAppFiles_NoVolume_Conflict(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{}`)

	h := newAppFilesHandler(pool, newFakePodFS())
	c, rec := newAppFilesCtx(t, http.MethodGet, "", projectID, envID, appName, nil)
	h.ListAppFiles(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s want 409", rec.Code, rec.Body.String())
	}
}

func TestListAppFiles_ReturnsVolumeRelativePaths(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	h := newAppFilesHandler(pool, newFakePodFS())
	c, rec := newAppFilesCtx(t, http.MethodGet, "", projectID, envID, appName, nil)
	h.ListAppFiles(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"path":"/"`) {
		t.Errorf("listing path is not volume-relative: %s", body)
	}
	if !strings.Contains(body, `"notes.txt"`) || !strings.Contains(body, `"uploads"`) {
		t.Errorf("entries missing: %s", body)
	}
}

func TestListAppFiles_TraversalIsClampedToVolume(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	fs := newFakePodFS()
	h := newAppFilesHandler(pool, fs)
	c, rec := newAppFilesCtx(t, http.MethodGet, "?path=/outside", projectID, envID, appName, nil)
	h.ListAppFiles(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s want 400", rec.Code, rec.Body.String())
	}
}

func TestReadAppFile_BinaryRejected(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	h := newAppFilesHandler(pool, newFakePodFS())
	c, rec := newAppFilesCtx(t, http.MethodGet, "/content?path=/logo.png", projectID, envID, appName, nil)
	h.ReadAppFile(c)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("code=%d body=%s want 415", rec.Code, rec.Body.String())
	}
}

func TestReadAppFile_Text(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	h := newAppFilesHandler(pool, newFakePodFS())
	c, rec := newAppFilesCtx(t, http.MethodGet, "/content?path=/notes.txt", projectID, envID, appName, nil)
	h.ReadAppFile(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"content":"hello"`) {
		t.Errorf("body=%s", rec.Body.String())
	}
}

func TestWriteAppFile_StaleModifiedIsRejected(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	fs := newFakePodFS()
	h := newAppFilesHandler(pool, fs)
	body := strings.NewReader(`{"path":"/notes.txt","content":"new","modified":100}`)
	c, rec := newAppFilesCtx(t, http.MethodPut, "/content", projectID, envID, appName, body)
	h.WriteAppFile(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s want 409", rec.Code, rec.Body.String())
	}
	if _, ok := fs.wrote["/data/notes.txt"]; ok {
		t.Error("stale write was applied")
	}
}

func TestWriteAppFile_Writes(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	fs := newFakePodFS()
	h := newAppFilesHandler(pool, fs)
	body := strings.NewReader(`{"path":"/notes.txt","content":"new","modified":200}`)
	c, rec := newAppFilesCtx(t, http.MethodPut, "/content", projectID, envID, appName, body)
	h.WriteAppFile(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if fs.wrote["/data/notes.txt"] != "new" {
		t.Errorf("wrote=%q want %q", fs.wrote["/data/notes.txt"], "new")
	}
}

func TestDeleteAppFile_VolumeRootRefused(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	fs := newFakePodFS()
	h := newAppFilesHandler(pool, fs)
	body := strings.NewReader(`{"path":"/","recursive":true}`)
	c, rec := newAppFilesCtx(t, http.MethodPost, "/delete", projectID, envID, appName, body)
	h.DeleteAppFile(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s want 409", rec.Code, rec.Body.String())
	}
	if len(fs.removed) != 0 {
		t.Errorf("removed=%v want none", fs.removed)
	}
}

func TestDeleteAppFile_RemovesChild(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	fs := newFakePodFS()
	h := newAppFilesHandler(pool, fs)
	body := strings.NewReader(`{"path":"/uploads","recursive":true}`)
	c, rec := newAppFilesCtx(t, http.MethodPost, "/delete", projectID, envID, appName, body)
	h.DeleteAppFile(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(fs.removed) != 1 || fs.removed[0] != "/data/uploads" {
		t.Errorf("removed=%v want [/data/uploads]", fs.removed)
	}
}

func TestMoveAppFile_BothPathsConfined(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	fs := newFakePodFS()
	h := newAppFilesHandler(pool, fs)
	body := strings.NewReader(`{"from":"/notes.txt","to":"/outside/notes.txt"}`)
	c, rec := newAppFilesCtx(t, http.MethodPost, "/move", projectID, envID, appName, body)
	h.MoveAppFile(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(fs.moved) != 0 {
		t.Errorf("moved=%v want none", fs.moved)
	}
}

func TestListAppFiles_NotConfigured_ServiceUnavailable(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	h := newAppFilesHandler(pool, disabledPodFS{newFakePodFS()})
	c, rec := newAppFilesCtx(t, http.MethodGet, "", projectID, envID, appName, nil)
	h.ListAppFiles(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s want 503", rec.Code, rec.Body.String())
	}
}
