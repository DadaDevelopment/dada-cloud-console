package boxcatalog

import "testing"

func TestLookupImage(t *testing.T) {
	img, ok := LookupImage("warm-v1")
	if !ok {
		t.Fatal("warm-v1 not found in the catalog")
	}
	if img.Ref == "" {
		t.Error("warm-v1 has no registry ref")
	}
	if len(img.Toolchain) == 0 {
		t.Error("warm-v1 declares no toolchain; the readiness canary would have nothing to probe")
	}
	if _, ok := LookupImage("warm-v99"); ok {
		t.Error("LookupImage accepted an unknown name")
	}
	if _, ok := LookupImage(""); ok {
		t.Error("LookupImage accepted the empty name")
	}
}

func TestLookupSize(t *testing.T) {
	s, ok := LookupSize("box-standard")
	if !ok {
		t.Fatal("box-standard not found in the catalog")
	}
	if s.VCPU != 2 || s.MemoryMB != 4096 || s.DiskGB != 20 {
		t.Errorf("box-standard = %d vCPU / %d MB / %d GB, want 2 / 4096 / 20 (the host arithmetic behind 6-8 boxes per host)", s.VCPU, s.MemoryMB, s.DiskGB)
	}
	if _, ok := LookupSize("box-enormous"); ok {
		t.Error("LookupSize accepted an unknown name")
	}
}

func TestNamesAreInDeclarationOrder(t *testing.T) {
	names := SizeNames()
	if len(names) != len(V1Sizes) {
		t.Fatalf("SizeNames returned %d entries, catalog has %d", len(names), len(V1Sizes))
	}
	for i, n := range names {
		if n != V1Sizes[i].Name {
			t.Errorf("SizeNames[%d] = %q, want %q (display order must match declaration order)", i, n, V1Sizes[i].Name)
		}
	}
	imgs := ImageNames()
	if len(imgs) != len(V1Images) {
		t.Fatalf("ImageNames returned %d entries, catalog has %d", len(imgs), len(V1Images))
	}
}

// TestExactlyOneDefault is the guard behind the panics in DefaultImage/DefaultSize.
// It runs here so a catalog edit that adds a second default (or drops the only
// one) fails in CI rather than at process start in production — and so the panic
// is provably unreachable on a request path.
func TestExactlyOneDefault(t *testing.T) {
	var imgDefaults, sizeDefaults int
	for _, img := range V1Images {
		if img.Default {
			imgDefaults++
		}
	}
	for _, s := range V1Sizes {
		if s.Default {
			sizeDefaults++
		}
	}
	if imgDefaults != 1 {
		t.Errorf("catalog declares %d default images, want exactly 1", imgDefaults)
	}
	if sizeDefaults != 1 {
		t.Errorf("catalog declares %d default sizes, want exactly 1", sizeDefaults)
	}

	if got := DefaultImage().Name; got == "" {
		t.Error("DefaultImage returned an unnamed image")
	}
	if got := DefaultSize().Name; got == "" {
		t.Error("DefaultSize returned an unnamed size")
	}
}

// TestSizesFitTheHostFlavour keeps the catalog honest about the substrate. Memory
// is NEVER oversubscribed on a box host (a runaway agent build must not OOM its
// neighbour), so no single box may claim more memory than one host has. A catalog
// entry that violates this promises a size the fleet cannot serve, and the failure
// would surface as a spawn that never finds a warm slot.
func TestSizesFitTheHostFlavour(t *testing.T) {
	const hostMemoryMB = 16 * 1024
	const hostVCPU = 8
	const hostDiskGB = 200
	for _, s := range V1Sizes {
		if s.MemoryMB > hostMemoryMB {
			t.Errorf("%s wants %d MB but the host flavour has %d MB and memory is never oversubscribed", s.Name, s.MemoryMB, hostMemoryMB)
		}
		if s.VCPU > hostVCPU {
			t.Errorf("%s wants %d vCPU but the host flavour has %d", s.Name, s.VCPU, hostVCPU)
		}
		if s.DiskGB > hostDiskGB {
			t.Errorf("%s wants %d GB but the host flavour has %d GB", s.Name, s.DiskGB, hostDiskGB)
		}
		if s.MaxTTLSeconds <= 0 {
			t.Errorf("%s has no TTL ceiling; a claim of it could hold a warm slot forever", s.Name)
		}
	}
}
