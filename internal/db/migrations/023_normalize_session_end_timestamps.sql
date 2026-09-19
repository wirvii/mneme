UPDATE sessions
SET ended_at = replace(ended_at, ' ', 'T') || 'Z'
WHERE ended_at IS NOT NULL
  AND instr(ended_at, 'T') = 0;

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (23, datetime('now'));
