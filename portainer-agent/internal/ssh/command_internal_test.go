package ssh

import "testing"

func TestBootstrapCommand(t *testing.T) {
	cases := map[string]string{
		"":           "bash -s",
		"root":       "bash -s",
		"ubuntuuser": "sudo -n bash -s",
		"ubuntu":     "sudo -n bash -s",
		"admin":      "sudo -n bash -s",
	}
	for user, want := range cases {
		if got := bootstrapCommand(user); got != want {
			t.Errorf("bootstrapCommand(%q) = %q, want %q", user, got, want)
		}
	}
}
