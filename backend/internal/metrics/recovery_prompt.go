package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// recoveryPromptsServed counts every platform-recovery prompt actually
// handed to a user by GET /recovery-prompt, by kind. Backlog 0431 exists
// because a platform bug getting fixed and a user getting told about it were
// two unconnected events -- kkartov@yandex.ru sat with zero apps for four
// days after the bug behind his failed installs was already fixed. This
// counter is the acceptance signal for that gap: it must move above zero
// once the fix ships, and the console's own click-through counter (recorded
// client-side) is what proves the prompt did more than get shown.
var recoveryPromptsServed = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "dada_platform_recovery_prompts_served_total",
	Help: "Platform-recovery prompts served to a user via GET /recovery-prompt, by kind. Acceptance signal for backlog 0431 -- the platform's own attempt to bring back a user hit by a bug that has since been fixed.",
}, []string{"kind"})

// RecordRecoveryPromptServed counts one prompt actually served. kind is one
// of the closed set in platformActionFailureFixes (backend/internal/api/
// platform_recovery.go) -- bounded by construction, since it can only ever be
// a Kind literal from that registry.
func RecordRecoveryPromptServed(kind string) {
	recoveryPromptsServed.WithLabelValues(kind).Inc()
}

// RecoveryPromptsServedCollectorForTest hands back the counter behind one
// kind label so a test in another package can assert on the exact series
// RecordRecoveryPromptServed writes to (via testutil.ToFloat64) without
// exposing the CounterVec itself for production use.
func RecoveryPromptsServedCollectorForTest(kind string) prometheus.Counter {
	return recoveryPromptsServed.WithLabelValues(kind)
}
