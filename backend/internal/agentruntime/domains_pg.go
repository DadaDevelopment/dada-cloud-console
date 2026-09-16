package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

// SkillRowQuerier is the slice of pgxpool.Pool the database-backed skills
// provider needs; tests hand it a fake.
type SkillRowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type pgDomainProvider struct {
	db       SkillRowQuerier
	fallback DomainProvider
}

// NewPGDomainProvider serves skills synced from the agent's git repository
// (agent_prompt_sources.skills) and falls back to the mounted files for agents
// without a synced source, so the values-file skills keep working until each
// agent is moved over.
func NewPGDomainProvider(db SkillRowQuerier, fallback DomainProvider) DomainProvider {
	return &pgDomainProvider{db: db, fallback: fallback}
}

const syncedSkillsSQL = `SELECT skills FROM agent_prompt_sources
	WHERE agent_name = $1 AND synced_at IS NOT NULL
	ORDER BY synced_at DESC
	LIMIT 1`

func (p *pgDomainProvider) synced(ctx context.Context, agentName string) (map[string]string, bool, error) {
	if !domainName.MatchString(agentName) {
		return nil, false, fmt.Errorf("invalid agent name")
	}
	var skills map[string]string
	err := p.db.QueryRow(ctx, syncedSkillsSQL, agentName).Scan(&skills)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("skills unavailable")
	}
	return skills, true, nil
}

func (p *pgDomainProvider) GetDomain(ctx context.Context, agentName, domain string) (string, error) {
	if !domainName.MatchString(domain) {
		return "", fmt.Errorf("invalid skill name")
	}
	skills, ok, err := p.synced(ctx, agentName)
	if err != nil {
		return "", err
	}
	if !ok {
		return p.fallback.GetDomain(ctx, agentName, domain)
	}
	content, found := skills[domain]
	if !found {
		return "", fmt.Errorf("skill not found")
	}
	if len(content) == 0 || len(content) > MaxSkillContentBytes {
		return "", fmt.Errorf("skill size out of bounds")
	}
	return content, nil
}

func (p *pgDomainProvider) ListDomains(ctx context.Context, agentName string) ([]string, error) {
	skills, ok, err := p.synced(ctx, agentName)
	if err != nil {
		return nil, err
	}
	if !ok {
		if catalog, isCatalog := p.fallback.(DomainCatalog); isCatalog {
			return catalog.ListDomains(ctx, agentName)
		}
		return []string{}, nil
	}
	names := make([]string, 0, len(skills))
	for name := range skills {
		if domainName.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}
