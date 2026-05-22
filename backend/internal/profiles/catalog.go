// Package profiles holds the AI Studio compute-profile catalog (D5).
//
// Profiles are config, not a CRD: edit this file and redeploy. v1 contains
// four profiles; future entries should preserve the cpu-* / gpu-* prefix
// convention because backend logic keys off it for the GPU-approval gate.
package profiles

// Profile is one named compute profile resolvable to XRD resources fields.
type Profile struct {
	Name      string // cpu-small, gpu-t4, ...
	CPU       string // raw resources.cpu, e.g. "1", "2", "4"
	Memory    string // raw resources.memory, e.g. "2Gi"
	GPU       string // resources.gpu count, "" disables. Always "" for cpu-* profiles.
	GPUVendor string // displayed only; e.g. "nvidia.com/gpu". Empty for cpu-* profiles.
}

// IsGPU returns true for any profile that requests a GPU. Used by the
// CreateAIModel handler to decide between Created and WaitingForApproval.
func (p Profile) IsGPU() bool { return p.GPU != "" }

// V1 holds the v1 catalog. Frozen at deploy time.
var V1 = []Profile{
	{Name: "cpu-small", CPU: "1", Memory: "2Gi"},
	{Name: "cpu-medium", CPU: "2", Memory: "4Gi"},
	{Name: "gpu-t4", CPU: "4", Memory: "16Gi", GPU: "1", GPUVendor: "nvidia.com/gpu"},
	{Name: "gpu-a100", CPU: "8", Memory: "32Gi", GPU: "1", GPUVendor: "nvidia.com/gpu"},
}

// Lookup returns the Profile by name. Second return is false if name is unknown.
func Lookup(name string) (Profile, bool) {
	for _, p := range V1 {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// Names returns the catalog names in display order.
func Names() []string {
	out := make([]string, len(V1))
	for i, p := range V1 {
		out[i] = p.Name
	}
	return out
}
