package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AIModelSource identifies where an AIModel artifact comes from.
type AIModelSource string

const (
	AIModelSourceMLflow AIModelSource = "mlflow"
	AIModelSourceS3     AIModelSource = "s3"
	AIModelSourceCustom AIModelSource = "custom"
)

// AIModelAuthMode mirrors the embedded PublicApi's auth.mode.
type AIModelAuthMode string

const (
	AIModelAuthAPIKey AIModelAuthMode = "apikey"
	AIModelAuthJWT    AIModelAuthMode = "jwt"
	AIModelAuthPublic AIModelAuthMode = "public"
)

// CreateAIModelPayload is the typed payload for CreateAIModel operations.
// Source determines which of MLflowName+MLflowVersion / ArtifactURI /
// ContainerImage is required. Backend resolves Source=mlflow into an S3
// artifactURI before rendering.
type CreateAIModelPayload struct {
	Name            string          `json:"name"`
	ModelType       string          `json:"model_type"` // sklearn|xgboost|lightgbm|pytorch|tensorflow|triton|huggingface|custom
	Source          AIModelSource   `json:"source"`
	MLflowName      string          `json:"mlflow_name,omitempty"`
	MLflowVersion   string          `json:"mlflow_version,omitempty"`
	ArtifactURI     string          `json:"artifact_uri,omitempty"`
	ContainerImage  string          `json:"container_image,omitempty"`
	Profile         string          `json:"profile"`
	AuthMode        AIModelAuthMode `json:"auth_mode"`
	AttachedAppName string          `json:"attached_app_name,omitempty"`
	Version         string          `json:"version,omitempty"`
}

// UpdateAIModelArtifactPayload changes the artifactURI (and triggers a KServe roll).
// Either ArtifactURI or (MLflowName+MLflowVersion) must be set.
type UpdateAIModelArtifactPayload struct {
	Name          string `json:"name"`
	ArtifactURI   string `json:"artifact_uri,omitempty"`
	MLflowName    string `json:"mlflow_name,omitempty"`
	MLflowVersion string `json:"mlflow_version,omitempty"`
}

// SetCanaryTrafficPayload sets canary.trafficPercent on an AIModel.
type SetCanaryTrafficPayload struct {
	Name           string `json:"name"`
	TrafficPercent int    `json:"traffic_percent"`
}

// PromoteAIModelPayload clears canary (sets to 100% / removes the field).
type PromoteAIModelPayload struct {
	Name string `json:"name"`
}

// DeleteAIModelPayload removes an AIModel and its embedded PublicApi.
// Force=true overrides the "attached App" guard (admin-only path).
type DeleteAIModelPayload struct {
	Name  string `json:"name"`
	Force bool   `json:"force,omitempty"`
}

// PinAIModelMlflowVersionPayload pins an AIModel to a specific MLflow registered model+version.
// Resolved to an artifactURI at render time; updating the pin = UpdateAIModelArtifact under the hood.
type PinAIModelMlflowVersionPayload struct {
	Name          string `json:"name"`
	MLflowName    string `json:"mlflow_name"`
	MLflowVersion string `json:"mlflow_version"`
}

// ProjectQuotas reflects per-project AI Studio limits.
type ProjectQuotas struct {
	ProjectID             uuid.UUID `json:"project_id"              db:"project_id"`
	CPUModelMax           int       `json:"cpu_model_max"           db:"cpu_model_max"`
	GPUModelMax           int       `json:"gpu_model_max"           db:"gpu_model_max"`
	MonthlyInferenceCalls int64     `json:"monthly_inference_calls" db:"monthly_inference_calls"`
	UpdatedAt             time.Time `json:"updated_at"              db:"updated_at"`
}

// QuotaUsage summarizes current consumption against quotas for the AI Studio overview widget.
type QuotaUsage struct {
	Quotas              ProjectQuotas `json:"quotas"`
	CPUModelsInUse      int           `json:"cpu_models_in_use"`
	GPUModelsInUse      int           `json:"gpu_models_in_use"`
	InferenceCallsMonth int64         `json:"inference_calls_month"`
}

// AIModelAPIKey is the row in aimodel_api_keys. The full plaintext value is never stored.
type AIModelAPIKey struct {
	ID            uuid.UUID  `json:"id"             db:"id"`
	ProjectID     uuid.UUID  `json:"project_id"     db:"project_id"`
	EnvironmentID uuid.UUID  `json:"environment_id" db:"environment_id"`
	AIModelName   string     `json:"aimodel_name"   db:"aimodel_name"`
	KeyPrefix     string     `json:"key_prefix"     db:"key_prefix"`
	KeyHash       []byte     `json:"-"              db:"key_hash"`
	CreatedAt     time.Time  `json:"created_at"     db:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`
}

// MarshalJSONPayload is a tiny helper for handlers — keeps the call site one line.
func MarshalJSONPayload(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
