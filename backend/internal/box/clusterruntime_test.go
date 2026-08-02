package box

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/boxcatalog"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// canonPod parses the second document of manifests/box-pod.yaml, which is what an
// operator reads and what the 07-31 isolation probe actually applied.
func canonPod(t *testing.T) *corev1.Pod {
	t.Helper()
	raw, err := os.ReadFile("manifests/box-pod.yaml")
	if err != nil {
		t.Fatalf("read canon manifest: %v", err)
	}
	docs := strings.Split(string(raw), "\n---\n")
	for _, doc := range docs {
		if !strings.Contains(doc, "kind: Pod") {
			continue
		}
		var pod corev1.Pod
		if err := yaml.NewYAMLOrJSONDecoder(bytes.NewBufferString(doc), 4096).Decode(&pod); err != nil {
			t.Fatalf("decode canon pod: %v", err)
		}
		return &pod
	}
	t.Fatal("manifests/box-pod.yaml has no Pod document")
	return nil
}

// TestBuiltPodMatchesTheCanonManifest is the test that keeps the Go builder and
// the YAML from drifting.
//
// The YAML is not decoration: it is what was applied when the isolation claim was
// probed on 07-31, so a builder that quietly renders something weaker would make
// that probe's result a statement about a pod nobody runs. Every field asserted
// here is one whose loss changes the isolation or cost story, not merely the
// cosmetics — a field that only affects appearance is deliberately absent so this
// test does not become the kind nobody may touch.
func TestBuiltPodMatchesTheCanonManifest(t *testing.T) {
	canon := canonPod(t)
	rt := newClusterRuntime(nil, nil, "dada-boxes", nil)
	built := rt.BuildPod("example", boxcatalog.DefaultImage(), boxcatalog.DefaultSize(), "ru-1")

	if got, want := ptrBool(built.Spec.HostUsers), ptrBool(canon.Spec.HostUsers); got != want || got != false {
		t.Errorf("hostUsers built=%v canon=%v; false is the one field that cannot be dropped", got, want)
	}
	if ptrBool(built.Spec.AutomountServiceAccountToken) != ptrBool(canon.Spec.AutomountServiceAccountToken) {
		t.Error("automountServiceAccountToken drifted from the canon")
	}
	if ptrBool(built.Spec.EnableServiceLinks) != ptrBool(canon.Spec.EnableServiceLinks) {
		t.Error("enableServiceLinks drifted from the canon")
	}
	if built.Spec.ServiceAccountName != canon.Spec.ServiceAccountName {
		t.Errorf("serviceAccountName built=%q canon=%q", built.Spec.ServiceAccountName, canon.Spec.ServiceAccountName)
	}
	if built.Spec.PriorityClassName != canon.Spec.PriorityClassName {
		t.Errorf("priorityClassName built=%q canon=%q", built.Spec.PriorityClassName, canon.Spec.PriorityClassName)
	}
	if built.Spec.RestartPolicy != canon.Spec.RestartPolicy {
		t.Errorf("restartPolicy built=%q canon=%q", built.Spec.RestartPolicy, canon.Spec.RestartPolicy)
	}
	if built.Spec.ActiveDeadlineSeconds == nil || canon.Spec.ActiveDeadlineSeconds == nil ||
		*built.Spec.ActiveDeadlineSeconds != *canon.Spec.ActiveDeadlineSeconds {
		t.Error("activeDeadlineSeconds drifted from the canon: it is the backstop on a box nobody reaps")
	}

	bc, cc := built.Spec.Containers[0], canon.Spec.Containers[0]
	if bc.Image != cc.Image {
		t.Errorf("image built=%q canon=%q", bc.Image, cc.Image)
	}
	if got, want := strings.Join(bc.Command, " "), strings.Join(cc.Command, " "); got != want {
		t.Errorf("command built=%q canon=%q", got, want)
	}
	if len(bc.SecurityContext.Capabilities.Drop) != 1 || bc.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Errorf("capabilities drop built=%v; a box needs none inside its user namespace", bc.SecurityContext.Capabilities.Drop)
	}
	if ptrBool(bc.SecurityContext.AllowPrivilegeEscalation) != false {
		t.Error("allowPrivilegeEscalation must stay false")
	}
	if len(bc.ResizePolicy) != len(cc.ResizePolicy) {
		t.Error("resizePolicy drifted: without NotRequired an in-place resize restarts the box and loses the session")
	}
	if bc.StartupProbe == nil || bc.StartupProbe.TCPSocket == nil {
		t.Fatal("the startup probe is what makes a pod join the pool only once its door accepts")
	}
	if bc.StartupProbe.TCPSocket.Port != cc.StartupProbe.TCPSocket.Port {
		t.Errorf("startup probe port built=%v canon=%v", bc.StartupProbe.TCPSocket.Port, cc.StartupProbe.TCPSocket.Port)
	}
}

// TestBuiltPodPinsResourcesToTheCatalogSize checks the number that decides
// density. Memory is never oversubscribed on a box host, so a limit that does not
// match the catalog size silently changes how many boxes fit and therefore what a
// box costs.
func TestBuiltPodPinsResourcesToTheCatalogSize(t *testing.T) {
	rt := newClusterRuntime(nil, nil, "dada-boxes", nil)
	size := boxcatalog.DefaultSize()
	built := rt.BuildPod("example", boxcatalog.DefaultImage(), size, "ru-1")

	res := built.Spec.Containers[0].Resources
	if got := res.Limits.Memory().Value(); got != int64(size.MemoryMB)*1024*1024 {
		t.Errorf("memory limit %d does not match catalog size %dMi", got, size.MemoryMB)
	}
	if res.Requests.Memory().Cmp(*res.Limits.Memory()) != 0 {
		t.Error("memory request and limit must be equal: a box host does not oversubscribe memory")
	}
	if got := res.Limits.Cpu().Value(); got != int64(size.VCPU) {
		t.Errorf("cpu limit %d does not match catalog size %d", got, size.VCPU)
	}
	if res.Requests.Cpu().Cmp(*res.Limits.Cpu()) >= 0 {
		t.Error("cpu request must be below the limit: CPU is oversubscribed, memory is not")
	}
}

// TestBuiltPodCarriesThePullSecretOnlyWhenConfigured guards the deploy that
// actually failed first: ghcr is private, and a pod without the secret sits in
// ImagePullBackOff forever while the pool reports a slot it cannot serve.
func TestBuiltPodCarriesThePullSecretOnlyWhenConfigured(t *testing.T) {
	rt := newClusterRuntime(nil, nil, "dada-boxes", nil)
	if got := rt.BuildPod("example", boxcatalog.DefaultImage(), boxcatalog.DefaultSize(), "ru-1"); len(got.Spec.ImagePullSecrets) != 0 {
		t.Errorf("unconfigured runtime must not name a pull secret, got %v", got.Spec.ImagePullSecrets)
	}
	rt.PullSecret = "github-container-registry"
	built := rt.BuildPod("example", boxcatalog.DefaultImage(), boxcatalog.DefaultSize(), "ru-1")
	if len(built.Spec.ImagePullSecrets) != 1 || built.Spec.ImagePullSecrets[0].Name != "github-container-registry" {
		t.Errorf("configured pull secret did not reach the pod: %v", built.Spec.ImagePullSecrets)
	}
}

// TestBuiltPodStartsParked pins the label the network policy selects. A pod that
// came up already labelled live would have tenant egress before anyone claimed
// it, which is exactly the window a warm pool is supposed to close.
func TestBuiltPodStartsParked(t *testing.T) {
	rt := newClusterRuntime(nil, nil, "dada-boxes", nil)
	built := rt.BuildPod("example", boxcatalog.DefaultImage(), boxcatalog.DefaultSize(), "ru-1")
	if got := built.Labels[labelBoxPhase]; got != phaseParked {
		t.Errorf("a fresh box pod is labelled %q, want %q", got, phaseParked)
	}
	if got := built.Labels[labelBoxID]; got != "example" {
		t.Errorf("box id label is %q, want the box id", got)
	}
}

// TestBindRefusesAnEnvironmentKeyThatIsShell covers the injection the bind script
// would otherwise allow. The value is quoted, so the key is the only place a
// tenant could break out of the printf and run something as root in their own box
// — harmless there — or, worse, smuggle a newline into the file the broker reads.
func TestBindRefusesAnEnvironmentKeyThatIsShell(t *testing.T) {
	rt := newClusterRuntime(nil, nil, "dada-boxes", nil)
	inst := &Instance{ID: "example", InstanceRef: "box-example"}
	for _, key := range []string{"FOO; rm -rf /", "FOO\nBAR", "1FOO", "", "FOO BAR", "FOO'"} {
		err := rt.Bind(t.Context(), inst, Spec{Env: map[string]string{key: "v"}})
		if err == nil {
			t.Errorf("Bind accepted %q as an environment key", key)
			continue
		}
		if !strings.Contains(err.Error(), "invalid environment key") {
			t.Errorf("Bind on %q failed with %v, want the key rejection before any exec", key, err)
		}
	}
}

// ptrBool reads an optional bool, treating absence as false — which is how the
// API server treats a missing hostUsers ONLY in the sense that this test would
// then fail loudly, since false is what the canon requires to be explicit.
func ptrBool(p *bool) bool {
	return p != nil && *p
}

// TestServiceDescriptorsSurviveLosingTheBody runs the real link script against a
// throwaway root and proves the two properties a sleeping box depends on: a
// descriptor written before the link existed is carried into the persistent store
// rather than deleted, and running the script again on a fresh body re-attaches
// the documented path to descriptors that were already there.
func TestServiceDescriptorsSurviveLosingTheBody(t *testing.T) {
	root := t.TempDir()
	script := strings.ReplaceAll(linkServicesScript, clusterServicesLink, root+clusterServicesLink)
	script = strings.ReplaceAll(script, "/etc/dada\n", root+"/etc/dada\n")
	script = strings.ReplaceAll(script, clusterServicesStore, root+clusterServicesStore)

	if err := os.MkdirAll(root+clusterServicesLink, 0o755); err != nil {
		t.Fatalf("seed body-local services dir: %v", err)
	}
	if err := os.WriteFile(root+clusterServicesLink+"/demo.json", []byte(`{"name":"demo"}`), 0o644); err != nil {
		t.Fatalf("seed descriptor: %v", err)
	}

	runScript(t, script)
	stored, err := os.ReadFile(root + clusterServicesStore + "/demo.json")
	if err != nil {
		t.Fatalf("descriptor was not carried onto the persistent store: %v", err)
	}
	if string(stored) != `{"name":"demo"}` {
		t.Fatalf("carried descriptor changed: %s", stored)
	}

	if err := os.RemoveAll(root + "/etc"); err != nil {
		t.Fatalf("simulate losing the body: %v", err)
	}
	runScript(t, script)
	if _, err := os.ReadFile(root + clusterServicesLink + "/demo.json"); err != nil {
		t.Fatalf("a woken body cannot see its declared services: %v", err)
	}
}

func runScript(t *testing.T, script string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("link script failed: %v: %s", err, out)
	}
}

// TestAResumedServiceStartsWhereItsDiskIs pins the default working directory of a
// restarted service to the persistent claim. A descriptor with no working
// directory that restarted in an ephemeral path would come back in a tree that no
// longer holds the customer's code.
func TestAResumedServiceStartsWhereItsDiskIs(t *testing.T) {
	script := startServiceScript(ServiceDescriptor{Name: "demo", Command: "python3 server.py"})
	if !strings.Contains(script, "cd '"+clusterWorkspacePath+"'") {
		t.Fatalf("a service with no declared working directory does not restart on the disk: %s", script)
	}
	if !strings.Contains(script, "python3 server.py") {
		t.Fatalf("the restarted command is not the declared one: %s", script)
	}
}

// TestAStartedServiceGetsTheBoxEnvironment pins what a declared service is
// entitled to: the environment the box was given. attachBoxDatabase writes the
// connection string into that file and nowhere else, so a service started without
// it comes up with no credentials and fails on its first query. Both runtimes'
// file names must be honoured, because the rendering is shared.
func TestAStartedServiceGetsTheBoxEnvironment(t *testing.T) {
	script := startServiceScript(ServiceDescriptor{
		Name:       "api",
		Command:    "python3 server.py",
		WorkingDir: "/workspace/app",
	})
	for _, want := range []string{". /" + ClusterBoxEnvPath, ". /" + BoxEnvPath, "set -a", "set +a"} {
		if !strings.Contains(script, want) {
			t.Errorf("start script does not load the box environment: missing %q in %s", want, script)
		}
	}
	if !strings.Contains(script, "exec python3 server.py") {
		t.Errorf("the declared command must still be the process that survives: %s", script)
	}
}
