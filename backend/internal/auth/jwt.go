package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims represents the JWT claims payload.
//
// Authorization is decoded from NATIVE Keycloak RBAC claims (ADR-009 §2-4):
// Keycloak is the system of record and emits stock claims; dada-cloud only
// decodes them. There is no pre-shaped org_role/projects claim anymore.
//
//   - Groups carries the full-path group memberships from the Group Membership
//     mapper: "/orgs/{org}/{Role}", "/orgs/{org}/projects/{proj}/{Role}", and the
//     hidden "/platform-admins", "/platform-analysts" and "/agents" staff groups.
//   - Scope is the native space-delimited OIDC scope string.
//   - Roles is realm_access.roles (diagnostics only; authz comes from Groups).
//
// The decoded view (orgRoles/projectRoles/scopeSet/platformAdmin/platformAnalyst/
// agent) is built lazily from Groups+Scope on first access and is never
// serialized.
type Claims struct {
	UserID      uuid.UUID `json:"user_id"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	// Groups/Roles/Scope come straight from Keycloak in OIDC mode (Group
	// Membership mapper paths, realm/resource roles, native OIDC scope). In local
	// HS256 mode the dev-god token still synthesizes Groups+Scope so authz works.
	Groups []string `json:"groups,omitempty"`
	Roles  []string `json:"roles,omitempty"`
	Scope  string   `json:"scope,omitempty"`

	SessionID string `json:"sid,omitempty"`

	// decoded view (not serialized).
	decoded         bool
	orgRoles        map[string]string
	projectRoles    map[string]string
	scopeSet        map[string]struct{}
	platformAdmin   bool
	platformAnalyst bool
	agent           bool
	jwt.RegisteredClaims
}

// AllScopes is the full scope vocabulary (PRD-IAM). Local dev-god tokens carry
// the whole set; user-service mints a narrower set for real keys.
var AllScopes = []string{
	"read", "metrics:write", "logs:write", "deploy:write",
	"builds:read", "builds:write", "admin",
	"ai:chat", "ai:embeddings",
}

// roleRank ranks the four roles for decode-time max-merging when a single scope
// carries more than one role membership. Mirrors models.RolePriority but kept
// local so the auth package stays free of a models dependency. Unknown → 0.
func roleRank(role string) int {
	switch role {
	case "Owner":
		return 4
	case "Admin":
		return 3
	case "Developer":
		return 2
	case "ReadOnly":
		return 1
	}
	return 0
}

// decode parses Groups + Scope into the lookup maps. Idempotent and lazy: every
// accessor calls it, so Claims built by hand (tests, the keycloak resolver) decode
// without an explicit step.
func (c *Claims) decode() {
	if c.decoded {
		return
	}
	c.decoded = true
	c.orgRoles = map[string]string{}
	c.projectRoles = map[string]string{}
	c.scopeSet = map[string]struct{}{}

	for _, g := range c.Groups {
		switch g {
		case "/platform-admins":
			c.platformAdmin = true
			continue
		case "/platform-analysts":
			c.platformAnalyst = true
			continue
		case "/agents":
			c.agent = true
			continue
		}
		// "/orgs/{org}/{Role}"                      → org role
		// "/orgs/{org}/projects/{proj}/{Role}"      → project role
		parts := strings.Split(strings.Trim(g, "/"), "/")
		if len(parts) < 3 || parts[0] != "orgs" {
			continue
		}
		switch {
		case len(parts) == 3:
			org, role := parts[1], parts[2]
			if roleRank(role) > roleRank(c.orgRoles[org]) {
				c.orgRoles[org] = role
			}
		case len(parts) == 5 && parts[2] == "projects":
			proj, role := parts[3], parts[4]
			if roleRank(role) > roleRank(c.projectRoles[proj]) {
				c.projectRoles[proj] = role
			}
		}
	}

	for _, s := range strings.Fields(c.Scope) {
		c.scopeSet[s] = struct{}{}
	}

	// Default per-user org (ADR-009 follow-up): every authenticated user is
	// implicitly Owner of a personal org whose id equals their username. This is
	// NOT backed by a Keycloak group — registration/login is Keycloak-native and
	// we do not provision a group per user. Personal projects carry
	// org_id = <username>; the org-role cascade then makes them visible/ownable to
	// exactly that user (and /platform-admins). max-merge so an explicit Keycloak
	// /orgs/<username>/<Role> grant, if one ever exists, is never downgraded.
	//
	// Agents (/agents) are deliberately excluded: the whole point of the group is
	// that a non-human identity holds ONLY the project grants it was handed, with
	// no org of its own to fall back on. Without this carve-out an agent scoped to
	// one sandbox project would still be Owner of the org named after its
	// service-account username and could mint projects there without limit.
	if c.Username != "" && !c.agent && roleRank("Owner") > roleRank(c.orgRoles[c.Username]) {
		c.orgRoles[c.Username] = "Owner"
	}
}

// OrgRole returns the caller's role in org (empty if none).
func (c *Claims) OrgRole(orgID string) string {
	c.decode()
	return c.orgRoles[orgID]
}

// ProjectRole returns the caller's explicit role on project (empty if none). It
// does NOT apply the org-role cascade — callers that need the effective role
// combine this with OrgRole(projectOrg) once they know the project's org.
func (c *Claims) ProjectRole(projectID string) string {
	c.decode()
	return c.projectRoles[projectID]
}

// OrgRoles returns the decoded org→role map (read-only; do not mutate).
func (c *Claims) OrgRoles() map[string]string {
	c.decode()
	return c.orgRoles
}

// ProjectRoles returns the decoded project→role map (read-only; do not mutate).
func (c *Claims) ProjectRoles() map[string]string {
	c.decode()
	return c.projectRoles
}

// IsPlatformAdmin reports staff god-mode (the hidden /platform-admins group,
// ADR-009). Outside the customer role enum; never surfaced in UI.
func (c *Claims) IsPlatformAdmin() bool {
	c.decode()
	return c.platformAdmin
}

// IsPlatformAnalyst reports read-only staff access (the hidden
// /platform-analysts group): every project readable, every admin read endpoint
// readable, nothing writable. Exists so analytics identities (the autonomous
// routine, dashboards) stop needing /platform-admins just to count things.
// Outside the customer role enum; never surfaced in UI.
func (c *Claims) IsPlatformAnalyst() bool {
	c.decode()
	return c.platformAnalyst
}

// IsAgent reports that the caller is a non-human automation identity (the hidden
// /agents group). Agents are confined to the projects explicitly granted to them:
// they get no implicit personal org (see decode) and may not create projects at
// all. Outside the customer role enum; never surfaced in UI.
func (c *Claims) IsAgent() bool {
	c.decode()
	return c.agent
}

// HasScope reports whether the native scope set contains want.
func (c *Claims) HasScope(want string) bool {
	c.decode()
	_, ok := c.scopeSet[want]
	return ok
}

// GenerateToken creates a signed JWT for local (HS256) auth mode. There is no
// Keycloak locally to emit native claims, so the token is a dev-god: the
// /platform-admins staff group (Owner everywhere) plus the full native scope
// string. Keeps local dev/tests working while dada-cloud carries zero
// role-resolution logic (ADR-009).
func GenerateToken(userID uuid.UUID, username, email, displayName, secret string) (string, error) {
	claims := Claims{
		UserID:      userID,
		Username:    username,
		Email:       email,
		DisplayName: displayName,
		Groups:      []string{"/orgs/local-dev/Owner", "/platform-admins"},
		Scope:       strings.Join(AllScopes, " "),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   userID.String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}

	return signed, nil
}

// ValidateToken parses and validates a JWT token string, returning its claims.
func ValidateToken(tokenStr, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}
