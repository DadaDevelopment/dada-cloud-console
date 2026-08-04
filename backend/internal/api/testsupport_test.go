package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/dada-tuda/console/backend/internal/dbtest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dropSeededProject removes a seeded test project together with the rows that
// would block its deletion. See dbtest.DropProject for why a bare
// `DELETE FROM projects` is not enough.
func dropSeededProject(pool *pgxpool.Pool, projectID uuid.UUID) {
	dbtest.DropProject(pool, projectID)
}

// dropSeededUser removes a seeded test actor together with the operations and
// audit rows it wrote, both of which reference users with NO ACTION.
func dropSeededUser(pool *pgxpool.Pool, userID uuid.UUID) {
	dbtest.DropUser(pool, userID)
}

// dropSeededAudit removes the audit rows a test's handler wrote about one
// resource.
//
// Rows whose project_id is NULL -- every system-actor audit, and every
// rejection recorded against a dangling reference -- survive dropSeededProject,
// so without this they accumulate in the shared cloud-console database run
// after run and show up in the admin audit viewer as production history.
func dropSeededAudit(pool *pgxpool.Pool, kind, name string) {
	_, _ = pool.Exec(context.Background(),
		`DELETE FROM audit_events WHERE resource_kind = $1 AND resource_name = $2`, kind, name)
}

// dropSeededAuditByMeta is dropSeededAudit for rows that carry no usable
// resource name, keyed instead on a metadata field the test controls -- the
// payment webhook audit, whose resource_name is the org id and is therefore
// empty for a payment no org claims.
func dropSeededAuditByMeta(pool *pgxpool.Pool, key, value string) {
	_, _ = pool.Exec(context.Background(),
		`DELETE FROM audit_events WHERE metadata->>$1 = $2`, key, value)
}

// agentChatUser mints the throwaway user_sub an agent-chat test writes under
// and removes every row that test left behind.
//
// The agent-chat tables key on user_sub, which is a bare TEXT column with no
// foreign key behind it, so the FK walk in dbtest cannot reach them: without
// this the transcript, the turn trace and the confirmation cards of every run
// stay in the shared cloud-console database and surface as somebody's chat
// history.
func agentChatUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	sub := uuid.NewString()
	t.Cleanup(func() {
		for _, table := range []string{
			"agent_chat_pending_actions",
			"agent_chat_messages",
			"agent_chat_turns",
			"agent_chat_context_resets",
		} {
			_, _ = pool.Exec(context.Background(),
				fmt.Sprintf(`DELETE FROM %s WHERE user_sub = $1`, table), sub)
		}
	})
	return sub
}

// dropSeededProjectsByName is dropSeededProject for tests that never learn the
// id because the project is created by the handler under test and identified
// only by its slug.
func dropSeededProjectsByName(pool *pgxpool.Pool, name string) {
	dbtest.DropProjectsByName(pool, name)
}
