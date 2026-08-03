package api

import (
	"context"
	"fmt"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/jackc/pgx/v5/pgxpool"
)

// boxLiveObjects reads what the database still claims in the box namespace.
//
// Both queries have to succeed or the sweep they feed must not run.
// box.LiveObjects.Complete is only set at the end, because a partial answer and a
// genuinely empty namespace look identical from the sweep's side, and the sweep's
// reaction to "nothing is claimed" is to delete everything. A postgres blip is a
// normal event on this platform — the shared instance filled its WAL disk on
// 2026-08-03 and took every console read down with it — so this is a failure mode
// that WILL be exercised rather than one that might be.
//
// An exposure's Service and Ingress are keyed to the BOX, not to the row in
// box_exposures, and that is deliberate. Keying them to live exposures would
// collect one more class of leak — an Unexpose whose row was withdrawn but whose
// objects survived the delete — at the price of making a published hostname's
// lifetime depend on two writes agreeing. The Ingress is what serves a customer's
// port; a sweep that can take it down while their box is up and their row merely
// disagrees is a worse failure than a leaked Service, which costs nothing but a
// name. So the box's own tombstone is the trigger: while the box exists its
// published objects are its business, and when it is gone they are garbage.
//
// A box is claimed while its row is anything other than the tombstone. That
// includes Failed and Deleting: a Failed box's objects are what an operator reads
// to find out why it failed, and a Deleting box is having its objects removed by
// the delete operation right now, which does not need a second deleter racing it.
// Both become sweepable the moment the row reaches Deleted, which is the point at
// which the product itself stops offering any way to reach them.
//
// A crystallized artifact is claimed while its promotion is not a failure. Failed
// and RolledBack are the two endings that leave no artifact anybody owns: the
// promotion either never finished or was undone, and whatever survived in the
// namespace is debris of a VM that does not exist. Verified and Running are the
// customer's permanent artifact and the one being built right now.
func boxLiveObjects(ctx context.Context, pool *pgxpool.Pool) (box.LiveObjects, error) {
	live := box.LiveObjects{
		InstanceRefs: map[string]struct{}{},
		BoxNames:     map[string]struct{}{},
		Crystals:     map[string]struct{}{},
	}

	rows, err := pool.Query(ctx, `
		SELECT instance_ref, name FROM boxes WHERE status <> 'Deleted'`)
	if err != nil {
		return live, fmt.Errorf("read live boxes: %w", err)
	}
	for rows.Next() {
		var ref, name string
		if err := rows.Scan(&ref, &name); err != nil {
			rows.Close()
			return live, fmt.Errorf("scan a live box: %w", err)
		}
		if ref != "" {
			live.InstanceRefs[ref] = struct{}{}
		}
		if name != "" {
			live.BoxNames[name] = struct{}{}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return live, fmt.Errorf("read live boxes: %w", err)
	}

	crystals, err := pool.Query(ctx, `
		SELECT DISTINCT vm_name FROM box_crystallizations
		 WHERE status NOT IN ('Failed', 'RolledBack')`)
	if err != nil {
		return live, fmt.Errorf("read live crystallizations: %w", err)
	}
	for crystals.Next() {
		var vm string
		if err := crystals.Scan(&vm); err != nil {
			crystals.Close()
			return live, fmt.Errorf("scan a live crystallization: %w", err)
		}
		if vm != "" {
			live.Crystals[vm] = struct{}{}
		}
	}
	crystals.Close()
	if err := crystals.Err(); err != nil {
		return live, fmt.Errorf("read live crystallizations: %w", err)
	}

	live.Complete = true
	return live, nil
}
