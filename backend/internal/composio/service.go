package composio

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// EndUserHeader is the request header the agent replays onto its MCP calls to
// say which end user it is acting for.
//
// Lowercase because that is how the kagent runtime writes and matches
// allowedHeaders. A call without it is not served: the broker has no way to pick
// a session, and picking any session would hand one user another user's Gmail.
const EndUserHeader = "x-dada-end-user"

// AgentHeader names the agent, for the audit row only. Absent is fine.
const AgentHeader = "x-dada-agent"

// Service is the broker's decision layer: it owns the mapping from a DADA end
// user to a Composio session, and it is the only thing that ever sees the
// platform API key.
type Service struct {
	client       *Client
	store        *Store
	userIDPrefix string
}

// NewService returns a Service. userIDPrefix namespaces every Composio user id
// this platform creates, so one Composio project can host several environments
// without two of them colliding on the same end user.
func NewService(client *Client, store *Store, userIDPrefix string) *Service {
	if userIDPrefix == "" {
		userIDPrefix = "dada"
	}
	return &Service{client: client, store: store, userIDPrefix: userIDPrefix}
}

// ComposioUserID is the stable identifier connections are stored under.
//
// Derived from the project id and the platform's own end-user key, never from an
// email: Composio keys every connected account on this string, and a value that
// can change would orphan a user's authorizations.
func (s *Service) ComposioUserID(projectID uuid.UUID, endUserKey string) string {
	return fmt.Sprintf("%s:%s:%s", s.userIDPrefix, projectID.String(), endUserKey)
}

// ensureSession returns the end user's session, creating it on first use.
func (s *Service) ensureSession(ctx context.Context, projectID uuid.UUID, endUserKey string, toolkits []string) (SessionRow, error) {
	row, err := s.store.Session(ctx, projectID, endUserKey)
	if err == nil {
		return row, nil
	}
	if err != ErrNoSession {
		return SessionRow{}, err
	}
	userID := s.ComposioUserID(projectID, endUserKey)
	session, err := s.client.CreateSession(ctx, userID, toolkits)
	if err != nil {
		return SessionRow{}, err
	}
	row = SessionRow{
		ProjectID:  projectID,
		EndUserKey: endUserKey,
		UserID:     userID,
		SessionID:  session.ID,
		MCPURL:     session.MCPURL,
		Toolkits:   toolkits,
	}
	if err := s.store.SaveSession(ctx, row); err != nil {
		return SessionRow{}, err
	}
	return row, nil
}

// Connect returns the hosted authorization URL for one toolkit and one end user.
//
// This is the whole "one button" path: the console calls it, redirects the
// browser to the returned URL, and the user sees the provider's own consent
// screen. The platform never sees a credential, and the end user never learns
// that Composio exists beyond the hostname of that page.
func (s *Service) Connect(ctx context.Context, projectID uuid.UUID, endUserKey, toolkit, callbackURL string) (string, error) {
	toolkit = strings.ToLower(strings.TrimSpace(toolkit))
	if toolkit == "" {
		return "", fmt.Errorf("toolkit is required")
	}
	row, err := s.ensureSession(ctx, projectID, endUserKey, []string{toolkit})
	if err != nil {
		return "", err
	}
	if !contains(row.Toolkits, toolkit) {
		merged := append(append([]string{}, row.Toolkits...), toolkit)
		sort.Strings(merged)
		if err := s.client.SetSessionToolkits(ctx, row.SessionID, merged); err != nil {
			return "", err
		}
		if err := s.store.SetSessionToolkits(ctx, projectID, endUserKey, merged); err != nil {
			return "", err
		}
	}
	link, err := s.client.Authorize(ctx, row.SessionID, toolkit, callbackURL)
	if err != nil {
		return "", err
	}
	status := "INITIALIZING"
	if err := s.store.UpsertIntegration(ctx, projectID, endUserKey, toolkit, link.ConnectedAccountID, status); err != nil {
		return "", err
	}
	return link.RedirectURL, nil
}

// Sync refreshes what we believe about an end user's authorizations from
// Composio, which is authoritative: the user finishes the OAuth dance on a page
// we do not host, so the only honest status is the one upstream reports.
func (s *Service) Sync(ctx context.Context, projectID uuid.UUID, endUserKey string) ([]Integration, error) {
	row, err := s.store.Session(ctx, projectID, endUserKey)
	if err == ErrNoSession {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	accounts, err := s.client.ConnectedAccounts(ctx, row.UserID)
	if err != nil {
		return nil, err
	}
	for _, acc := range accounts {
		if err := s.store.UpsertIntegration(ctx, projectID, endUserKey, acc.Toolkit, acc.ID, acc.Status); err != nil {
			return nil, err
		}
	}
	return s.store.Integrations(ctx, projectID, endUserKey)
}

// Integrations returns the cached view, without calling Composio.
func (s *Service) Integrations(ctx context.Context, projectID uuid.UUID, endUserKey string) ([]Integration, error) {
	return s.store.Integrations(ctx, projectID, endUserKey)
}

// ActiveToolkits is what this end user has actually authorized and can use now.
//
// Only ACTIVE counts: a connected account exists from the moment a link is
// minted, so treating presence as permission would put tools in front of an
// agent that answer 401 and make the agent look broken instead of unauthorized.
func (s *Service) ActiveToolkits(ctx context.Context, projectID uuid.UUID, endUserKey string) ([]string, error) {
	items, err := s.store.Integrations(ctx, projectID, endUserKey)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, item := range items {
		if strings.EqualFold(item.Status, "ACTIVE") {
			out = append(out, item.Toolkit)
		}
	}
	sort.Strings(out)
	return out, nil
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
