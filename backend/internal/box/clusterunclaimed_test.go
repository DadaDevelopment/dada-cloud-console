package box

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// unclaimedRig builds a namespace holding one of every object a box can leave
// behind, all of them older than unclaimedGrace, plus the clock the sweep reads.
func unclaimedRig(t *testing.T) (*fake.Clientset, *ClusterRuntime) {
	t.Helper()
	start := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	old := metav1.NewTime(start.Add(-24 * time.Hour))
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "box-live", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxID: "live", labelBoxPhase: phaseLive, labelBoxName: "sunny-otter"},
		}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "box-live-workspace", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxID: "live"},
		}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "box-parked", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxID: "parked", labelBoxPhase: phaseParked},
		}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "box-parked-workspace", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxID: "parked"},
		}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "box-ghost", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxID: "ghost", labelBoxPhase: phaseLive},
		}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "box-ghost-workspace", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxID: "ghost"},
		}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "box-tombstoned-workspace", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxID: "tombstoned"},
		}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: "crystal-kept", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelCrystal: "kept"},
		}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: "crystal-debris", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelCrystal: "debris"},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name: "crystal-debris", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelCrystal: "debris"},
		}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{
			Name: "crystal-debris", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelCrystal: "debris"},
		}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "crystal-debris", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelCrystal: "debris"},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name: "sunny-otter-3000", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxName: "sunny-otter"},
		}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{
			Name: "sunny-otter-3000", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxName: "sunny-otter"},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name: "dead-badger-8080", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxName: "dead-badger"},
		}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{
			Name: "dead-badger-8080", Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true", labelBoxName: "dead-badger"},
		}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: noindexConfigMap, Namespace: "dada-boxes", CreationTimestamp: old,
			Labels: map[string]string{labelBox: "true"},
		}},
	)
	return cs, newClusterRuntime(cs, nil, "dada-boxes", NewFakeClock(start))
}

// liveSet is what the control plane claims in unclaimedRig: one running box with
// one published port, and one crystallized artifact.
func liveSet() LiveObjects {
	return LiveObjects{
		InstanceRefs: map[string]struct{}{"box-live": {}},
		BoxNames:     map[string]struct{}{"sunny-otter": {}},
		Crystals:     map[string]struct{}{"kept": {}},
		Complete:     true,
	}
}

func has(t *testing.T, cs *fake.Clientset, kind, name string) bool {
	t.Helper()
	ctx := context.Background()
	var err error
	switch kind {
	case "pod":
		_, err = cs.CoreV1().Pods("dada-boxes").Get(ctx, name, metav1.GetOptions{})
	case "pvc":
		_, err = cs.CoreV1().PersistentVolumeClaims("dada-boxes").Get(ctx, name, metav1.GetOptions{})
	case "deploy":
		_, err = cs.AppsV1().Deployments("dada-boxes").Get(ctx, name, metav1.GetOptions{})
	case "svc":
		_, err = cs.CoreV1().Services("dada-boxes").Get(ctx, name, metav1.GetOptions{})
	case "ing":
		_, err = cs.NetworkingV1().Ingresses("dada-boxes").Get(ctx, name, metav1.GetOptions{})
	default:
		t.Fatalf("unknown kind %q", kind)
	}
	return err == nil
}

// TestReapUnclaimedCollectsEveryLeakShape is the pass ReapOrphans could not be:
// it walks the four object kinds a box can leave behind and removes exactly the
// ones no database row accounts for. Each of these was a real leak — together
// they were 15.6% of the platform bill at zero external demand.
//
// The report counts eight for nine removals, and that is not an off-by-one: a box
// body is destroyed together with its workspace in one call, so the ghost's PVC is
// already gone by the time the claims pass lists them. The report says what the
// sweep did, not how many objects stopped existing.
func TestReapUnclaimedCollectsEveryLeakShape(t *testing.T) {
	cs, rt := unclaimedRig(t)

	report, err := rt.ReapUnclaimed(context.Background(), liveSet())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	gone := []struct{ kind, name string }{
		{"pod", "box-ghost"},
		{"pvc", "box-ghost-workspace"},
		{"pvc", "box-tombstoned-workspace"},
		{"deploy", "crystal-debris"},
		{"svc", "crystal-debris"},
		{"ing", "crystal-debris"},
		{"pvc", "crystal-debris"},
		{"svc", "dead-badger-8080"},
		{"ing", "dead-badger-8080"},
	}
	for _, g := range gone {
		if has(t, cs, g.kind, g.name) {
			t.Errorf("%s/%s survived the sweep; nothing in the database names it", g.kind, g.name)
		}
	}
	want := UnclaimedReport{Pods: 1, Claims: 2, Deployments: 1, Services: 2, Ingresses: 2}
	if report != want {
		t.Errorf("report was %s, want %s", report.String(), want.String())
	}
}

// TestReapUnclaimedLeavesClaimedObjectsAlone is the half that matters more. The
// sweep deletes running customer bodies and permanent artifacts if it is wrong
// about ownership, so a live box, its published hostname, a crystallized artifact
// and the warm pool all have to survive a pass that found garbage next to them.
func TestReapUnclaimedLeavesClaimedObjectsAlone(t *testing.T) {
	cs, rt := unclaimedRig(t)

	if _, err := rt.ReapUnclaimed(context.Background(), liveSet()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	kept := []struct{ kind, name, why string }{
		{"pod", "box-live", "a box with a live row"},
		{"pvc", "box-live-workspace", "the workspace of a live box"},
		{"pod", "box-parked", "a warm pool body, which has no row by design"},
		{"pvc", "box-parked-workspace", "the disk of a warm pool body"},
		{"deploy", "crystal-kept", "a crystallized artifact the customer paid for"},
		{"svc", "sunny-otter-3000", "the published port of a live box"},
		{"ing", "sunny-otter-3000", "the hostname of a live box"},
	}
	for _, k := range kept {
		if !has(t, cs, k.kind, k.name) {
			t.Errorf("sweep deleted %s/%s — %s", k.kind, k.name, k.why)
		}
	}
	if _, err := cs.CoreV1().ConfigMaps("dada-boxes").Get(context.Background(), noindexConfigMap, metav1.GetOptions{}); err != nil {
		t.Errorf("sweep deleted the shared noindex ConfigMap: %v", err)
	}
}

// TestReapUnclaimedRefusesAnIncompleteLiveSet pins the interlock. An unreachable
// database yields an empty live set, which is indistinguishable from an empty
// namespace — and acting on that reading deletes every running customer box. The
// shared postgres went down twice in the week this was written, so this is the
// path that decides whether an outage costs a tick of leak or the whole fleet.
func TestReapUnclaimedRefusesAnIncompleteLiveSet(t *testing.T) {
	cs, rt := unclaimedRig(t)

	report, err := rt.ReapUnclaimed(context.Background(), LiveObjects{})
	if err == nil {
		t.Fatal("sweep accepted an incomplete live set; want a refusal")
	}
	if report.Total() != 0 {
		t.Fatalf("sweep removed %d objects before refusing", report.Total())
	}
	if !has(t, cs, "pod", "box-live") || !has(t, cs, "pvc", "box-live-workspace") {
		t.Fatal("a refused sweep still deleted a live box")
	}
	if !has(t, cs, "pod", "box-ghost") {
		t.Error("a refused sweep must change nothing at all, not even the garbage")
	}
}

// TestReapUnclaimedSparesYoungObjects covers the race the grace exists for: a
// cold-start spawn creates its pod before it writes its row, and a crystallization
// creates four objects over several seconds. An object younger than the grace is
// not garbage, it is mid-write.
func TestReapUnclaimedSparesYoungObjects(t *testing.T) {
	cs, rt := unclaimedRig(t)
	now := rt.clock.Now()

	fresh := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "box-newborn", Namespace: "dada-boxes",
		CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
		Labels:            map[string]string{labelBox: "true", labelBoxID: "newborn", labelBoxPhase: phaseLive},
	}}
	if _, err := cs.CoreV1().Pods("dada-boxes").Create(context.Background(), fresh, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	freshClaim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name: "box-newborn-workspace", Namespace: "dada-boxes",
		CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
		Labels:            map[string]string{labelBox: "true", labelBoxID: "newborn"},
	}}
	if _, err := cs.CoreV1().PersistentVolumeClaims("dada-boxes").Create(context.Background(), freshClaim, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := rt.ReapUnclaimed(context.Background(), liveSet()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !has(t, cs, "pod", "box-newborn") || !has(t, cs, "pvc", "box-newborn-workspace") {
		t.Fatal("the sweep reaped a box that was still being created; the grace is what makes a spawn safe")
	}
}
