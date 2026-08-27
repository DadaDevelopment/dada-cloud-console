package api

import (
	"fmt"
	"regexp"
)

var (
	reKubeName  = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,61}[a-z0-9]$|^[a-z0-9]$`)
	rePgName    = regexp.MustCompile(`^[a-z]([a-z0-9\-]{0,61}[a-z0-9])?$`)
	reKeyPrefix = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_\-]{0,127}$`)
)

func validateKubeName(name string) error {
	if !reKubeName.MatchString(name) {
		return fmt.Errorf("name must be lowercase alphanumeric with hyphens, max 63 chars")
	}
	return nil
}

// validatePgName requires a name that is simultaneously a valid PostgreSQL
// identifier and a valid RFC 1123 subdomain. The managed-database CRD
// (ServiceDatabaseV2) carries the k8s track's `database` field verbatim into
// a Crossplane composed resource name, which k8s requires to be RFC 1123 (no
// underscores). Hyphens satisfy both worlds -- a hyphenated identifier is
// legal PostgreSQL as long as it stays quoted, which the provisioning path
// already does -- so hyphens are accepted and underscores are not, on both
// the k8s and VM/compose tracks, keeping one rule for both.
func validatePgName(name string) error {
	if !rePgName.MatchString(name) {
		return fmt.Errorf("database name must start with a lowercase letter and contain only lowercase letters, numbers and hyphens, max 63 chars")
	}
	return nil
}

// validateKeyPrefix requires a name that is safe to embed unquoted into a
// Redis ACL key pattern ("<prefix>:*") and into a k8s Secret name segment
// ("<appRef>-<name>-redis-credentials" does NOT use this value, but the XR's
// own spec.keyPrefix ends up rendered straight into the Composition's ACL
// pattern strings -- see argo-infra's servicecache-composition.yaml). No
// colons: a caller-supplied ":" would let the prefix itself define
// additional pattern segments the console never intended to grant.
func validateKeyPrefix(prefix string) error {
	if !reKeyPrefix.MatchString(prefix) {
		return fmt.Errorf("key_prefix must start with a letter or digit and contain only letters, numbers, hyphens and underscores, max 128 chars")
	}
	return nil
}

// reImage permits registry:port/org/image:tag and mixed-case org names (e.g. ghcr.io/MyOrg/app:v1),
// as well as the digest-pinned form registry/org/image@sha256:<64 hex> used by immutable CI deploys.
// Rules: starts with alphanumeric; path may contain letters, digits, dots, hyphens, slashes, colons
// (to allow registry:port); must end with either :tag (alphanumeric with dots/hyphens) or
// @sha256:<64 hex>; no spaces. The gitops renderer (splitImageRef) resolves both forms.
var reImage = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\-/:]*(:[a-zA-Z0-9._\-]+|@sha256:[a-fA-F0-9]{64})$`)

// ValidateImage checks that an image string is in image:tag or image@sha256:digest format.
func ValidateImage(image string) error {
	if !reImage.MatchString(image) {
		return fmt.Errorf("image must be in image:tag or image@sha256:digest format (e.g. ghcr.io/org/app:v1.0)")
	}
	return nil
}
