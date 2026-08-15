package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Live incident 2026-08-14: a real payer (artempro2021@bk.ru) clicked "Оплатить"
// twice in 3 seconds. Both YooKassa CreatePayment calls failed after the
// pending payments row was already inserted, and the pod that served the
// request was recycled before anyone read its logs -- the exact provider
// error was never recoverable. The row itself is now marked canceled instead
// of hanging pending forever (see Checkout in
// backend/internal/billing/yookassa/provider.go), but that alone still
// leaves no operator-visible signal that a checkout failed: the row sits
// quietly in a 500-row payments table until someone thinks to query it.
//
// paymentCreateFailures is the metric half of the fix: a bounded counter by
// error_class an alert rule can watch, so a sustained rate of checkout
// failures shows up before the next customer has to be discovered a week
// later in a SQL query.
var paymentCreateFailures = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "dada_payment_create_failures_total",
	Help: "YooKassa CreatePayment calls that failed during checkout, by error class. Any sustained rate here means paying customers are silently failing to check out.",
}, []string{"error_class"})

// RecordPaymentCreateFailure counts one failed CreatePayment call. errorClass
// is one of a short closed set (see classifyPaymentError in
// backend/internal/billing/yookassa/provider.go) -- never the raw error
// string, which would make the label cardinality unbounded.
func RecordPaymentCreateFailure(errorClass string) {
	if errorClass == "" {
		errorClass = "unknown"
	}
	paymentCreateFailures.WithLabelValues(errorClass).Inc()
}

// PaymentCreateFailuresCollectorForTest hands back the counter behind one
// error_class label, so a test in another package can assert on the exact
// series RecordPaymentCreateFailure writes to (via testutil.ToFloat64)
// without exposing the CounterVec itself for production use.
func PaymentCreateFailuresCollectorForTest(errorClass string) prometheus.Counter {
	return paymentCreateFailures.WithLabelValues(errorClass)
}
