ALTER TABLE agent_task_queue
    DROP COLUMN IF EXISTS superseded_comment_ids,
    DROP COLUMN IF EXISTS superseded_by_task_id,
    DROP COLUMN IF EXISTS supersession_kind;
