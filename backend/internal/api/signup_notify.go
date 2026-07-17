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

		totalUsers := -1
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&count); err == nil {
			totalUsers = count
		}

		subject, body := notify.ComposeSignup(email, username, createdAtUTC, totalUsers)
		if err := notifier.Send(to, subject, body); err != nil {
			log.Printf("signup notify: send to %s failed: %v", to, err)
		}
	}()
}
