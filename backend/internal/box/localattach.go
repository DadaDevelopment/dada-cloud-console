package box

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// LocalAttachProvider attaches real managed resources to a running box.
//
// The resource lives OUTSIDE the box, and that is the architecture rather than a
// deployment detail: a disposable body must not own the customer's database, or
// deleting the body would delete the data and crystallization would have nothing
// to carry. So an attach here is (1) provision outside, (2) inject the credential
// into the box's 0600 env file, (3) report exactly which keys were injected.
//
// Postgres is genuinely provisioned: a role and a database are created on the
// managed cluster this provider is pointed at, the box connects to it over the
// network, and the walk proves it with `psql "$DATABASE_URL" -c 'select 1'` run
// INSIDE the box. In production the same seam is satisfied by the operations
// parent/child path — AttachBoxDatabase enqueues a child CreateServiceDatabase
// against the box's environment_id, waits for Committed and resolves the
// Crossplane connection secret (see models.AttachBoxDatabasePayload). That path
// needs Crossplane and a Kubernetes cluster, neither of which exists here, so
// this provider talks to a real Postgres cluster instead of pretending to talk to
// Crossplane.
//
// S3 is NOT implemented and does not pretend to be: AttachS3 returns an error
// naming what it would need. There is no S3-compatible endpoint in this
// environment, and an attach that injected plausible-looking AWS_* values into a
// box would be a fake door inside the product.
type LocalAttachProvider struct {
	// Runtime is where the injected env is written.
	Runtime *LocalRuntime
	// AdminDSN is a superuser connection to the managed cluster the databases are
	// created on. It is the platform's credential and never enters a box.
	AdminDSN string
	// ReachableHost/Port is how the BOX reaches that cluster. Kept separate from
	// AdminDSN because the platform's own path to a database and the tenant's are
	// routinely different, and conflating them is how an injected DSN works from
	// the control plane and fails inside the guest.
	ReachableHost string
	ReachablePort int
}

var _ AttachProvider = (*LocalAttachProvider)(nil)

// ManagedPostgresConfigured reports whether this provider can provision at all.
//
// A handler asks this instead of reading AdminDSN, because "the platform is not
// wired for managed Postgres" is a 503 and "the provision failed" is a 500, and a
// caller holding only the seam cannot tell them apart from an error string.
func (p *LocalAttachProvider) ManagedPostgresConfigured() bool { return p.AdminDSN != "" }

// safeIdent bounds what can become a SQL identifier. Postgres has no placeholder
// for identifiers, so CREATE DATABASE has to interpolate — which makes this
// pattern the actual injection defence rather than a tidiness check.
var safeIdent = regexp.MustCompile(`^[a-z][a-z0-9_]{0,48}$`)

// AttachPostgres satisfies the AttachProvider seam: it returns the injected env
// keys and never the values.
func (p *LocalAttachProvider) AttachPostgres(ctx context.Context, inst *Instance, name, envPrefix string) ([]string, error) {
	injected, _, err := p.AttachPostgresNamed(ctx, inst, name, envPrefix)
	return injected, err
}

// AttachPostgresNamed also reports the database it provisioned.
//
// The name is RETURNED rather than recomputed by the caller. The provider derives
// it from the instance ref and the attachment name, and a second derivation in the
// control plane would be a second place for the sanitization rules to live — so the
// row would eventually record a database name that is not the one that exists.
func (p *LocalAttachProvider) AttachPostgresNamed(ctx context.Context, inst *Instance, name, envPrefix string) ([]string, string, error) {
	if p.AdminDSN == "" {
		return nil, "", fmt.Errorf("box: managed Postgres is not configured (BOX_MANAGED_PG_URL unset)")
	}
	dbName := sanitizeIdent("box_" + strings.ReplaceAll(inst.InstanceRef, "-", "_") + "_" + name)
	roleName := sanitizeIdent("boxr_" + strings.ReplaceAll(inst.InstanceRef, "-", "_") + "_" + name)
	if !safeIdent.MatchString(dbName) || !safeIdent.MatchString(roleName) {
		return nil, "", fmt.Errorf("box: attachment name %q does not produce a usable identifier", name)
	}
	secret, err := randomPassword()
	if err != nil {
		return nil, "", err
	}

	conn, err := pgx.Connect(ctx, p.AdminDSN)
	if err != nil {
		return nil, "", fmt.Errorf("box: connect to managed Postgres: %w", err)
	}
	defer conn.Close(ctx)

	// Idempotent: an attach retried after a timeout must not fail on "already
	// exists" and leave the box without a credential it can use.
	var roleExists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, roleName).Scan(&roleExists); err != nil {
		return nil, "", err
	}
	if roleExists {
		if _, err := conn.Exec(ctx, fmt.Sprintf(`ALTER ROLE %s WITH LOGIN PASSWORD '%s'`, roleName, escapeLiteral(secret))); err != nil {
			return nil, "", fmt.Errorf("box: rotate managed role: %w", err)
		}
	} else if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE ROLE %s WITH LOGIN PASSWORD '%s'`, roleName, escapeLiteral(secret))); err != nil {
		return nil, "", fmt.Errorf("box: create managed role: %w", err)
	}

	var dbExists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&dbExists); err != nil {
		return nil, "", err
	}
	if !dbExists {
		if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, dbName, roleName)); err != nil {
			return nil, "", fmt.Errorf("box: create managed database: %w", err)
		}
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(`GRANT ALL PRIVILEGES ON DATABASE %s TO %s`, dbName, roleName)); err != nil {
		return nil, "", fmt.Errorf("box: grant on managed database: %w", err)
	}

	host := p.ReachableHost
	if host == "" {
		host = "127.0.0.1"
	}
	port := p.ReachablePort
	if port == 0 {
		port = 5432
	}
	dsn := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(roleName, secret),
		Host:     fmt.Sprintf("%s:%d", host, port),
		Path:     "/" + dbName,
		RawQuery: "sslmode=disable",
	}).String()

	prefix := strings.TrimSpace(envPrefix)
	env := map[string]string{
		prefix + "DATABASE_URL": dsn,
		prefix + "PGHOST":       host,
		prefix + "PGPORT":       fmt.Sprint(port),
		prefix + "PGDATABASE":   dbName,
		prefix + "PGUSER":       roleName,
		prefix + "PGPASSWORD":   secret,
	}
	if err := p.Runtime.WriteEnv(ctx, inst, env); err != nil {
		return nil, "", err
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, dbName, nil
}

// AttachS3 is deliberately unimplemented rather than faked.
//
// It would need an S3-compatible endpoint plus the existing CreateS3Bucket
// operation and cloudtask/s3creds resolver. None of those can run in an
// environment with no cluster, and injecting well-formed AWS_* values that address
// nothing would put a fake door inside the product — which is precisely the
// failure mode this vertical was built to stop.
func (p *LocalAttachProvider) AttachS3(ctx context.Context, inst *Instance, bucket, envPrefix string) ([]string, error) {
	return nil, fmt.Errorf("box: S3 attach is not implemented by LocalAttachProvider: it needs an S3-compatible endpoint and the existing CreateS3Bucket operation plus cloudtask.S3CredentialsResolver, neither of which exists off-cluster")
}

func sanitizeIdent(in string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(in) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 48 {
		out = out[:48]
	}
	return strings.Trim(out, "_")
}

func escapeLiteral(s string) string { return strings.ReplaceAll(s, "'", "''") }

func randomPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
