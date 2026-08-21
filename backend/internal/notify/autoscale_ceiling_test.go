package notify

import (
	"strings"
	"testing"
)

// A refusal that came from the platform's own namespace policy must never be
// dressed up as "you are at the maximum profile, go find a leak in your code".
// fonbet-value sat at mem 1Gi/2Gi -- an eighth of the platform cap -- and got
// exactly that email while our LimitRange was what refused the growth.
func TestComposeAutoscaleCeilingDoesNotBlameUserCodeForPlatformLimit(t *testing.T) {
	for _, refusal := range []string{"limitrange_capped", "quota_blocked"} {
		subject, body := ComposeAutoscaleCeiling("fonbet-value", "cpu 500m/2, mem 1Gi/2Gi", "memory", refusal, 1.94, "https://console.example/app")
		if strings.Contains(body, "утечк") || strings.Contains(body, "зациклившейся") {
			t.Errorf("refusal %q blames the owner's code: %s", refusal, body)
		}
		if strings.Contains(subject, "максимальном профиле") || strings.Contains(body, "максимальном профиле") {
			t.Errorf("refusal %q claims the app is at the maximum profile: %s / %s", refusal, subject, body)
		}
		if !strings.Contains(body, "на нашей стороне") {
			t.Errorf("refusal %q does not name the limit as ours: %s", refusal, body)
		}
	}
}

// The genuine top of the ladder keeps the old text: there the owner's code
// really is the likelier cause and saying otherwise would be the mirror lie.
func TestComposeAutoscaleCeilingStillPointsAtCodeAtTheRealCeiling(t *testing.T) {
	subject, body := ComposeAutoscaleCeiling("some-app", "cpu 8, mem 16Gi/16Gi", "memory", "at_ceiling", 1.5, "https://console.example/app")
	if !strings.Contains(subject, "максимальном профиле") {
		t.Errorf("at_ceiling subject lost the maximum-profile wording: %s", subject)
	}
	if !strings.Contains(body, "утечк") {
		t.Errorf("at_ceiling body no longer points at a leak: %s", body)
	}
}
