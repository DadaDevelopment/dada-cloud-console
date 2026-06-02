package ssh

import "testing"

func TestDialAddr(t *testing.T) {
	cases := map[string]string{
		"203.0.113.10":      "203.0.113.10:22",   // bare IP → default port
		"203.0.113.10:2222": "203.0.113.10:2222", // explicit port preserved
		"vm.example.com":    "vm.example.com:22",  // bare host → default port
	}
	for in, want := range cases {
		if got := dialAddr(in); got != want {
			t.Errorf("dialAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
