package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDomainLoadsProjectedConfigMapSymlinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "test-agent", "domains")
	version := "..2026_09_05_15_30_00.123456789"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, version), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, version, "deposit_access.md"), []byte("Do not treat a reported deposit as verified."), 0600))
	require.NoError(t, os.Symlink(version, filepath.Join(dir, "..data")))
	require.NoError(t, os.Symlink(filepath.Join("..data", "deposit_access.md"), filepath.Join(dir, "deposit_access.md")))
	provider := NewFileDomainProvider(root)
	names, err := provider.(DomainCatalog).ListDomains(context.Background(), "test-agent")
	require.NoError(t, err)
	require.Equal(t, []string{"deposit_access"}, names)
	content, err := provider.GetDomain(context.Background(), "test-agent", "deposit_access")
	require.NoError(t, err)
	require.Equal(t, "Do not treat a reported deposit as verified.", content)
}

func TestDomainRejectsEscapingSymlinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "test-agent", "domains")
	require.NoError(t, os.MkdirAll(dir, 0700))
	outside := filepath.Join(root, "outside.md")
	require.NoError(t, os.WriteFile(outside, []byte("private outside content"), 0600))
	for name, target := range map[string]string{"outside": outside, "parent": ".."} {
		require.NoError(t, os.Symlink(target, filepath.Join(dir, name+".md")))
		_, err := NewFileDomainProvider(root).GetDomain(context.Background(), "test-agent", name)
		require.ErrorContains(t, err, "skill outside agent directory")
	}
}
