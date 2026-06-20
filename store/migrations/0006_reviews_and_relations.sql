CREATE TABLE reviews (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_slug  TEXT    NOT NULL,
    round       INTEGER NOT NULL DEFAULT 1,
    reviewer    TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL,
    has_commits INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME
);

CREATE TABLE issue_relations (
    parent_issue_slug TEXT NOT NULL,
    child_issue_slug  TEXT NOT NULL,
    PRIMARY KEY (parent_issue_slug, child_issue_slug)
);
