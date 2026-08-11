package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func podWithStatus(appName string, statuses ...corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-abc12",
			Namespace: "acme-prod",
			Labels:    map[string]string{"dada.io/app": appName},
		},
		Status: corev1.PodStatus{ContainerStatuses: statuses},
	}
}

func TestDetectPodAlertCrashLoopBackOff(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	})
	alert, bad := detectPodAlert(pod)
	if !bad {
		t.Fatalf("expected bad state detected")
	}
	if alert.Reason != reasonCrashLoopBackOff || alert.AppName != "web" || alert.PodName != "web-abc12" {
		t.Fatalf("unexpected alert: %+v", alert)
	}
}

func TestDetectPodAlertImagePullBackOff(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
	})
	alert, bad := detectPodAlert(pod)
	if !bad || alert.Reason != reasonImagePullBackOff {
		t.Fatalf("expected ImagePullBackOff alert, got bad=%v alert=%+v", bad, alert)
	}
}

func TestDetectPodAlertOOMKilled(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name: "web",
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
		},
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	})
	alert, bad := detectPodAlert(pod)
	if !bad || alert.Reason != reasonOOMKilled {
		t.Fatalf("expected OOMKilled to win over CrashLoopBackOff, got bad=%v alert=%+v", bad, alert)
	}
}

func TestDetectPodAlertHealthyPodIgnored(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	if _, bad := detectPodAlert(pod); bad {
		t.Fatalf("expected healthy running pod to not alert")
	}
}

func TestDetectPodAlertSkipsPodsWithoutAppLabel(t *testing.T) {
	pod := podWithStatus("", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	})
	if _, bad := detectPodAlert(pod); bad {
		t.Fatalf("expected pod without dada.io/app label to be skipped")
	}
}

func TestDetectPodAlertPendingPullNotYetBackoffIgnored(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
	})
	if _, bad := detectPodAlert(pod); bad {
		t.Fatalf("expected ContainerCreating (normal transient state) to not alert")
	}
}

func TestDetectPodAlertPlainExitCodeWithRestartDetected(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:         "web",
		RestartCount: 1,
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
		},
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	})
	alert, bad := detectPodAlert(pod)
	if !bad || alert.Reason != reasonCrashLoopBackOff {
		t.Fatalf("expected CrashLoopBackOff to still win over plain exit code, got bad=%v alert=%+v", bad, alert)
	}
}

func TestDetectPodAlertPlainExitCodeNoWaitingReasonDetected(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:         "web",
		RestartCount: 1,
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
		},
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
	})
	alert, bad := detectPodAlert(pod)
	if !bad || alert.Reason != reasonError || alert.ExitCode != 1 {
		t.Fatalf("expected reasonError alert with exit code 1, got bad=%v alert=%+v", bad, alert)
	}
}

func TestDetectPodAlertExitCodeZeroIgnored(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:         "web",
		RestartCount: 1,
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 0},
		},
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	if _, bad := detectPodAlert(pod); bad {
		t.Fatalf("expected exit code 0 to not alert")
	}
}

func TestDetectPodAlertExitCodeNoRestartYetIgnored(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:         "web",
		RestartCount: 0,
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
		},
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
	})
	if _, bad := detectPodAlert(pod); bad {
		t.Fatalf("expected exit code with RestartCount=0 (still first attempt) to not alert")
	}
}

func TestDetectPodAlertOOMKilledStillWinsOverPlainExitCode(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:         "web",
		RestartCount: 1,
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137},
		},
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	alert, bad := detectPodAlert(pod)
	if !bad || alert.Reason != reasonOOMKilled {
		t.Fatalf("expected OOMKilled to win over plain exit code, got bad=%v alert=%+v", bad, alert)
	}
}

func TestEmailableReasonExcludesPlainExitCode(t *testing.T) {
	if emailableReason(reasonError) {
		t.Fatalf("expected reasonError to not be emailable")
	}
	for _, r := range []string{reasonOOMKilled, reasonCrashLoopBackOff, reasonImagePullBackOff, reasonErrImagePull} {
		if !emailableReason(r) {
			t.Fatalf("expected %s to remain emailable", r)
		}
	}
}

// appHealthSendOutcomeRow is the subset of app_health_alerts columns
// recordAppHealthAlertSend touches, read back for assertions.
type appHealthSendOutcomeRow struct {
	lastSentAt        time.Time
	lastSendAttemptAt *time.Time
	lastSendOK        *bool
	lastSendError     *string
	lastRecipient     *string
	sendFailures      int
}

func readAppHealthSendOutcome(t *testing.T, namespace, appName string) appHealthSendOutcomeRow {
	t.Helper()
	pool := alertRecipientTestPool(t)
	var row appHealthSendOutcomeRow
	err := pool.QueryRow(context.Background(),
		`SELECT last_sent_at, last_send_attempt_at, last_send_ok, last_send_error, last_recipient, send_failures
		 FROM app_health_alerts WHERE namespace = $1 AND app_name = $2`,
		namespace, appName,
	).Scan(&row.lastSentAt, &row.lastSendAttemptAt, &row.lastSendOK, &row.lastSendError, &row.lastRecipient, &row.sendFailures)
	if err != nil {
		t.Fatalf("read app_health_alerts row for %s/%s: %v", namespace, appName, err)
	}
	return row
}

// TestRecordAppHealthAlertSend_Success exercises the happy path: last_sent_at
// (the 24h cooldown gate claimAppHealthAlertSlot already set) is left alone,
// and the new columns record a delivered email with a clean slate.
func TestRecordAppHealthAlertSend_Success(t *testing.T) {
	pool := alertRecipientTestPool(t)
	ctx := context.Background()
	namespace := "ns-send-ok-" + uuid.NewString()[:8]
	appName := "app-send-ok"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, namespace)
	})

	if !claimAppHealthAlertSlot(ctx, pool, namespace, appName, reasonCrashLoopBackOff, "pod/app", appHealthAlertCooldown) {
		t.Fatal("expected the claim on a brand new row to succeed")
	}
	before := readAppHealthSendOutcome(t, namespace, appName)

	recordAppHealthAlertSend(ctx, pool, namespace, appName, "owner@example.com", nil)

	after := readAppHealthSendOutcome(t, namespace, appName)
	if after.lastSendOK == nil || !*after.lastSendOK {
		t.Fatalf("expected last_send_ok = true, got %v", after.lastSendOK)
	}
	if after.lastSendError != nil {
		t.Fatalf("expected last_send_error IS NULL, got %q", *after.lastSendError)
	}
	if after.lastRecipient == nil || *after.lastRecipient != "owner@example.com" {
		t.Fatalf("expected last_recipient = owner@example.com, got %v", after.lastRecipient)
	}
	if after.sendFailures != 0 {
		t.Fatalf("expected send_failures = 0 on success, got %d", after.sendFailures)
	}
	if !after.lastSentAt.Equal(before.lastSentAt) {
		t.Fatalf("expected last_sent_at untouched by a successful send: before=%v after=%v", before.lastSentAt, after.lastSentAt)
	}
}

// TestRecordAppHealthAlertSend_FailureBacksOffWithoutBurningCooldown proves
// the core fix: a failed send is recorded (not just logged), and does not
// burn the full 24h cooldown slot the way a bare claim-before-send would.
// Instead it gives back a bounded appHealthAlertRetryBackoff window, so the
// next claim attempt fails immediately after (still backing off) but
// succeeds once the backoff has elapsed. A subsequent successful send on the
// same row clears the failure back to a clean slate (send_failures counts
// consecutive failures, not lifetime ones).
func TestRecordAppHealthAlertSend_FailureBacksOffWithoutBurningCooldown(t *testing.T) {
	pool := alertRecipientTestPool(t)
	ctx := context.Background()
	namespace := "ns-send-fail-" + uuid.NewString()[:8]
	appName := "app-send-fail"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, namespace)
	})

	if !claimAppHealthAlertSlot(ctx, pool, namespace, appName, reasonCrashLoopBackOff, "pod/app", appHealthAlertCooldown) {
		t.Fatal("expected the claim on a brand new row to succeed")
	}

	sendErr := errors.New("relay error: не удалось доставить письмо, ящик недоступен, попробуйте позже " + strings.Repeat("х", appHealthSendErrorMaxLen))
	recordAppHealthAlertSend(ctx, pool, namespace, appName, "owner@example.com", sendErr)

	row := readAppHealthSendOutcome(t, namespace, appName)
	if row.lastSendOK == nil || *row.lastSendOK {
		t.Fatalf("expected last_send_ok = false, got %v", row.lastSendOK)
	}
	if row.lastSendError == nil || *row.lastSendError == "" {
		t.Fatal("expected last_send_error to be recorded")
	}
	if got := []rune(*row.lastSendError); len(got) > appHealthSendErrorMaxLen {
		t.Fatalf("expected last_send_error truncated to %d runes, got %d", appHealthSendErrorMaxLen, len(got))
	}
	if !strings.Contains(*row.lastSendError, "не удалось доставить") {
		t.Fatalf("expected truncation to preserve valid UTF-8 Cyrillic prefix, got %q", *row.lastSendError)
	}
	if row.sendFailures != 1 {
		t.Fatalf("expected send_failures = 1, got %d", row.sendFailures)
	}

	if claimAppHealthAlertSlot(ctx, pool, namespace, appName, reasonCrashLoopBackOff, "pod/app", appHealthAlertCooldown) {
		t.Fatal("expected the immediate re-claim right after a failure to still be blocked by the partial backoff")
	}

	_, err := pool.Exec(ctx,
		`UPDATE app_health_alerts SET last_sent_at = $3 WHERE namespace = $1 AND app_name = $2`,
		namespace, appName, time.Now().Add(-appHealthAlertCooldown-time.Second))
	if err != nil {
		t.Fatalf("simulate backoff elapsed: %v", err)
	}
	if !claimAppHealthAlertSlot(ctx, pool, namespace, appName, reasonCrashLoopBackOff, "pod/app", appHealthAlertCooldown) {
		t.Fatal("expected the claim to succeed once the retry backoff has elapsed")
	}

	recordAppHealthAlertSend(ctx, pool, namespace, appName, "owner@example.com", nil)
	after := readAppHealthSendOutcome(t, namespace, appName)
	if after.sendFailures != 0 {
		t.Fatalf("expected a following successful send to reset send_failures to 0, got %d", after.sendFailures)
	}
	if after.lastSendError != nil {
		t.Fatalf("expected last_send_error cleared after a successful send, got %q", *after.lastSendError)
	}
}

// recoveredContainer builds the live shape this cycle's bug was found on:
// a container that crashed once in the past, restarted, and has been running
// and ready ever since.
func recoveredContainer(name string, upFor time.Duration, now time.Time, lastTerm corev1.ContainerStateTerminated) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:                 name,
		Ready:                true,
		RestartCount:         1,
		State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(-upFor))}},
		LastTerminationState: corev1.ContainerState{Terminated: &lastTerm},
	}
}

func TestDetectPodAlertRecoveredErrorExitIsNotCurrent(t *testing.T) {
	now := time.Now()
	pod := podWithStatus("web", recoveredContainer("web", time.Hour, now,
		corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1}))
	if alert, bad := detectPodAlertAt(pod, now, appHealthRecoveryWindow); bad {
		t.Fatalf("a container ready and up for an hour must not read as crashing, got %+v", alert)
	}
}

func TestDetectPodAlertRecoveredOOMIsNotCurrent(t *testing.T) {
	now := time.Now()
	pod := podWithStatus("web", recoveredContainer("web", 8*24*time.Hour, now,
		corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}))
	if alert, bad := detectPodAlertAt(pod, now, appHealthRecoveryWindow); bad {
		t.Fatalf("an OOM the container recovered from 8 days ago must not re-alert, got %+v", alert)
	}
}

// The reverse break: the gate must not swallow a crash that just happened.
// A container back up for less than the window is still an incident — this is
// the case that fires the owner's email.
func TestDetectPodAlertFreshCrashStillReported(t *testing.T) {
	now := time.Now()
	pod := podWithStatus("web", recoveredContainer("web", time.Minute, now,
		corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1}))
	alert, bad := detectPodAlertAt(pod, now, appHealthRecoveryWindow)
	if !bad || alert.Reason != reasonError || alert.ExitCode != 1 {
		t.Fatalf("a crash one minute old must still alert, got bad=%v alert=%+v", bad, alert)
	}
}

// A container that is up but failing its readiness probe has not recovered,
// however long it has been up.
func TestDetectPodAlertUpButNotReadyIsNotRecovered(t *testing.T) {
	now := time.Now()
	cs := recoveredContainer("web", 24*time.Hour, now,
		corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1})
	cs.Ready = false
	if _, bad := detectPodAlertAt(podWithStatus("web", cs), now, appHealthRecoveryWindow); !bad {
		t.Fatal("a container that is not ready must not count as recovered")
	}
}

// A flapping container never satisfies the uptime gate, so the long-standing
// guarantee that a crashlooping app never reads healthy is untouched.
func TestDetectPodAlertFlappingContainerStillCrashing(t *testing.T) {
	now := time.Now()
	cs := recoveredContainer("web", 20*time.Second, now,
		corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137})
	cs.RestartCount = 42
	alert, bad := detectPodAlertAt(podWithStatus("web", cs), now, appHealthRecoveryWindow)
	if !bad || alert.Reason != reasonOOMKilled {
		t.Fatalf("a container up 20s after 42 restarts is still crashing, got bad=%v alert=%+v", bad, alert)
	}
}

// Backoff is current state, not history: a container sitting in
// CrashLoopBackOff is never gated, because it is not running at all.
func TestDetectPodAlertBackoffUngatedByRecovery(t *testing.T) {
	now := time.Now()
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:         "web",
		RestartCount: 3,
		State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff}},
	})
	if _, bad := detectPodAlertAt(pod, now, appHealthRecoveryWindow); !bad {
		t.Fatal("CrashLoopBackOff must always report regardless of the recovery gate")
	}
}
