package agentruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type fileDomainProvider struct {
	basePath string
}

func NewFileDomainProvider(basePath string) DomainProvider {
	return &fileDomainProvider{basePath: basePath}
}

func (p *fileDomainProvider) GetDomain(ctx context.Context, agentName, domain string) (string, error) {
	safeDomain := strings.ReplaceAll(domain, "..", "")
	safeDomain = strings.ReplaceAll(safeDomain, "/", "")

	path := filepath.Join(p.basePath, "agents", agentName, "domains", safeDomain+".md")

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("domain not found: %s", domain)
		}
		return "", fmt.Errorf("read domain file: %w", err)
	}

	return string(content), nil
}
