-- A CRM pause the endpoint refuses for good (422 unmappable external id, 404
-- person never linked) gets the terminal status 'rejected', so the runtime's
-- reconciler stops retrying it every 30 seconds.
ALTER TABLE conversation_runtime_state DROP CONSTRAINT IF EXISTS conversation_runtime_state_crm_status_sync_check;
ALTER TABLE conversation_runtime_state ADD CONSTRAINT conversation_runtime_state_crm_status_sync_check
    CHECK (crm_status_sync IN ('', 'pending', 'completed', 'failed', 'rejected'));
