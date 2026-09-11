CREATE TABLE cheatsheet_jobs (
    vacation_id TEXT NOT NULL REFERENCES vacations(id) ON DELETE CASCADE,
    source_language TEXT NOT NULL,
    destination_key TEXT NOT NULL,
    job_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'failed', 'ready', 'setup')),
    created_at TEXT NOT NULL,
    PRIMARY KEY (vacation_id, source_language, destination_key, job_key)
);
CREATE INDEX cheatsheet_jobs_pending ON cheatsheet_jobs(status, created_at);
