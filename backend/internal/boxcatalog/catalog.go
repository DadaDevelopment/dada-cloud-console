// Package boxcatalog holds the Dada Box warm-image and size catalog.
//
// Deliberately NOT a table, following backend/internal/profiles/catalog.go: a
// frozen Go variable, Lookup, Names, edit-and-redeploy. The reason is stronger
// here than for compute profiles. A box size is only real if the pool controller
// has pre-warmed sandboxes of that size on hosts that can fit them, and a warm
// image is only real if it has been pulled and digest-pinned onto every box host.
// A row an operator can INSERT at 3am would promise a size the fleet cannot serve
// and a spawn would fail at claim time with no warm slot. A deploy, by contrast,
// is exactly the event that also rolls the pool config and the image pin, so the
// catalog and the fleet move together by construction.
//
// Adding an entry is therefore a code change plus a deploy, on purpose.
package boxcatalog

// Image is one pre-baked warm box image.
//
// Digest is the pin. A box image is ~8-12GB of pre-warmed package caches, and
// "latest" on a pool of hosts pulled at different times is how two boxes of the
// same name stop behaving the same way — which destroys the reproducibility the
// product sells. Empty Digest means "not pinned yet" and is only acceptable
// before the first fleet exists.
type Image struct {
	Name        string // warm-v1, ...
	Ref         string // registry reference, pinned by digest in Digest
	Digest      string // sha256:... — the actual pin; empty until the image is published
	Description string
	// Toolchain is the key=value contract the readiness canary probes inside the
	// box. It is data rather than prose because internal/box/readiness.go parses
	// key=value output from the guest rather than matching vendor version banners,
	// so a missing tool is a precise failure instead of a regex miss.
	Toolchain []string
	// Default marks the image a caller gets when it names none. Exactly one entry
	// must carry it; DefaultImage panics otherwise, at init, in tests.
	Default bool
}

// Size is one box shape: CPU, memory, disk.
//
// Memory is never oversubscribed on a box host (a runaway agent build must not
// OOM its neighbour), so MemoryMB is the number that actually decides density and
// therefore cost. CPU is oversubscribed ~3x, which is why VCPU is a ceiling
// (cgroup cpu.max) rather than a reservation.
type Size struct {
	Name     string // box-standard, box-large, box-xl
	VCPU     int    // cgroup v2 cpu.max, in whole cores
	MemoryMB int    // cgroup v2 memory.max
	DiskGB   int    // XFS project quota on the overlay upper dir
	// MaxTTLSeconds caps how long a claim of this size may live before it sleeps.
	// Bigger boxes hold more of a host hostage, so they get no more rope.
	MaxTTLSeconds int
	Default       bool
}

// V1Images is the v1 warm-image catalog. Frozen at deploy time.
//
// One image on purpose. Round 3's conclusion was that the pre-baked image IS the
// product: the pre-warmed npm/pip/go/cargo caches are what make an install inside
// a box faster than on the customer's laptop. A second image halves the warm-pool
// hit rate for the same host budget, so a second entry has to earn itself with
// measured demand, not with a guess about preferences.
var V1Images = []Image{
	{
		Name:        "warm-v1",
		Ref:         "ghcr.io/dadadevelopment/dada-box-warm:v1",
		Digest:      "",
		Description: "Ubuntu 24.04 with node, python 3.12+uv, go, rust, build tools, psql, redis-cli, tmux and git. No container daemon: a box pod drops every capability under PSS restricted, so docker is not part of what this image promises.",
		Toolchain:   []string{"node", "python3", "go", "cargo", "git", "psql"},
		Default:     true,
	},
}

// V1Sizes is the v1 size catalog. Frozen at deploy time.
//
// The standard box is 2 vCPU / 4GiB because that fits 6-8 boxes on the 8 vCPU /
// 16GB / 200GB host flavour at ~3x CPU oversubscription and ZERO memory
// oversubscription. The numbers are host arithmetic, not a price list.
//
// The disk figures are half of what the first cut assumed, and the reason is that
// on the cluster runtime DiskGB stopped being a quota and became a reservation: a
// box workspace is a Longhorn volume, and Longhorn refuses to place one unless the
// node keeps 15% of its disk free afterwards. At 20GiB a box the fleet ran out of
// placements while every node still had a quarter of its disk unused, and the
// symptom was the worst one there is - a new customer asks for a box and does not
// get one. A workspace holds a checkout and a node_modules, not an image, so 10GiB
// is a working size rather than a concession.
var V1Sizes = []Size{
	{Name: "box-standard", VCPU: 2, MemoryMB: 4096, DiskGB: 10, MaxTTLSeconds: 8 * 3600, Default: true},
	{Name: "box-large", VCPU: 4, MemoryMB: 8192, DiskGB: 20, MaxTTLSeconds: 8 * 3600},
	{Name: "box-xl", VCPU: 8, MemoryMB: 16384, DiskGB: 40, MaxTTLSeconds: 8 * 3600},
}

// LookupImage returns the Image by name. Second return is false if name is unknown.
func LookupImage(name string) (Image, bool) {
	for _, img := range V1Images {
		if img.Name == name {
			return img, true
		}
	}
	return Image{}, false
}

// LookupSize returns the Size by name. Second return is false if name is unknown.
func LookupSize(name string) (Size, bool) {
	for _, s := range V1Sizes {
		if s.Name == name {
			return s, true
		}
	}
	return Size{}, false
}

// ImageNames returns the image names in display order.
func ImageNames() []string {
	out := make([]string, len(V1Images))
	for i, img := range V1Images {
		out[i] = img.Name
	}
	return out
}

// SizeNames returns the size names in display order.
func SizeNames() []string {
	out := make([]string, len(V1Sizes))
	for i, s := range V1Sizes {
		out[i] = s.Name
	}
	return out
}

// DefaultImage returns the image a caller gets when it names none.
//
// Panics if the catalog does not carry exactly one default. That is on purpose and
// it is cheap: the catalog is a compile-time constant, so the panic can only fire
// in a test or at process start, never on a request — and a catalog with no
// default would otherwise turn every field-omitted BoxUp into a confusing 400.
func DefaultImage() Image {
	var found *Image
	for i := range V1Images {
		if V1Images[i].Default {
			if found != nil {
				panic("boxcatalog: more than one default image")
			}
			found = &V1Images[i]
		}
	}
	if found == nil {
		panic("boxcatalog: no default image")
	}
	return *found
}

// DefaultSize returns the size a caller gets when it names none. Panics under the
// same exactly-one rule as DefaultImage, for the same reason.
func DefaultSize() Size {
	var found *Size
	for i := range V1Sizes {
		if V1Sizes[i].Default {
			if found != nil {
				panic("boxcatalog: more than one default size")
			}
			found = &V1Sizes[i]
		}
	}
	if found == nil {
		panic("boxcatalog: no default size")
	}
	return *found
}
