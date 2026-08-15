package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// archiveChildRef is one table that still points at the rows a run wants to
// delete, and the foreign key it points with.
// ParentColumn is the referenced column on the archived table; it is what lets
// a candidate be re-counted against the rows a run would actually delete rather
// than against the child's whole population. Estimated marks a count that came
// from the catalog because the exact one did not finish in time, so a refusal
// carrying it is a precaution rather than a measurement.
type archiveChildRef struct {
	Table        string `json:"table"`
	Column       string `json:"column"`
	Rows         int64  `json:"rows"`
	Cascade      bool   `json:"cascade"`
	ParentColumn string `json:"-"`
	Estimated    bool   `json:"estimated,omitempty"`
	Unknown      bool   `json:"unknown,omitempty"`
}

// archiveProbeBudget is how long the two child questions are allowed to take.
// It is a parameter and not a constant because the same question is asked in
// two places that can afford very different answers: an HTTP handler with a
// human waiting, and the worker with a whole tick before anything is exported.
type archiveProbeBudget struct {
	Probe time.Duration
	Count time.Duration
}

// archiveBlockingChildren lists the referencing tables that stand between a run
// and its delete phase.
//
// A parent table cannot be archived while a child still references its rows:
// the export and the verify both succeed, and then every delete batch fails on
// the foreign key -- the run dies after writing an archive nobody asked for.
// The one real case this was found on lost twelve minutes and a 4 GB Parquet
// object to that ordering before anything said the word "children".
//
// A cascade key blocks too, and for a worse reason: the delete would succeed
// and take the child's rows with it, and those rows were never exported to any
// archive. Only children that actually hold rows are reported, so a table with
// an empty audit child is not declared unarchivable over nothing.
//
// The counts here are the child's whole population, which answers "could this
// key ever block" and not "does it block this run". Callers that know the
// cutoff must narrow the list with archiveChildrenInWindow before refusing.
func archiveBlockingChildren(ctx context.Context, conn *pgx.Conn, schema, table string) ([]archiveChildRef, error) {
	rows, err := conn.Query(ctx, `
		SELECT ch.relname, a.attname, af.attname,
		       GREATEST(ch.reltuples, 0)::bigint,
		       con.confdeltype = 'c'
		  FROM pg_constraint con
		  JOIN pg_class ch ON ch.oid = con.conrelid
		  JOIN pg_class pa ON pa.oid = con.confrelid
		  JOIN pg_namespace pn ON pn.oid = pa.relnamespace
		  JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = con.conkey[1]
		  JOIN pg_attribute af ON af.attrelid = con.confrelid AND af.attnum = con.confkey[1]
		 WHERE con.contype = 'f'
		   AND pn.nspname = $1 AND pa.relname = $2
		   AND ch.oid <> pa.oid
		 ORDER BY ch.relname, a.attname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []archiveChildRef{}
	for rows.Next() {
		var ref archiveChildRef
		if err := rows.Scan(&ref.Table, &ref.Column, &ref.ParentColumn, &ref.Rows, &ref.Cascade); err != nil {
			return nil, err
		}
		if ref.Rows <= 0 {
			continue
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// archiveChildrenInWindow keeps only the children that hold rows pointing at
// what this run would delete, counting them instead of guessing.
//
// The catalog count answers a different question than the run asks. A child
// keeps referencing the parent's recent rows forever, so a table that has been
// archived down to nothing older than the cutoff still reports millions of
// rows, and the run is refused over rows it would never touch. On the first
// customer database to fill a shard this was not a corner case: after the
// oldest rows were archived away, two of the three keys blocking a 5 GB table
// held exactly zero rows in the window, and a key holding three million rows
// blocked a 1 GB table it could no longer collide with.
//
// The decision and the number are two different questions, and only the first
// one has to be exact. Whether the key blocks is asked with EXISTS, which the
// planner can answer from the first matching row: on the 5 GB table this fix
// was written for, that is 130 ms against 2.5 s for the count, and the count
// was the half that kept missing its deadline and marking every key Estimated.
//
// The count runs only for a key that already blocks, purely so the refusal can
// name a size. A count that does not finish in time leaves the catalog estimate
// in place, marked Estimated, which weakens the sentence and not the verdict.
//
// A probe that does not finish in time returns the key marked Unknown, which is
// neither a yes nor a no. EXISTS is fast only when the answer is yes: proving
// that a key does NOT block still costs a full scan, and on the customer table
// this was written for those scans measured 11 s and 41 s against a 4 s panel
// budget. Treating that silence as a refusal made the gate deny every table it
// could not measure in a hurry. Callers decide what silence means, and only the
// caller with time to spare -- the worker, before a single row is exported --
// is allowed to turn it into a verdict.
func archiveChildrenInWindow(ctx context.Context, conn *pgx.Conn, r archiveRun, refs []archiveChildRef, budget archiveProbeBudget) []archiveChildRef {
	out := []archiveChildRef{}
	for _, ref := range refs {
		blocks, err := archiveChildBlocksWindow(ctx, conn, r, ref, budget.Probe)
		if err == nil && !blocks {
			continue
		}
		if err != nil {
			ref.Estimated = true
			ref.Unknown = true
			out = append(out, ref)
			continue
		}
		if exact, err := archiveChildRowsInWindow(ctx, conn, r, ref, budget.Count); err == nil {
			ref.Rows = exact
		} else {
			ref.Estimated = true
		}
		out = append(out, ref)
	}
	return out
}

// archiveChildrenDecided keeps only the keys that were measured to block.
//
// It is what the HTTP handlers use, so that only a measured block refuses a
// request. A key the probe could not answer inside the request budget is left
// to the worker, which asks the same question with minutes instead of seconds
// and stops the run before its first export if the answer turns out to be yes.
// Refusing on silence in a handler denied every table whose foreign keys are
// too large to prove clean while a human waits, which is exactly the kind of
// table an archive exists for.
func archiveChildrenDecided(refs []archiveChildRef) []archiveChildRef {
	out := []archiveChildRef{}
	for _, ref := range refs {
		if !ref.Unknown {
			out = append(out, ref)
		}
	}
	return out
}

// archiveChildrenUnknown keeps only the keys the probe could not answer in the
// budget it was given.
func archiveChildrenUnknown(refs []archiveChildRef) []archiveChildRef {
	out := []archiveChildRef{}
	for _, ref := range refs {
		if ref.Unknown {
			out = append(out, ref)
		}
	}
	return out
}

// archiveChildBlocksWindow answers whether any child row points at a row this
// run would delete. It carries the same predicate as the count and the delete,
// and stops at the first hit.
func archiveChildBlocksWindow(ctx context.Context, conn *pgx.Conn, r archiveRun, ref archiveChildRef, budget time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	query := fmt.Sprintf(
		`SELECT EXISTS (SELECT 1 FROM %s c JOIN %s tgt ON tgt.%s = c.%s WHERE %s)`,
		pgx.Identifier{r.Schema, ref.Table}.Sanitize(),
		pgx.Identifier{r.Schema, r.Table}.Sanitize(),
		pgx.Identifier{ref.ParentColumn}.Sanitize(),
		pgx.Identifier{ref.Column}.Sanitize(),
		archiveWhereSQL(r, "tgt"))

	var blocks bool
	if err := conn.QueryRow(ctx, query, r.Cutoff).Scan(&blocks); err != nil {
		return false, err
	}
	return blocks, nil
}

// archiveChildRowsInWindow counts the child rows whose parent this run would
// delete. The predicate is archiveWhereSQL so that the count and the delete
// cannot disagree about which rows the archive covers.
func archiveChildRowsInWindow(ctx context.Context, conn *pgx.Conn, r archiveRun, ref archiveChildRef, budget time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	query := fmt.Sprintf(
		`SELECT count(*) FROM %s c JOIN %s tgt ON tgt.%s = c.%s WHERE %s`,
		pgx.Identifier{r.Schema, ref.Table}.Sanitize(),
		pgx.Identifier{r.Schema, r.Table}.Sanitize(),
		pgx.Identifier{ref.ParentColumn}.Sanitize(),
		pgx.Identifier{ref.Column}.Sanitize(),
		archiveWhereSQL(r, "tgt"))

	var n int64
	if err := conn.QueryRow(ctx, query, r.Cutoff).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// archiveChildrenVerdict is the authoritative answer to whether a run may
// proceed, and the only place an unanswered foreign key is allowed to stop one.
//
// It runs in the worker, in the phase before the archive bucket is touched, so
// the whole question is asked with minutes of budget instead of the seconds an
// HTTP handler can spare. That matters because EXISTS is fast only when it
// finds a row: proving that a key holds nothing in the window is a full scan,
// and the customer database this was built for needs 11 s and 41 s to prove it
// for two of its keys. Asking here costs a tick; asking too late costs a
// multi-gigabyte export that every delete batch then refuses.
//
// A key still unanswered at this budget blocks. Nothing has been exported yet,
// so the cost of being wrong in this direction is one failed run with a
// readable reason.
func archiveChildrenVerdict(ctx context.Context, conn *pgx.Conn, r archiveRun) error {
	candidates, err := archiveBlockingChildren(ctx, conn, r.Schema, r.Table)
	if err != nil {
		return fmt.Errorf("read foreign keys of %s.%s: %w", r.Schema, r.Table, err)
	}
	if len(candidates) == 0 {
		return nil
	}
	blocking := archiveChildrenInWindow(ctx, conn, r, candidates, dbArchiveWorkerProbeBudget)
	if len(blocking) == 0 {
		return nil
	}
	return errors.New(archiveChildrenReason(r.Schema, r.Table, blocking))
}

// archiveChildrenOnItsOwnConn asks the child questions on a connection nothing
// else will need afterwards.
//
// A probe that runs out of budget is cancelled mid-query, and pgx answers a
// cancelled query by closing the connection: every later statement on it fails
// with "conn closed". Sharing the caller's connection therefore turned one slow
// foreign key into a dead request -- in production the plan came back 503
// "cannot read the table's delete rules right now" for the two tables whose
// keys are slowest, which reads as a broken database rather than a slow key.
//
// Failing to get a connection leaves every candidate undecided, which is the
// same answer as a probe that ran out of time, and lands in the same place: the
// worker, before anything is exported.
func (h *Handler) archiveChildrenOnItsOwnConn(ctx context.Context, shard, datname string, r archiveRun, refs []archiveChildRef, budget archiveProbeBudget) []archiveChildRef {
	if len(refs) == 0 {
		return refs
	}
	conn, err := h.connectToTenantDB(ctx, shard, datname)
	if err != nil {
		out := make([]archiveChildRef, 0, len(refs))
		for _, ref := range refs {
			ref.Unknown = true
			ref.Estimated = true
			out = append(out, ref)
		}
		return out
	}
	defer conn.Close(context.Background())
	return archiveChildrenInWindow(ctx, conn, r, refs, budget)
}

// archiveChildrenVerdictOnItsOwnConn is the worker's verdict, asked on a
// connection of its own for the reason archiveChildrenOnItsOwnConn documents:
// the phase that follows still needs a live connection to count rows.
func (h *Handler) archiveChildrenVerdictOnItsOwnConn(ctx context.Context, shard, datname string, r archiveRun) error {
	conn, err := h.connectToTenantDB(ctx, shard, datname)
	if err != nil {
		return fmt.Errorf("connect to %s on %s: %w", datname, shard, err)
	}
	defer conn.Close(context.Background())
	return archiveChildrenVerdict(ctx, conn, r)
}

// archiveChildrenReason is the sentence the console shows instead of a run that
// would die in its delete phase. Pure.
func archiveChildrenReason(schema, table string, refs []archiveChildRef) string {
	if len(refs) == 0 {
		return ""
	}
	names := make([]string, 0, len(refs))
	cascade := false
	estimated := false
	unknown := false
	for _, ref := range refs {
		names = append(names, fmt.Sprintf("%s.%s (%s, %d rows)", schema, ref.Table, ref.Column, ref.Rows))
		cascade = cascade || ref.Cascade
		estimated = estimated || ref.Estimated
		unknown = unknown || ref.Unknown
	}
	msg := fmt.Sprintf(
		"%s.%s cannot be archived yet: %s still point at the rows this cutoff would delete, so every delete would be refused by the foreign key. Archive those tables first.",
		schema, table, strings.Join(names, ", "))
	if cascade {
		msg += " One of them deletes on cascade, which would remove rows no archive holds."
	}
	if estimated {
		msg += " At least one count is the table's total rather than a measurement, because counting the exact overlap took too long."
	}
	if unknown {
		msg += " At least one key could not be answered at all in the time it was given, and an unanswered foreign key is treated as blocking rather than exporting gigabytes that the delete would then refuse."
	}
	return msg
}
