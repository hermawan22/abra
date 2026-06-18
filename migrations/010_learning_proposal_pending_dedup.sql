WITH ranked AS (
  SELECT
    id,
    row_number() OVER (
      PARTITION BY
        scope,
        proposal_type,
        title,
        COALESCE(target_type, ''),
        COALESCE(target_id, ''),
        COALESCE(source_url, '')
      ORDER BY created_at DESC, id DESC
    ) AS rn
  FROM learning_proposals
  WHERE status = 'pending'
)
UPDATE learning_proposals lp
SET
  status = 'canceled',
  review_reason = COALESCE(
    NULLIF(review_reason, ''),
    'Canceled by migration: duplicate pending learning proposal superseded by a newer matching proposal.'
  ),
  payload = payload || jsonb_build_object(
    'dedup_migration', '010_learning_proposal_pending_dedup',
    'dedup_canceled_at', now()::text
  ),
  reviewed_at = COALESCE(reviewed_at, now()),
  updated_at = now()
FROM ranked
WHERE lp.id = ranked.id
  AND ranked.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS learning_proposals_pending_dedup_idx
  ON learning_proposals (
    scope,
    proposal_type,
    title,
    COALESCE(target_type, ''),
    COALESCE(target_id, ''),
    COALESCE(source_url, '')
  )
  WHERE status = 'pending';
