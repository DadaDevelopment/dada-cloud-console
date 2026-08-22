package api

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// telemostBotDeployment is internal/prod/telemost-bot as it ran on 2026-08-21:
// twelve environment variables, none of them in the console's env_vars table,
// half of them wired to Secrets, plus the .env Secret pulled in wholesale by
// useDotEnv.
func telemostBotDeployment() *appsv1.Deployment {
	secretRef := func(secret, key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret},
			Key:                  key,
		}}
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "telemost-bot",
			Namespace: "internal-prod",
			Labels:    map[string]string{"dada.io/app": "telemost-bot"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "app",
						Env: []corev1.EnvVar{
							{Name: "BOT_TOKEN", ValueFrom: secretRef("telemost-bot-secrets", "bot_token")},
							{Name: "POSTGRES_HOST", Value: "telemost-bot-db"},
							{Name: "POSTGRES_PASSWORD", ValueFrom: secretRef("telemost-bot-db-credentials", "password")},
							{Name: "LOG_LEVEL", Value: "info"},
						},
						EnvFrom: []corev1.EnvFromSource{{
							SecretRef: &corev1.SecretEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "telemost-bot-dotenv"},
							},
						}},
					}},
				},
			},
		},
	}
}

func handlerWithClientset(cs *fake.Clientset) *Handler {
	h := &Handler{}
	h.platformHealthOnce.Do(func() {})
	h.platformHealthCS = cs
	return h
}

// TestReadClusterEnv_SeesWhatTheConsoleTableDoesNot is the regression test for
// listEnvVars answering {"env_vars":[]} while the workload carried a full
// environment. Every variable the pod runs on must show up, with its provenance,
// and marked as absent from the console.
func TestReadClusterEnv_SeesWhatTheConsoleTableDoesNot(t *testing.T) {
	h := handlerWithClientset(fake.NewSimpleClientset(telemostBotDeployment()))

	snap := h.readClusterEnv(context.Background(), "internal-prod", "telemost-bot", map[string]bool{})
	if !snap.Observed {
		t.Fatalf("the workload exists and must be observed, got reason %q", snap.Reason)
	}
	if len(snap.Vars) != 4 {
		t.Fatalf("want 4 variables off the pod spec, got %d: %+v", len(snap.Vars), snap.Vars)
	}

	byKey := map[string]clusterEnvVar{}
	for _, v := range snap.Vars {
		byKey[v.Key] = v
	}
	if got := byKey["BOT_TOKEN"]; got.From != "secretKeyRef" || got.Ref != "secret/telemost-bot-secrets" {
		t.Errorf("BOT_TOKEN must report its secret provenance, got %+v", got)
	}
	if got := byKey["LOG_LEVEL"]; got.From != "value" {
		t.Errorf("LOG_LEVEL is a literal, got %+v", got)
	}
	for _, v := range snap.Vars {
		if v.InConsole {
			t.Errorf("%s is not in env_vars and must not claim to be", v.Key)
		}
	}
	if len(snap.Sources) != 1 || snap.Sources[0].Name != "telemost-bot-dotenv" {
		t.Fatalf("the useDotEnv Secret must be reported as a bulk source, got %+v", snap.Sources)
	}
}

// TestReadClusterEnv_MarksKeysTheConsoleAlreadyManages keeps the two lists
// joinable: a caller has to be able to tell which live variables it can edit
// through the console from which ones exist only in the cluster.
func TestReadClusterEnv_MarksKeysTheConsoleAlreadyManages(t *testing.T) {
	h := handlerWithClientset(fake.NewSimpleClientset(telemostBotDeployment()))

	snap := h.readClusterEnv(context.Background(), "internal-prod", "telemost-bot",
		map[string]bool{"LOG_LEVEL": true})
	for _, v := range snap.Vars {
		want := v.Key == "LOG_LEVEL"
		if v.InConsole != want {
			t.Errorf("%s: in_console = %v, want %v", v.Key, v.InConsole, want)
		}
	}
}

// TestReadClusterEnv_DoesNotReportAnEmptyEnvironmentWhenItCouldNotLook is the
// point of the observed flag: an unreadable cluster and an app with no variables
// produce the same empty list, and only one of them is a fact.
func TestReadClusterEnv_DoesNotReportAnEmptyEnvironmentWhenItCouldNotLook(t *testing.T) {
	off := &Handler{}
	off.platformHealthOnce.Do(func() {})
	if snap := off.readClusterEnv(context.Background(), "internal-prod", "telemost-bot", nil); snap.Observed {
		t.Fatal("an off-cluster console cannot observe anything and must say so")
	}

	h := handlerWithClientset(fake.NewSimpleClientset())
	snap := h.readClusterEnv(context.Background(), "internal-prod", "ghost", nil)
	if snap.Observed {
		t.Fatal("no workload found must not read as an empty environment")
	}
	if snap.Reason == "" {
		t.Fatal("a non-observation must carry its reason")
	}
}

// TestReadClusterEnv_FallsBackToTheAppNamedDeployment covers hand-maintained
// apps that predate the dada.io/app label -- exactly the class whose environment
// lives outside the console, so exactly the class this read exists for.
func TestReadClusterEnv_FallsBackToTheAppNamedDeployment(t *testing.T) {
	dep := telemostBotDeployment()
	dep.Labels = nil
	h := handlerWithClientset(fake.NewSimpleClientset(dep))

	snap := h.readClusterEnv(context.Background(), "internal-prod", "telemost-bot", nil)
	if !snap.Observed || len(snap.Vars) != 4 {
		t.Fatalf("an unlabelled deployment named after the app must still be read, got %+v", snap)
	}
}
