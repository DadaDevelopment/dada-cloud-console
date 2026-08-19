package api

import "testing"

func TestBuildVolumeUsageFieldsBothDimensionsKnown(t *testing.T) {
	f := buildVolumeUsageFields(5000, 10000, 300, 1000, true)
	if f.Ratio != 0.5 {
		t.Fatalf("expected byte ratio 0.5, got %v", f.Ratio)
	}
	if !f.InodesKnown {
		t.Fatalf("expected inodes known")
	}
	if f.InodesUsed != 300 || f.InodesTotal != 1000 || f.InodesRatio != 0.3 {
		t.Fatalf("unexpected inode fields: %+v", f)
	}
	if f.BindingKind != ratioKindBytes {
		t.Fatalf("expected binding kind bytes when neither ratio crosses threshold, got %s", f.BindingKind)
	}
	j := f.toJSON()
	for _, key := range []string{"used_bytes", "capacity_bytes", "ratio", "binding_kind", "inodes_used", "inodes_total", "inodes_ratio"} {
		if _, ok := j[key]; !ok {
			t.Fatalf("expected key %s in JSON, got %+v", key, j)
		}
	}
}

func TestBuildVolumeUsageFieldsInodeQueryFailed(t *testing.T) {
	f := buildVolumeUsageFields(5000, 10000, 0, 0, false)
	if f.Ratio != 0.5 {
		t.Fatalf("expected byte ratio 0.5 unaffected by inode failure, got %v", f.Ratio)
	}
	if f.InodesKnown {
		t.Fatalf("expected inodes not known when inode query failed")
	}
	if f.BindingKind != ratioKindBytes {
		t.Fatalf("expected binding kind to default to bytes, got %s", f.BindingKind)
	}
	j := f.toJSON()
	for _, key := range []string{"inodes_used", "inodes_total", "inodes_ratio"} {
		if _, ok := j[key]; ok {
			t.Fatalf("expected key %s to be omitted when inodes unknown, got %+v", key, j)
		}
	}
	for _, key := range []string{"used_bytes", "capacity_bytes", "ratio", "binding_kind"} {
		if _, ok := j[key]; !ok {
			t.Fatalf("expected key %s in JSON, got %+v", key, j)
		}
	}
}

// TestBuildVolumeUsageFieldsInodeExhaustedBytesLow reproduces the live
// fonbet-value 2026-08-19 incident: ext4 inodes fully exhausted
// (1310720/1310720) while bytes sit at a comfortable 73% (15359680512 /
// 21024600064). The endpoint must report inodes as the binding constraint
// even though the byte ratio alone would read as healthy.
func TestBuildVolumeUsageFieldsInodeExhaustedBytesLow(t *testing.T) {
	const usedBytes float64 = 15359680512
	const capacityBytes float64 = 21024600064
	const inodesUsed float64 = 1310720
	const inodesTotal float64 = 1310720

	f := buildVolumeUsageFields(usedBytes, capacityBytes, inodesUsed, inodesTotal, true)

	wantByteRatio := usedBytes / capacityBytes
	if f.Ratio != wantByteRatio {
		t.Fatalf("expected byte ratio %v, got %v", wantByteRatio, f.Ratio)
	}
	if f.Ratio >= appVolumeAlertThreshold {
		t.Fatalf("sanity check failed: byte ratio %v should read under threshold %v, or this case no longer proves anything", f.Ratio, appVolumeAlertThreshold)
	}
	if !f.InodesKnown || f.InodesRatio != 1.0 {
		t.Fatalf("expected inodes known and fully exhausted, got %+v", f)
	}
	if f.BindingKind != ratioKindInodes {
		t.Fatalf("expected binding kind inodes for an inode-exhausted, byte-healthy volume, got %s", f.BindingKind)
	}
}
