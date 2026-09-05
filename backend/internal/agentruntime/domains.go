package agentruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type DomainCatalog interface {
	ListDomains(context.Context, string) ([]string, error)
}
type fileDomainProvider struct{ basePath string }

var domainName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

func NewFileDomainProvider(basePath string) DomainProvider { return &fileDomainProvider{basePath} }
func (p *fileDomainProvider) directory(agentName string) (string, error) {
	if !domainName.MatchString(agentName) {
		return "", fmt.Errorf("invalid agent name")
	}
	base, err := filepath.EvalSymlinks(p.basePath)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "agents", agentName, "domains")
	resolved, err := filepath.EvalSymlinks(dir)
	if os.IsNotExist(err) {
		return dir, nil
	}
	if err != nil || resolved != dir {
		return "", fmt.Errorf("skill directory must not cross symlink boundaries")
	}
	return dir, nil
}
func (p *fileDomainProvider) GetDomain(ctx context.Context, agentName, domain string) (string, error) {
	if !domainName.MatchString(domain) {
		return "", fmt.Errorf("invalid skill name")
	}
	dir, err := p.directory(agentName)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("skills unavailable")
	}
	path, err := filepath.EvalSymlinks(filepath.Join(dir, domain+".md"))
	if err != nil {
		return "", fmt.Errorf("skill not found")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("skill outside agent directory")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read skill: %w", err)
	}
	if len(content) == 0 || len(content) > MaxSkillContentBytes {
		return "", fmt.Errorf("skill size out of bounds")
	}
	return string(content), nil
}
func (p *fileDomainProvider) ListDomains(ctx context.Context, agentName string) ([]string, error) {
	dir, err := p.directory(agentName)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			n := strings.TrimSuffix(e.Name(), ".md")
			if domainName.MatchString(n) {
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}
