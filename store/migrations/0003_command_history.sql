CREATE TABLE command_history (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    command    TEXT     NOT NULL,
    payload    TEXT     NOT NULL CHECK (json_valid(payload)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX command_history_command_created
    ON command_history (command, created_at DESC);
