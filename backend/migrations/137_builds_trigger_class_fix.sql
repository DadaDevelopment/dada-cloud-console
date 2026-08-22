-- The class-fix sweeper (build_classfix_sweeper.go) re-queues a build whose
-- failure class a platform-side fix has since closed. It used to insert that
-- row with trigger='manual', which reads as the user pressing the rebuild
-- button -- they never did. builds_trigger_check has to widen before the
-- sweeper can write the honest value.
--
-- Widening a CHECK is safe across a rolling update: old and new replicas both
-- write the values they always wrote, and only the new replica additionally
-- writes 'class_fix'. No existing row's trigger value narrows or disappears.
ALTER TABLE builds DROP CONSTRAINT builds_trigger_check;
ALTER TABLE builds ADD CONSTRAINT builds_trigger_check
    CHECK (trigger IN ('push', 'pr', 'manual', 'rollback', 'class_fix'));

-- Backfill: the sweeper's one live firing (sess-0822e, audit_events action
-- BuildAutoRetried, metadata->>'class_fix_id'='static-npm-template-20260821')
-- wrote its build row before this migration existed, so it is still sitting
-- there tagged trigger='manual'. Correct it from the audit trail the sweeper
-- itself wrote, rather than leaving the one instance that motivated this
-- migration reading as the very thing it was meant to stop being read as.
UPDATE builds b
SET    trigger = 'class_fix'
FROM   audit_events a
WHERE  a.action = 'BuildAutoRetried'
  AND  b.id = (a.metadata->>'build_id')::uuid
  AND  b.trigger = 'manual';
