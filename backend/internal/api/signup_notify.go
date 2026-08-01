package api

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/jackc/pgx/v5/pgxpool"
)

// serviceAccountUsernamePrefix is Keycloak's default naming for the synthetic
// user a client's "Service Accounts Enabled" flag provisions
// (service-account-<clientId>). Client-credentials tokens carry this as
// preferred_username, so it is a reliable signal that a resolved row is not a
// real human signup.
const serviceAccountUsernamePrefix = "service-account-"

// isServiceAccountPrincipal reports whether kc identifies a Keycloak client's
// service-account user rather than a real registered user.
func isServiceAccountPrincipal(kc *auth.KeycloakClaims) bool {
	return kc != nil && strings.HasPrefix(kc.PreferredUsername, serviceAccountUsernamePrefix)
}

// signupCustomerCount is the "user #N" the owner reads as a milestone. It
// counts the customer cohort only.
//
// A raw count(*) over users answered a different question than the one the
// number is used for: it included the seed rows, Keycloak service accounts,
// the @keycloak.local shells and our own e2e probes, so the milestone ran
// ahead of reality by roughly half (migration 075). The same view already
// backs the admin overview, so both numbers now move together instead of
// disagreeing by cohort.
//
// Returns -1 when the count is unavailable; ComposeSignup renders that as
// unknown rather than as zero.
func signupCustomerCount(ctx context.Context, pool *pgxpool.Pool) int {
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM user_accounts WHERE account_kind = $1",
		overviewCustomerKind,
	).Scan(&count); err != nil {
		log.Printf("signup notify: customer count failed: %v", err)
		return -1
	}
	return count
}

// notifySignup fires the owner email for a brand-new user row, off the
// request's hot path. It is a no-op when notifier or to is unset, or when kc
// identifies a service account. Every failure is logged and swallowed — the
// signup itself must never be affected by a mail outage.
func notifySignup(pool *pgxpool.Pool, notifier *notify.Notifier, to string, kc *auth.KeycloakClaims) {
	if notifier == nil || to == "" || isServiceAccountPrincipal(kc) {
		return
	}

	email := kc.Email
	username := kc.PreferredUsername
	createdAtUTC := time.Now().UTC().Format(time.RFC3339)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		totalUsers := signupCustomerCount(ctx, pool)

		subject, body := notify.ComposeSignup(email, username, createdAtUTC, totalUsers)
		if err := notifier.Send(to, subject, body); err != nil {
			log.Printf("signup notify: send to %s failed: %v", to, err)
		}
	}()
}
