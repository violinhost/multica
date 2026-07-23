ALTER TABLE agent_task_queue
    ADD COLUMN IF NOT EXISTS supersession_kind TEXT
        CHECK (supersession_kind IN ('direct_delivery', 'trusted_receipt')),
    ADD COLUMN IF NOT EXISTS superseded_by_task_id UUID,
    ADD COLUMN IF NOT EXISTS superseded_comment_ids UUID[] NOT NULL DEFAULT '{}';
