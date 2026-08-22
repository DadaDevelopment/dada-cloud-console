package kagent

import (
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrClusterUnavailable is returned when the console cannot see the cluster --
// off-cluster development, or a service account without access to the agent
// namespace. Callers turn it into a 503 with an explanation rather than a 500,
// because nothing is wrong with the request.
var ErrClusterUnavailable = errors.New("agent runtime is not reachable from this console")

func isNotFound(err error) bool { return apierrors.IsNotFound(err) }
