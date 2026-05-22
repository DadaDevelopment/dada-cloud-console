package api

import "testing"

// kserveURL + kserveProtocolForModelType are pure routing helpers.
// They map model_type to the right v1/v2 path on the in-cluster predictor.
// Regressions here would silently break the playground for the affected
// model types, so the matrix is worth pinning explicitly.

func TestKServeProtocolForModelType(t *testing.T) {
	v1Types := []string{"sklearn", "xgboost", "lightgbm", "pytorch", "tensorflow"}
	v2Types := []string{"huggingface", "triton", "custom", "unknown-future-type"}

	for _, mt := range v1Types {
		path, isV2 := kserveProtocolForModelType(mt)
		if isV2 {
			t.Errorf("%q: expected v1, got v2", mt)
		}
		if path != "v1" {
			t.Errorf("%q: expected path=v1, got %q", mt, path)
		}
	}
	for _, mt := range v2Types {
		path, isV2 := kserveProtocolForModelType(mt)
		if !isV2 {
			t.Errorf("%q: expected v2, got v1", mt)
		}
		if path != "v2" {
			t.Errorf("%q: expected path=v2, got %q", mt, path)
		}
	}
}

func TestKServeURL(t *testing.T) {
	cases := []struct {
		name      string
		modelName string
		ns        string
		modelType string
		want      string
	}{
		{
			name:      "v1 sklearn",
			modelName: "iris",
			ns:        "internal-prod",
			modelType: "sklearn",
			want:      "http://iris-predictor.internal-prod.svc.cluster.local/v1/models/iris:predict",
		},
		{
			name:      "v2 huggingface",
			modelName: "llama",
			ns:        "internal-prod",
			modelType: "huggingface",
			want:      "http://llama-predictor.internal-prod.svc.cluster.local/v2/models/llama/infer",
		},
		{
			name:      "v2 custom",
			modelName: "bert",
			ns:        "team-dev",
			modelType: "custom",
			want:      "http://bert-predictor.team-dev.svc.cluster.local/v2/models/bert/infer",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := kserveURL(c.modelName, c.ns, c.modelType)
			if got != c.want {
				t.Errorf("kserveURL = %q, want %q", got, c.want)
			}
		})
	}
}
