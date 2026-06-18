UPDATE claims c
SET
  status = 'deprecated',
  metadata = c.metadata || jsonb_build_object(
    'deprecated_by_migration', '011_deprecate_code_document_claims',
    'deprecated_reason', 'Code documents must not produce trusted natural-language claims.',
    'deprecated_at', now()::text
  ),
  updated_at = now()
FROM documents d
WHERE c.scope = d.scope
  AND COALESCE(c.source_url, '') = COALESCE(d.source_url, '')
  AND d.metadata->>'content_kind' = 'code'
  AND c.status NOT IN ('deprecated', 'expired');
