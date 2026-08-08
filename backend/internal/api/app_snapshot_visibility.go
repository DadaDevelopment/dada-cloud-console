package api

// notOrphanedSnapshot is the shared SQL guard that separates a real app from
// orphan-GC's soft delete. gitops-agent stamps phase='Orphaned' on an App
// snapshot that has neither a live pod nor an app.yaml in git (see
// gitops-agent/internal/worker/orphangc.go gcDecide) and keeps the row until
// OrphanPurgeAfter so the deletion stays reversible; a snapshot whose app comes
// back is un-marked on the next sweep. The row therefore describes something
// that provably does not run, and every reader answering "what does this
// org/environment have" must skip it.
//
// It exists as one constant because the same soft-deleted row leaked into
// several answers at once: the console listed it as a working app (a deleted
// app reappeared and users deleted it a second time, 2026-08-08), quota
// counting treated it as one of the org's apps, and consumption billed it.
// The predicate assumes the resource_snapshots table is aliased rs.
const notOrphanedSnapshot = `rs.phase <> 'Orphaned'`
