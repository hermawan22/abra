package brain

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/graph"
	"github.com/hermawan22/abra/internal/store"
)

type preparedIngestDocument struct {
	input               IngestDocumentInput
	content             string
	metadata            map[string]any
	sourceConfigID      string
	ingestionJobID      string
	authority           string
	authorityScore      float64
	claimStatus         string
	chunks              []string
	chunkEmbeddings     []ai.Embedding
	chunkEmbeddingModel string
	claims              []string
	claimEmbeddings     []ai.Embedding
	claimEmbeddingModel string
	codePath            string
}

type ingestSourceLock struct {
	scope     string
	sourceURL string
}

func (s *Service) IngestDocument(ctx context.Context, input IngestDocumentInput) (IngestDocumentResult, error) {
	doc, err := s.prepareIngestDocument(input)
	if err != nil {
		return IngestDocumentResult{}, err
	}
	docs, err := s.embedPreparedDocuments(ctx, []preparedIngestDocument{doc})
	if err != nil {
		return IngestDocumentResult{}, err
	}
	var result IngestDocumentResult
	err = s.db.WithTx(ctx, func(txStore *store.Store) error {
		txService := *s
		txService.db = txStore
		if err := lockPreparedIngestSources(ctx, txStore, docs); err != nil {
			return err
		}
		persisted, err := txService.persistPreparedIngestDocument(ctx, docs[0])
		if err != nil {
			return err
		}
		result = persisted
		return nil
	})
	return result, err
}

func (s *Service) IngestDocuments(ctx context.Context, inputs []IngestDocumentInput) ([]IngestDocumentResult, error) {
	if len(inputs) == 0 {
		return []IngestDocumentResult{}, nil
	}
	prepared := make([]preparedIngestDocument, 0, len(inputs))
	for index, input := range inputs {
		doc, err := s.prepareIngestDocument(input)
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", index, err)
		}
		prepared = append(prepared, doc)
	}
	prepared, err := s.embedPreparedDocuments(ctx, prepared)
	if err != nil {
		return nil, err
	}
	results := make([]IngestDocumentResult, 0, len(prepared))
	err = s.db.WithTx(ctx, func(txStore *store.Store) error {
		txService := *s
		txService.db = txStore
		if err := lockPreparedIngestSources(ctx, txStore, prepared); err != nil {
			return err
		}
		for index, doc := range prepared {
			result, err := txService.persistPreparedIngestDocument(ctx, doc)
			if err != nil {
				return fmt.Errorf("document %d: %w", index, err)
			}
			results = append(results, result)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Service) prepareIngestDocument(input IngestDocumentInput) (preparedIngestDocument, error) {
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.SourceURL = strings.TrimSpace(input.SourceURL)
	input.Title = strings.TrimSpace(input.Title)
	input.Scope = strings.TrimSpace(input.Scope)
	if input.SourceType == "" || input.SourceURL == "" || input.Title == "" || input.Scope == "" || strings.TrimSpace(input.Content) == "" {
		return preparedIngestDocument{}, fmt.Errorf("source_type, source_url, title, scope, and content are required")
	}

	content := input.Content
	if s.cfg.RedactPII {
		content = redact(content)
	}
	explicitMetadata := map[string]any{"ingest_complete": false}
	if strings.TrimSpace(input.Authority) != "" {
		explicitMetadata["authority"] = strings.TrimSpace(input.Authority)
	}
	if input.AuthorityScore > 0 {
		explicitMetadata["authority_score"] = input.AuthorityScore
	}
	metadata := mergeMetadata(input.Metadata, explicitMetadata)
	sourceConfigID := metadataString(input.Metadata, "source_config_id")
	ingestionJobID := metadataString(input.Metadata, "ingestion_job_id")
	authority, authorityScore, claimStatus := ingestAuthorityDefaults(metadata, sourceConfigID)
	input.Content = content
	return preparedIngestDocument{
		input:          input,
		content:        content,
		metadata:       metadata,
		sourceConfigID: sourceConfigID,
		ingestionJobID: ingestionJobID,
		authority:      authority,
		authorityScore: authorityScore,
		claimStatus:    claimStatus,
		chunks:         chunkText(content, 1200),
		claims:         extractClaimsForDocument(input, content),
		codePath:       codeGraphPath(input),
	}, nil
}

func ingestAuthorityDefaults(metadata map[string]any, sourceConfigID string) (string, float64, string) {
	if strings.TrimSpace(sourceConfigID) == "" {
		if metadataString(metadata, "direct_ingest_trust") == "cli-seed" {
			authority := metadataString(metadata, "authority")
			if authority == "" {
				authority = "official-doc"
			}
			authorityScore := metadataFloat(metadata, "authority_score")
			if authorityScore == 0 {
				authorityScore = 0.75
			}
			return authority, authorityScore, "verified"
		}
		if authority := metadataString(metadata, "authority"); authority != "" && authority != "manual-unverified" {
			authorityScore := metadataFloat(metadata, "authority_score")
			if authorityScore == 0 {
				authorityScore = 0.75
			}
			return authority, authorityScore, "verified"
		}
		return "manual-unverified", 0.35, "unverified"
	}
	authority := metadataString(metadata, "authority")
	if authority == "" {
		authority = "official-doc"
	}
	authorityScore := metadataFloat(metadata, "authority_score")
	if authorityScore == 0 {
		authorityScore = 0.75
	}
	return authority, authorityScore, "verified"
}

func (s *Service) embedPreparedDocuments(ctx context.Context, docs []preparedIngestDocument) ([]preparedIngestDocument, error) {
	chunkTexts := []string{}
	chunkRefs := []struct{ doc, index int }{}
	for docIndex, doc := range docs {
		docs[docIndex].chunkEmbeddings = make([]ai.Embedding, len(doc.chunks))
		for chunkIndex, chunk := range doc.chunks {
			chunkRefs = append(chunkRefs, struct{ doc, index int }{doc: docIndex, index: chunkIndex})
			chunkTexts = append(chunkTexts, chunk)
		}
	}
	if len(chunkTexts) > 0 {
		response, err := s.embedTexts(ctx, chunkTexts)
		if err != nil {
			return nil, err
		}
		for globalIndex, ref := range chunkRefs {
			docs[ref.doc].chunkEmbeddings[ref.index] = response.Embeddings[globalIndex]
			docs[ref.doc].chunkEmbeddingModel = response.Model
		}
	}

	claimTexts := []string{}
	claimRefs := []struct{ doc, index int }{}
	for docIndex, doc := range docs {
		docs[docIndex].claimEmbeddings = make([]ai.Embedding, len(doc.claims))
		for claimIndex, claim := range doc.claims {
			claimRefs = append(claimRefs, struct{ doc, index int }{doc: docIndex, index: claimIndex})
			claimTexts = append(claimTexts, claim)
		}
	}
	if len(claimTexts) > 0 {
		response, err := s.embedTexts(ctx, claimTexts)
		if err != nil {
			return nil, err
		}
		for globalIndex, ref := range claimRefs {
			docs[ref.doc].claimEmbeddings[ref.index] = response.Embeddings[globalIndex]
			docs[ref.doc].claimEmbeddingModel = response.Model
		}
	}
	return docs, nil
}

func preparedIngestSourceLocks(docs []preparedIngestDocument) []ingestSourceLock {
	seen := map[string]ingestSourceLock{}
	for _, doc := range docs {
		lock := ingestSourceLock{
			scope:     strings.TrimSpace(doc.input.Scope),
			sourceURL: strings.TrimSpace(doc.input.SourceURL),
		}
		if lock.scope == "" || lock.sourceURL == "" {
			continue
		}
		key := lock.scope + "\x00" + lock.sourceURL
		seen[key] = lock
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	locks := make([]ingestSourceLock, 0, len(keys))
	for _, key := range keys {
		locks = append(locks, seen[key])
	}
	return locks
}

func lockPreparedIngestSources(ctx context.Context, db *store.Store, docs []preparedIngestDocument) error {
	for _, lock := range preparedIngestSourceLocks(docs) {
		if err := db.LockSourceIngest(ctx, lock.scope, lock.sourceURL); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) persistPreparedIngestDocument(ctx context.Context, doc preparedIngestDocument) (IngestDocumentResult, error) {
	input := doc.input
	content := doc.content
	sourceConfigID := doc.sourceConfigID
	ingestionJobID := doc.ingestionJobID
	authority := doc.authority
	authorityScore := doc.authorityScore
	claimStatus := doc.claimStatus
	chunks := doc.chunks
	claims := doc.claims
	codePath := doc.codePath

	documentID, err := s.db.UpsertDocument(ctx, store.DocumentRecord{
		SourceType:      input.SourceType,
		SourceURL:       input.SourceURL,
		SourceID:        input.SourceID,
		SourceConfigID:  sourceConfigID,
		IngestionJobID:  ingestionJobID,
		Title:           input.Title,
		Scope:           input.Scope,
		ContentChecksum: checksum(content),
		SourceUpdatedAt: input.SourceUpdatedAt,
		Authority:       authority,
		AuthorityScore:  authorityScore,
		Metadata:        doc.metadata,
	})
	if err != nil {
		return IngestDocumentResult{}, err
	}

	records := make([]store.ChunkRecord, 0, len(chunks))
	for i, chunk := range chunks {
		records = append(records, store.ChunkRecord{
			Content:             chunk,
			Embedding:           doc.chunkEmbeddings[i].Vector,
			EmbeddingProvider:   s.cfg.Embedding.Provider,
			EmbeddingModel:      doc.chunkEmbeddingModel,
			EmbeddingDimensions: doc.chunkEmbeddings[i].Dimensions,
			SourceConfigID:      sourceConfigID,
			IngestionJobID:      ingestionJobID,
			Metadata:            lineageMetadata(sourceConfigID, ingestionJobID),
		})
	}
	if err := s.db.ReplaceChunks(ctx, documentID, input.Scope, records); err != nil {
		return IngestDocumentResult{}, err
	}

	entityCount := 0
	relationCount := 0
	summaryCount := 0
	conflictCount := 0
	deprecatedClaimCount := 0
	deprecatedRelationCount := 0
	deletedSummaryCount := 0
	graphRefreshResult, err := s.db.BeginSourceGraphRefresh(ctx, input.Scope, input.SourceURL, ingestionJobID)
	if err != nil {
		return IngestDocumentResult{}, err
	}
	deprecatedRelationCount = int(graphRefreshResult.DeprecatedRelations)
	deletedSummaryCount = int(graphRefreshResult.DeletedSummaries)
	if codePath != "" && graph.IsCodeGraphPath(codePath) {
		candidates := graph.ExtractCodeFile(graph.CodeFile{
			Path:      codePath,
			Content:   content,
			SourceID:  input.SourceID,
			SourceURL: input.SourceURL,
		})
		entities, relations, err := s.persistGraphCandidates(ctx, graphPersistInput{
			Scope:          input.Scope,
			SourceURL:      input.SourceURL,
			SourceType:     input.SourceType,
			SourceConfigID: sourceConfigID,
			IngestionJobID: ingestionJobID,
			DocumentID:     documentID,
			Metadata:       lineageMetadata(sourceConfigID, ingestionJobID),
			Description:    "Extracted from code structure: " + codePath,
			Candidates:     candidates,
		})
		if err != nil {
			return IngestDocumentResult{}, err
		}
		entityCount += entities
		relationCount += relations
	}

	refreshResult, err := s.db.BeginSourceClaimRefresh(ctx, input.Scope, input.SourceType, input.SourceURL, ingestionJobID)
	if err != nil {
		return IngestDocumentResult{}, err
	}
	deprecatedClaimCount = int(refreshResult.Deprecated)
	for i, claim := range claims {
		claimID, err := s.db.InsertClaim(ctx, store.ClaimRecord{
			ClaimText:           claim,
			Scope:               input.Scope,
			SourceURL:           input.SourceURL,
			SourceType:          input.SourceType,
			Authority:           authority,
			Status:              claimStatus,
			Confidence:          authorityScore,
			Embedding:           doc.claimEmbeddings[i].Vector,
			EmbeddingProvider:   s.cfg.Embedding.Provider,
			EmbeddingModel:      doc.claimEmbeddingModel,
			EmbeddingDimensions: doc.claimEmbeddings[i].Dimensions,
			SourceConfigID:      sourceConfigID,
			IngestionJobID:      ingestionJobID,
			AuthorityScore:      authorityScore,
			Metadata: mergeMetadata(lineageMetadata(sourceConfigID, ingestionJobID), map[string]any{
				"extracted":       true,
				"document_title":  input.Title,
				"authority_score": authorityScore,
			}),
		})
		if err != nil {
			return IngestDocumentResult{}, err
		}
		if err := s.db.AddEvidence(ctx, store.EvidenceRecord{
			ClaimID:    claimID,
			DocumentID: documentID,
			Quote:      claim,
			SourceURL:  input.SourceURL,
			SourceType: input.SourceType,
		}); err != nil {
			return IngestDocumentResult{}, err
		}
		conflicts, err := s.detectClaimConflicts(ctx, claimID, claim, input.Scope, input.SourceURL, mergeMetadata(lineageMetadata(sourceConfigID, ingestionJobID), map[string]any{
			"document_id": documentID,
			"source_type": input.SourceType,
		}))
		if err != nil {
			return IngestDocumentResult{}, err
		}
		conflictCount += conflicts
		entities, relations, err := s.persistGraphCandidates(ctx, graphPersistInput{
			Scope:          input.Scope,
			SourceURL:      input.SourceURL,
			SourceType:     input.SourceType,
			SourceConfigID: sourceConfigID,
			IngestionJobID: ingestionJobID,
			DocumentID:     documentID,
			ClaimID:        claimID,
			Metadata:       mergeMetadata(lineageMetadata(sourceConfigID, ingestionJobID), map[string]any{"claim_id": claimID, "claim_text": claim}),
			Description:    "Extracted from claim: " + claim,
			Candidates:     graph.ExtractFromClaims([]string{claim}),
		})
		if err != nil {
			return IngestDocumentResult{}, err
		}
		entityCount += entities
		relationCount += relations
	}
	summaries, err := s.upsertMemorySummaries(ctx, summaryInput{
		DocumentID: documentID,
		Input:      input,
		Content:    content,
		CodePath:   codePath,
		Metadata:   lineageMetadata(sourceConfigID, ingestionJobID),
	})
	if err != nil {
		return IngestDocumentResult{}, err
	}
	summaryCount += summaries
	if err := s.db.InsertAuditEvent(ctx, "document.ingested", "document", documentID, input.Scope, input.SourceURL, map[string]any{"chunks": len(chunks), "claims": len(claims), "deprecated_claims": deprecatedClaimCount, "deprecated_relations": deprecatedRelationCount, "deleted_summaries": deletedSummaryCount, "conflicts": conflictCount, "entities": entityCount, "relations": relationCount, "summaries": summaryCount}); err != nil {
		return IngestDocumentResult{}, err
	}
	if err := s.db.MarkDocumentIngestComplete(ctx, documentID); err != nil {
		return IngestDocumentResult{}, err
	}

	return IngestDocumentResult{DocumentID: documentID, Chunks: len(chunks), Claims: len(claims), DeprecatedClaims: deprecatedClaimCount, DeprecatedRelations: deprecatedRelationCount, DeletedSummaries: deletedSummaryCount, Conflicts: conflictCount, Entities: entityCount, Relations: relationCount, Summaries: summaryCount}, nil
}
