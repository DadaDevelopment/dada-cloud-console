package profiles

import "testing"

func TestLookupKnown(t *testing.T) {
	cases := []struct {
		name  string
		isGPU bool
	}{
		{"cpu-small", false},
		{"cpu-medium", false},
		{"gpu-t4", true},
		{"gpu-a100", true},
	}
	for _, tc := range cases {
		p, ok := Lookup(tc.name)
		if !ok {
			t.Fatalf("Lookup(%q) returned !ok", tc.name)
		}
		if p.IsGPU() != tc.isGPU {
			t.Errorf("%s IsGPU=%v want %v", tc.name, p.IsGPU(), tc.isGPU)
		}
		if p.CPU == "" || p.Memory == "" {
			t.Errorf("%s missing cpu/memory", tc.name)
		}
		if tc.isGPU && p.GPUVendor == "" {
			t.Errorf("%s GPU profile missing GPUVendor", tc.name)
		}
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, ok := Lookup("nonexistent"); ok {
		t.Errorf("Lookup(nonexistent) should return !ok")
	}
	if _, ok := Lookup(""); ok {
		t.Errorf("Lookup(\"\") should return !ok")
	}
}

func TestNamesOrder(t *testing.T) {
	names := Names()
	if len(names) != len(V1) {
		t.Fatalf("Names length %d != catalog length %d", len(names), len(V1))
	}
	for i, n := range names {
		if n != V1[i].Name {
			t.Errorf("Names[%d]=%q want %q", i, n, V1[i].Name)
		}
	}
}
