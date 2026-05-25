-- Destructive: existing branch tracking data is dropped. The branches
-- table is recreated with `name` as the primary key (the old uuid
-- column is gone). Legacy 4-part branch names still parse, so the
-- operator can re-`git zf issue start` to recreate equivalent rows.
DROP TABLE branches;

CREATE TABLE branches (
    name       TEXT PRIMARY KEY,
    issue_id   INTEGER NOT NULL REFERENCES issues(id),
    type       TEXT NOT NULL,
    status_id  INTEGER NOT NULL DEFAULT 1 REFERENCES statuses(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    merged_at  DATETIME
);

CREATE TRIGGER enforce_merged_at
BEFORE UPDATE OF status_id ON branches
WHEN NEW.status_id = 2
BEGIN
    SELECT CASE WHEN NEW.merged_at IS NULL
        THEN RAISE(ABORT, 'merged_at must not be null when status is merged')
    END;
END;
