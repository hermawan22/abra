package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func (s *Store) ListEvidenceAnchorCandidates(ctx context.Context, scope string, limit int) ([]EvidenceAnchorCandidate, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.queryRunner().Query(ctx, `
		SELECT
		  c.id,
		  c.claim_text,
		  c.scope,
		  c.status,
		  COALESCE(c.source_url, ''),
		  COALESCE(c.source_type, ''),
		  COALESCE(d.id, ''),
		  COALESCE(d.title, ''),
		  COALESCE(ch.content, ''),
		  CASE
		    WHEN c.status = 'expired' OR (c.expires_at IS NOT NULL AND c.expires_at < now()) OR c.freshness_status = 'expired' OR d.freshness_status = 'expired' THEN 'expired'
		    WHEN c.freshness_status = 'stale' OR d.freshness_status = 'stale' THEN 'stale'
		    WHEN c.last_verified_at IS NULL THEN 'unknown'
		    WHEN c.last_verified_at < now() - interval '120 days' THEN 'stale'
		    ELSE 'fresh'
		  END AS freshness,
		  COALESCE(c.last_verified_at::text, ''),
		  c.updated_at::text
		FROM claims c
		LEFT JOIN documents d
		  ON d.scope = c.scope
		 AND COALESCE(d.source_url, '') = COALESCE(c.source_url, '')
		 AND d.status NOT IN ('deprecated', 'deleted')
		LEFT JOIN LATERAL (
		  SELECT content
		  FROM chunks
		  WHERE document_id = d.id
		  ORDER BY ts_rank_cd(search_vector, plainto_tsquery('simple', c.claim_text)) DESC, chunk_index ASC
		  LIMIT 1
		) ch ON true
		WHERE c.scope = $1
		  AND c.status IN ('verified', 'inferred')
		  AND COALESCE(c.source_url, '') <> ''
		  AND NOT EXISTS (
		    SELECT 1
		    FROM evidence e
		    WHERE e.claim_id = c.id
		      AND COALESCE(e.source_url, '') = COALESCE(c.source_url, '')
		      AND length(trim(COALESCE(e.quote, ''))) > 0
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM source_configs sc
		    WHERE sc.id = c.source_config_id
		      AND sc.status = 'deleted'
		  )
		ORDER BY c.updated_at DESC
		LIMIT $2
	`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvidenceAnchorCandidate{}
	for rows.Next() {
		var item EvidenceAnchorCandidate
		if err := rows.Scan(
			&item.ClaimID,
			&item.Claim,
			&item.Scope,
			&item.Status,
			&item.SourceURL,
			&item.SourceType,
			&item.DocumentID,
			&item.DocumentTitle,
			&item.DocumentChunk,
			&item.Freshness,
			&item.LastVerifiedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CountEvidenceAnchorCandidates(ctx context.Context, scope string) (int, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return 0, fmt.Errorf("scope is required")
	}
	var count int
	err := s.queryRunner().QueryRow(ctx, `
		SELECT COUNT(DISTINCT c.id)::int
		FROM claims c
		WHERE c.scope = $1
		  AND c.status IN ('verified', 'inferred')
		  AND COALESCE(c.source_url, '') <> ''
		  AND NOT EXISTS (
		    SELECT 1
		    FROM evidence e
		    WHERE e.claim_id = c.id
		      AND COALESCE(e.source_url, '') = COALESCE(c.source_url, '')
		      AND length(trim(COALESCE(e.quote, ''))) > 0
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM source_configs sc
		    WHERE sc.id = c.source_config_id
		      AND sc.status = 'deleted'
		  )
	`, scope).Scan(&count)
	return count, err
}

func (s *Store) EvidenceAnchorsForClaims(ctx context.Context, scope string, claimIDs []string) ([]EvidenceAnchorResult, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(claimIDs))
	for _, id := range claimIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Strings(ids)
	rows, err := s.queryRunner().Query(ctx, `
		SELECT
		  e.claim_id,
		  COALESCE(e.document_id, ''),
		  e.quote,
		  COALESCE(e.start_char, 0),
		  COALESCE(e.end_char, 0),
		  COALESCE(e.source_url, ''),
		  COALESCE(e.source_type, ''),
		  COALESCE(d.title, '')
		FROM evidence e
		JOIN claims c ON c.id = e.claim_id
		LEFT JOIN documents d ON d.id = e.document_id AND d.status NOT IN ('deprecated', 'deleted')
		WHERE c.scope = $1
		  AND e.claim_id = ANY($2::text[])
		  AND c.status IN ('verified', 'inferred')
		  AND COALESCE(e.source_url, '') = COALESCE(c.source_url, '')
		  AND length(trim(COALESCE(e.quote, ''))) > 0
		ORDER BY e.claim_id ASC, e.created_at DESC
	`, scope, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvidenceAnchorResult{}
	for rows.Next() {
		var item EvidenceAnchorResult
		if err := rows.Scan(&item.ClaimID, &item.DocumentID, &item.Quote, &item.StartChar, &item.EndChar, &item.SourceURL, &item.SourceType, &item.DocumentTitle); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DocumentsBySource(ctx context.Context, scope string, sourceURLs []string, limitPerSource int) ([]DocumentResult, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	seen := map[string]struct{}{}
	sources := make([]string, 0, len(sourceURLs))
	for _, source := range sourceURLs {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		sources = append(sources, source)
	}
	if len(sources) == 0 {
		return nil, nil
	}
	if len(sources) > 20 {
		sources = sources[:20]
	}
	if limitPerSource < 1 || limitPerSource > 10 {
		limitPerSource = 3
	}
	rows, err := s.queryRunner().Query(ctx, `
		WITH ranked_chunks AS (
		  SELECT
		    d.id,
		    d.title,
		    d.source_url,
		    ch.content,
		    ch.chunk_index,
		    row_number() OVER (
		      PARTITION BY d.source_url
		      ORDER BY ch.chunk_index ASC, ch.id ASC
		    ) AS source_rank
		  FROM documents d
		  JOIN chunks ch ON ch.document_id = d.id
		  LEFT JOIN source_configs sc ON sc.id = d.source_config_id
		  WHERE d.scope = $1
		    AND ch.scope = $1
		    AND d.status NOT IN ('deprecated', 'deleted')
		    AND COALESCE(sc.status, 'active') <> 'deleted'
		    AND d.source_url = ANY($2::text[])
		)
		SELECT
		  id,
		  title,
		  source_url,
		  string_agg(content, E'\n' ORDER BY chunk_index) AS content,
		  0.35::float8 AS rank_score
		FROM ranked_chunks
		WHERE source_rank <= $3
		GROUP BY id, title, source_url
		ORDER BY source_url ASC
	`, scope, sources, limitPerSource)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []DocumentResult
	for rows.Next() {
		var doc DocumentResult
		if err := rows.Scan(&doc.ID, &doc.Title, &doc.Source, &doc.Content, &doc.Rank); err != nil {
			return nil, err
		}
		doc.TextScore = doc.Rank
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}
