ALTER TABLE memory_summaries
  DROP CONSTRAINT IF EXISTS memory_summaries_level_check;

ALTER TABLE memory_summaries
  ADD CONSTRAINT memory_summaries_level_check
  CHECK (level IN ('source', 'repo', 'module', 'file', 'route', 'component', 'symbol', 'package', 'decision'));
