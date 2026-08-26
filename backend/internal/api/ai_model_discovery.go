package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	aiModelDiscoveryConcurrency   = 4
	aiModelDiscoveryMaxBody       = 2 << 20
	aiModelDiscoveryMaxPages      = 20
	aiModelDiscoveryMaxModels     = 2000
	aiModelDiscoveryMaxModelIDLen = 256
	aiModelDiscoveryBatchTimeout  = 12 * time.Second
)

var aiModelDiscoveryClient = &http.Client{Timeout: 8 * time.Second}
var aiModelLookupIP = net.DefaultResolver.LookupIPAddr
var aiModelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

var aiProviderDefaultBases = map[string]string{
	"openai":     "https://api.openai.com",
	"anthropic":  "https://api.anthropic.com",
	"openrouter": "https://openrouter.ai/api",
	"groq":       "https://api.groq.com/openai",
	"sambanova":  "https://api.sambanova.ai",
	"sotamodel":  "https://www.sotamodel.net",
	"nvidia_nim": "https://integrate.api.nvidia.com",
}

func modelDiscoveryURL(apiBase string) (string, error) {
	base, err := url.Parse(strings.TrimRight(apiBase, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("invalid api_base")
	}
	switch {
	case strings.HasSuffix(base.Path, "/models"):
	case strings.HasSuffix(base.Path, "/v1"):
		base.Path += "/models"
	default:
		base.Path += "/v1/models"
	}
	return base.String(), nil
}

func unsafeDiscoveryIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

func validateAIAPIBase(ctx context.Context, apiBase string) error {
	u, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("api_base must be an https URL without userinfo or fragment")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if unsafeDiscoveryIP(ip) {
			return errors.New("api_base resolves to a non-public address")
		}
		return nil
	}
	addrs, err := aiModelLookupIP(ctx, u.Hostname())
	if err != nil || len(addrs) == 0 {
		return errors.New("api_base hostname cannot be resolved")
	}
	for _, addr := range addrs {
		if unsafeDiscoveryIP(addr.IP) {
			return errors.New("api_base resolves to a non-public address")
		}
	}
	return nil
}

func validateDiscoveredModelID(id string) error {
	if len(id) == 0 || len(id) > aiModelDiscoveryMaxModelIDLen || !aiModelIDPattern.MatchString(id) {
		return errors.New("invalid discovered model id")
	}
	return nil
}

type upstreamModelsPage struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// discoverUpstreamModels supports the OpenAI-compatible data/models shape and
// Anthropic's same data shape with after_id pagination. It never includes a
// response body or credential in returned errors.
func discoverUpstreamModels(ctx context.Context, client *http.Client, provider, apiBase, apiKey string) ([]string, error) {
	if strings.TrimSpace(apiBase) == "" {
		apiBase = aiProviderDefaultBases[provider]
	}
	if err := validateAIAPIBase(ctx, apiBase); err != nil {
		return nil, err
	}
	endpoint, err := modelDiscoveryURL(apiBase)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	models := []string{}
	afterID := ""
	for pageNo := 0; pageNo < aiModelDiscoveryMaxPages; pageNo++ {
		u, _ := url.Parse(endpoint)
		if provider == "anthropic" && afterID != "" {
			q := u.Query()
			q.Set("after_id", afterID)
			u.RawQuery = q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, errors.New("build model discovery request")
		}
		if provider == "anthropic" {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		safeClient := *client
		safeClient.CheckRedirect = func(*http.Request, []*http.Request) error {
			return errors.New("model discovery redirects are disabled")
		}
		resp, err := safeClient.Do(req)
		if err != nil {
			return nil, errors.New("model discovery request failed")
		}
		var page upstreamModelsPage
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			err = json.NewDecoder(io.LimitReader(resp.Body, aiModelDiscoveryMaxBody)).Decode(&page)
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("model discovery returned status %d", resp.StatusCode)
		}
		if err != nil {
			return nil, errors.New("invalid model discovery response")
		}
		for _, item := range page.Data {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			if err := validateDiscoveredModelID(id); err != nil {
				// Upstream catalogues are untrusted and may contain display names
				// that are not legal wire model ids. Drop only that row; the
				// authenticated report endpoint remains strict for callers.
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			models = append(models, id)
			if len(models) > aiModelDiscoveryMaxModels {
				return nil, errors.New("model discovery result exceeds limit")
			}
		}
		if provider != "anthropic" || !page.HasMore {
			return models, nil
		}
		if page.LastID == "" || page.LastID == afterID {
			return nil, errors.New("invalid model discovery pagination")
		}
		afterID = page.LastID
	}
	return nil, errors.New("model discovery pagination limit exceeded")
}

func normalizeDiscoveredModels(raw []string) ([]string, error) {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		model := strings.TrimSpace(value)
		if model == "" {
			continue
		}
		if err := validateDiscoveredModelID(model); err != nil {
			return nil, err
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
		if len(out) > aiModelDiscoveryMaxModels {
			return nil, errors.New("model discovery result exceeds limit")
		}
	}
	return out, nil
}

func replaceCredentialModels(ctx context.Context, pool *pgxpool.Pool, credentialID uuid.UUID, models []string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var provider string
	if err := tx.QueryRow(ctx, `SELECT c.provider FROM ai_gateway_key_credentials c
		LEFT JOIN ai_gateway_keys k ON k.id = c.gateway_key_id
		WHERE c.id = $1 AND c.deleted_at IS NULL
		  AND (c.gateway_key_id IS NULL OR k.revoked_at IS NULL)`, credentialID).Scan(&provider); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("credential not found")
		}
		return err
	}
	models, err = normalizeDiscoveredModels(append(models, aiAliasesForProvider(provider)...))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ai_gateway_key_credential_models WHERE credential_id = $1`, credentialID); err != nil {
		return err
	}
	for _, model := range models {
		if _, err := tx.Exec(ctx, `INSERT INTO ai_gateway_key_credential_models (credential_id, model_id) VALUES ($1, $2)`, credentialID, model); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_gateway_key_credentials SET status='healthy',updated_at=now() WHERE id=$1`, credentialID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type createdCredentialDiscovery struct {
	ID       uuid.UUID
	Provider string
	APIBase  string
	APIKey   string
}

func discoverCreatedCredentials(ctx context.Context, pool *pgxpool.Pool, credentials []createdCredentialDiscovery) {
	ctx, cancel := context.WithTimeout(ctx, aiModelDiscoveryBatchTimeout)
	defer cancel()
	sem := make(chan struct{}, aiModelDiscoveryConcurrency)
	var wg sync.WaitGroup
	for _, credential := range credentials {
		credential := credential
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			models, err := discoverUpstreamModels(ctx, aiModelDiscoveryClient, credential.Provider, credential.APIBase, credential.APIKey)
			if err != nil {
				return
			}
			_ = replaceCredentialModels(ctx, pool, credential.ID, models)
		}()
	}
	wg.Wait()
}

// refreshCredentialDiscovery removes the snapshot made with an old secret or
// base URL before probing the replacement. A failed probe therefore exposes no
// stale models after re-enable; the credential itself remains saved.
func refreshCredentialDiscovery(ctx context.Context, pool *pgxpool.Pool, credential createdCredentialDiscovery) {
	ctx, cancel := context.WithTimeout(ctx, aiModelDiscoveryBatchTimeout)
	defer cancel()
	if err := replaceCredentialModels(ctx, pool, credential.ID, nil); err != nil {
		return
	}
	models, err := discoverUpstreamModels(ctx, aiModelDiscoveryClient, credential.Provider, credential.APIBase, credential.APIKey)
	if err != nil {
		return
	}
	_ = replaceCredentialModels(ctx, pool, credential.ID, models)
}
