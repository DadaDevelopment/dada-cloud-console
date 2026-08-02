package box

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// fakeShell answers exec calls from a script keyed by (pod, command shape), and
// records every command so a test can assert on what the mechanism actually ran
// rather than only on what it returned.
type fakeShell struct {
	mu       sync.Mutex
	commands []string
	reply    func(pod, cmd string) (string, error)
	tarBytes []byte
}

func (f *fakeShell) execStream(ctx context.Context, pod, container, cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	f.mu.Lock()
	f.commands = append(f.commands, pod+"|"+cmd)
	f.mu.Unlock()
	if stdin != nil {
		body, _ := io.ReadAll(stdin)
		if strings.HasPrefix(cmd, "tar -x") {
			f.mu.Lock()
			f.tarBytes = append(f.tarBytes, body...)
			f.mu.Unlock()
		}
	}
	out, err := f.reply(pod, cmd)
	if err != nil {
		return err
	}
	if stdout != nil && out != "" {
		_, _ = io.WriteString(stdout, out)
	}
	return nil
}

func (f *fakeShell) ran(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

const (
	crystalTestBoxPod  = "box-live-1"
	crystalTestBasePod = "box-parked-1"
	crystalTestImage   = "ghcr.io/dadadevelopment/dada-box-warm:v1"
)

// crystalManifest is the manifest script's output shape: the M pass then the H
// pass. The baseline and the box share /etc/os-release and differ by the file the
// box wrote, which is exactly the delta the mechanism must find.
func crystalManifest(withApp bool) string {
	lines := []string{
		"M|f|644|120|/etc/os-release",
		"M|d|755|4096|/srv",
	}
	hashes := []string{
		"H|aaaa1111  /etc/os-release",
	}
	if withApp {
		lines = append(lines, "M|f|755|42|/srv/app.js")
		hashes = append(hashes, "H|bbbb2222  /srv/app.js")
	}
	return strings.Join(append(lines, hashes...), "\n") + "\n"
}

func crystalReply(t *testing.T) func(pod, cmd string) (string, error) {
	t.Helper()
	return func(pod, cmd string) (string, error) {
		switch {
		case strings.Contains(cmd, "/etc/dada/services/"):
			return `{"name":"web","command":"node app.js","working_dir":"/workspace","ports":[8080]}` + "\n", nil
		case strings.Contains(cmd, "/proc/net/tcp"):
			return "8080\n", nil
		case strings.HasPrefix(cmd, "cat /etc/dada/box.env"):
			return "DATABASE_URL=postgres://u:p@h/db\nPORT=8080\n", nil
		case strings.HasPrefix(cmd, "stat -c %a"):
			return "600\n", nil
		case strings.HasPrefix(cmd, "ROOTS="):
			return crystalManifest(pod != crystalTestBasePod), nil
		case strings.Contains(cmd, "http_code"):
			return "200\n", nil
		default:
			return "", nil
		}
	}
}

// crystalClientset is a cluster holding a live box pod and one parked pod of the
// same image, with a reactor that gives every created Deployment a Running pod —
// the fake clientset runs no controllers, so without it nothing would ever start.
func crystalClientset() *fake.Clientset {
	live := parkedPod(crystalTestBoxPod, crystalTestImage, "", true)
	live.Labels[labelBoxPhase] = phaseLive
	live.Status.Phase = corev1.PodRunning
	live.Spec.Containers = []corev1.Container{{Name: clusterContainerName, Image: crystalTestImage}}

	base := parkedPod(crystalTestBasePod, crystalTestImage, "", true)
	base.Status.Phase = corev1.PodRunning
	base.Spec.Containers = []corev1.Container{{Name: clusterContainerName, Image: crystalTestImage}}

	cs := fake.NewSimpleClientset(live, base)
	cs.PrependReactor("create", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		dep := action.(k8stesting.CreateAction).GetObject().(*appsv1.Deployment)
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dep.Name + "-abc",
				Namespace: dep.Namespace,
				Labels:    dep.Spec.Template.Labels,
			},
			Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: crystalContainer, Image: dep.Spec.Template.Spec.Containers[0].Image}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}
		go func() {
			_, _ = cs.CoreV1().Pods(dep.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
		}()
		return false, nil, nil
	})
	return cs
}

func newTestCrystallizer(t *testing.T, cs *fake.Clientset, shell *fakeShell) *ClusterCrystallizer {
	t.Helper()
	return &ClusterCrystallizer{
		shell:        shell,
		clientset:    cs,
		clock:        SystemClock{},
		Namespace:    "dada-boxes",
		HostnameBase: "box.dada-tuda.ru",
		TLSSecret:    "box-wildcard-tls",
		IngressClass: "nginx",
		StorageClass: "longhorn",
		DiskGB:       10,
		ReadyTimeout: 2 * time.Second,
		SeedTimeout:  2 * time.Second,
	}
}

// TestClusterCrystallizeCarriesOnlyTheDeltaAndVerifiesIt is the whole mechanism:
// the delta is computed against a pristine pod of the same image, only that delta
// is transferred, and the promotion is called verified only after the file
// manifest, the socket set, the env digests and an in-namespace probe all agree.
func TestClusterCrystallizeCarriesOnlyTheDeltaAndVerifiesIt(t *testing.T) {
	shell := &fakeShell{reply: crystalReply(t)}
	cs := crystalClientset()
	cz := newTestCrystallizer(t, cs, shell)

	rep, err := cz.CrystallizeWithReport(context.Background(),
		&Instance{ID: "b-1", InstanceRef: crystalTestBoxPod},
		CrystallizeOptions{VMName: "shop", Domain: "localhost", ProbePath: "/"})
	if err != nil {
		t.Fatalf("crystallize: %v\n%s", err, rep.Text())
	}
	if !rep.Manifest.Equal || !rep.Sockets.Equal || !rep.Env.Equal || !rep.ProbeInternal.OK {
		t.Fatalf("expected every check to pass, got %+v", rep)
	}
	if rep.Manifest.BoxFiles != 1 {
		t.Fatalf("only the file the box added should be carried, got %d files: %+v", rep.Manifest.BoxFiles, rep.Manifest)
	}
	if !strings.Contains(rep.BaselineSource, crystalTestBasePod) {
		t.Fatalf("the report must name the baseline it compared against, got %q", rep.BaselineSource)
	}
	if !shell.ran("tar -c --numeric-owner") || !shell.ran("tar -x --numeric-owner") {
		t.Fatalf("the delta was never streamed: %v", shell.commands)
	}
	if !shell.ran("touch " + crystalSeeded) {
		t.Fatal("the seed marker was never written, so the artifact would start against a half-copied disk")
	}

	dep, err := cs.AppsV1().Deployments("dada-boxes").Get(context.Background(), "crystal-shop", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the permanent workload was not created: %v", err)
	}
	if dep.Spec.Template.Spec.HostUsers == nil || *dep.Spec.Template.Spec.HostUsers {
		t.Fatal("hostUsers must stay false: it is the load-bearing isolation field of ADR-019")
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims("dada-boxes").Get(context.Background(), "crystal-shop", metav1.GetOptions{}); err != nil {
		t.Fatalf("the permanent disk was not created: %v", err)
	}
	if _, err := cs.NetworkingV1().Ingresses("dada-boxes").Get(context.Background(), "crystal-shop", metav1.GetOptions{}); err != nil {
		t.Fatalf("the address was not published: %v", err)
	}
}

// TestClusterCrystallizeRefusesABoxThatRunsNothing keeps the promotion honest: a
// box with no service descriptor and no command in the request has nothing to
// keep running, and promoting it would bill a customer monthly for an idle pod.
func TestClusterCrystallizeRefusesABoxThatRunsNothing(t *testing.T) {
	shell := &fakeShell{reply: func(pod, cmd string) (string, error) { return "", nil }}
	cz := newTestCrystallizer(t, crystalClientset(), shell)

	rep, err := cz.CrystallizeWithReport(context.Background(),
		&Instance{ID: "b-1", InstanceRef: crystalTestBoxPod},
		CrystallizeOptions{VMName: "empty", Domain: "localhost"})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if rep == nil || rep.Stage != "capture" {
		t.Fatalf("a failure must still carry a report naming its stage, got %+v", rep)
	}
	if !strings.Contains(err.Error(), "declares no service") {
		t.Fatalf("the refusal must say what is missing, got %q", err.Error())
	}
}

// TestClusterCrystallizeFailsWhenTheArtifactNeverListens is the check ADR-019 §7
// exists for: the transfer can succeed while the promoted process never comes up,
// and an unverified promotion must be reported as failed rather than as done.
func TestClusterCrystallizeFailsWhenTheArtifactNeverListens(t *testing.T) {
	base := crystalReply(t)
	shell := &fakeShell{reply: func(pod, cmd string) (string, error) {
		if strings.Contains(cmd, "/proc/net/tcp") && pod != crystalTestBoxPod {
			return "", nil
		}
		return base(pod, cmd)
	}}
	cz := newTestCrystallizer(t, crystalClientset(), shell)
	cz.ReadyTimeout = 200 * time.Millisecond

	rep, err := cz.CrystallizeWithReport(context.Background(),
		&Instance{ID: "b-1", InstanceRef: crystalTestBoxPod},
		CrystallizeOptions{VMName: "silent", Domain: "localhost"})
	if err == nil {
		t.Fatalf("a promotion whose ports never came up must fail: %s", rep.Text())
	}
	if rep.Sockets.Equal {
		t.Fatal("the socket comparison must not claim equality when nothing listens")
	}
	if len(rep.Sockets.DeclaredPorts) == 0 {
		t.Fatal("the report must still say which ports were expected")
	}
}

// TestCrystalCommandWaitsForTheSeedMarker pins the one ordering that could
// produce a corrupt artifact: the workload is created before the userland is
// transferred, so its command must block until the transfer says it is done.
func TestCrystalCommandWaitsForTheSeedMarker(t *testing.T) {
	cmd := crystalCommand(ServiceDescriptor{Name: "web", Command: "node app.js"}, "/workspace")
	if !strings.HasPrefix(strings.TrimSpace(cmd), "while [ ! -f "+crystalSeeded) {
		t.Fatalf("the command must wait for the seed marker first, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "exec /bin/sh -c 'node app.js'") {
		t.Fatalf("the declared command must be what the artifact runs, got:\n%s", cmd)
	}
}

// TestSplitDeltaRoutesTheWorkingTreeToThePersistentMount keeps runtime writes
// alive: files under the working directory land on the mounted subtree, and
// everything else is re-applied over the image root at each start.
func TestSplitDeltaRoutesTheWorkingTreeToThePersistentMount(t *testing.T) {
	delta := map[string]FileEntry{
		"/workspace/db.sqlite":  {Path: "/workspace/db.sqlite"},
		"/workspace":            {Path: "/workspace"},
		"/etc/nginx/nginx.conf": {Path: "/etc/nginx/nginx.conf"},
	}
	root, work := splitDelta(delta, "/workspace")
	if len(work) != 2 || len(root) != 1 || root[0] != "/etc/nginx/nginx.conf" {
		t.Fatalf("root=%v work=%v", root, work)
	}
	if pathDepth("/workspace") != 1 {
		t.Fatalf("strip depth for /workspace must be 1, got %d", pathDepth("/workspace"))
	}
}

// TestParseTarSkipsReadsWarnings pins the parse of what a box refused to hand
// over: real tar output, including a line about leading slashes that names no
// path and a duplicate that must not be reported twice.
func TestParseTarSkipsReadsWarnings(t *testing.T) {
	log := "tar: Removing leading `/' from member names\n" +
		"tar: Removing leading `/' from hard link targets\n" +
		"tar: /etc/redis: Warning: Cannot open: Permission denied\n" +
		"tar: /home/ubuntu: Warning: Cannot open: Permission denied\n" +
		"tar: /etc/redis: Warning: Cannot open: Permission denied\n" +
		"tar: /var/lib/redis/dump.rdb: Cannot read: Permission denied\n"
	got := parseTarSkips(log)
	want := []string{"/etc/redis", "/home/ubuntu", "/var/lib/redis/dump.rdb"}
	if len(got) != len(want) {
		t.Fatalf("parseTarSkips = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseTarSkips = %v, want %v", got, want)
		}
	}
}

func TestParseTarSkipsIgnoresChatter(t *testing.T) {
	if got := parseTarSkips("tar: Removing leading `/' from hard link targets\n"); len(got) != 0 {
		t.Fatalf("parseTarSkips = %v, want none", got)
	}
}

func TestStripComponents(t *testing.T) {
	cases := []struct {
		path  string
		strip int
		want  string
	}{
		{"/etc/redis", 0, "etc/redis"},
		{"/workspace/app/data", 1, "app/data"},
		{"/workspace", 1, ""},
		{"/workspace/app", 3, ""},
	}
	for _, c := range cases {
		if got := stripComponents(c.path, c.strip); got != c.want {
			t.Fatalf("stripComponents(%q, %d) = %q, want %q", c.path, c.strip, got, c.want)
		}
	}
}
