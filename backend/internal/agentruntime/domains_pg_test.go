package agentruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

type fakeSkillRow struct {
	skills map[string]string
	err    error
}

func (r fakeSkillRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*map[string]string)) = r.skills
	return nil
}

type fakeSkillDB struct {
	rows map[string]fakeSkillRow
}

func (f fakeSkillDB) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	row, ok := f.rows[args[0].(string)]
	if !ok {
		return fakeSkillRow{err: pgx.ErrNoRows}
	}
	return row
}

func TestPGDomainProviderServesSyncedSkills(t *testing.T) {
	db := fakeSkillDB{rows: map[string]fakeSkillRow{
		"synced": {skills: map[string]string{"withdrawal": "# W\n", "deposit": "# D\n", "": "bad"}},
		"broken": {skills: map[string]string{"huge": strings.Repeat("x", MaxSkillContentBytes+1)}},
		"legacy": {err: errors.New("connection refused")},
	}}
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "legacy", "domains")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "files.md"), []byte("from disk"), 0600))
	p := NewPGDomainProvider(db, NewFileDomainProvider(root))

	content, err := p.GetDomain(context.Background(), "synced", "deposit")
	require.NoError(t, err)
	require.Equal(t, "# D\n", content)
	names, err := p.(DomainCatalog).ListDomains(context.Background(), "synced")
	require.NoError(t, err)
	require.Equal(t, []string{"deposit", "withdrawal"}, names)

	_, err = p.GetDomain(context.Background(), "synced", "missing")
	require.ErrorContains(t, err, "skill not found")
	_, err = p.GetDomain(context.Background(), "broken", "huge")
	require.ErrorContains(t, err, "out of bounds")
	_, err = p.GetDomain(context.Background(), "synced", "../etc")
	require.ErrorContains(t, err, "invalid skill name")

	content, err = p.GetDomain(context.Background(), "legacy", "files")
	require.NoError(t, err)
	require.Equal(t, "from disk", content)
	names, err = p.(DomainCatalog).ListDomains(context.Background(), "legacy")
	require.NoError(t, err)
	require.Equal(t, []string{"files"}, names)
}
