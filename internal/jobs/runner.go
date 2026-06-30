package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hermawan22/abra/internal/ingest"
	"github.com/hermawan22/abra/internal/observability"
	"go.opentelemetry.io/otel/attribute"
)

const (
	DefaultMaxSourcesPerRun             = 25
	DefaultMaxChangedDocumentsPerSource = 100
	DefaultWorkerIngestBatchSize        = 50
	DefaultWorkerConcurrency            = 1
	MaxWorkerConcurrency                = 32
	DefaultSourceTimeout                = 2 * time.Minute
	DefaultLeaseTimeout                 = 5 * time.Minute
	DefaultLeaseOwner                   = "abra-worker"
	DefaultGitCloneDepth                = 1
)

type SourceStore interface {
	RecoverStaleIngestionJobs(ctx context.Context, leaseTimeout time.Duration) (int64, error)
	EnqueueScheduledSources(ctx context.Context, limit int) (int, error)
	ClaimQueuedIngestionJobs(ctx context.Context, limit int, leaseOwner string) ([]QueuedIngestionJob, error)
	HeartbeatIngestionJob(ctx context.Context, jobID string, leaseOwner string) error
	FinishIngestionJob(ctx context.Context, jobID string, leaseOwner string, stats SourceStats, runErr error) (string, error)
	GetWebhookDocument(ctx context.Context, jobID string) (IngestDocumentInput, error)
	DocumentState(ctx context.Context, doc ingest.Document) (DocumentState, error)
	MarkSourceSuccess(ctx context.Context, sourceID string, stats SourceStats) error
	MarkSourceError(ctx context.Context, sourceID string, err error) error
}

type BatchDocumentStateStore interface {
	DocumentStates(ctx context.Context, docs []ingest.Document) (map[string]DocumentState, error)
}

type DocumentState struct {
	Found             bool
	ContentChecksum   string
	IngestChecksum    string
	IngestFingerprint string
	IngestComplete    bool
}

type DocumentIngestor interface {
	IngestDocument(ctx context.Context, input IngestDocumentInput) (IngestDocumentResult, error)
}

type BatchDocumentIngestor interface {
	IngestDocuments(ctx context.Context, inputs []IngestDocumentInput) ([]IngestDocumentResult, error)
}

type IngestDocumentInput struct {
	SourceType      string
	SourceURL       string
	SourceID        string
	Title           string
	Scope           string
	Content         string
	SourceUpdatedAt string
	Metadata        map[string]any
}

type IngestDocumentResult struct {
	DocumentID string
	Chunks     int
	Claims     int
}

type Options struct {
	MaxSourcesPerRun             int
	MaxChangedDocumentsPerSource int
	Concurrency                  int
	SourceTimeout                time.Duration
	LeaseTimeout                 time.Duration
	LeaseOwner                   string
	GitCacheDir                  string
	GitCloneDepth                int
	Logger                       *slog.Logger
}

type Runner struct {
	store    SourceStore
	ingestor DocumentIngestor
	options  Options
}

type RunStats struct {
	Sources               int
	SourcesSucceeded      int
	SourcesFailed         int
	DocumentsSeen         int
	DocumentsChanged      int
	DocumentsSkipped      int
	DocumentsDeferred     int
	FilesSkippedLarge     int
	FilesSkippedBinary    int
	FilesSkippedGenerated int
	ChunksWritten         int
	ClaimsWritten         int
}

type SourceStats struct {
	DocumentsSeen         int
	DocumentsChanged      int
	DocumentsSkipped      int
	DocumentsDeferred     int
	FilesSkippedLarge     int
	FilesSkippedBinary    int
	FilesSkippedGenerated int
	ChunksWritten         int
	ClaimsWritten         int
	SourceDocuments       []SourceDocumentRef
}

type SourceDocumentRef struct {
	SourceType string
	SourceURL  string
	Scope      string
}

type QueuedIngestionJob struct {
	ID          string
	Source      SourceConfig
	TriggerType string
	Attempts    int
	MaxAttempts int
}

func NewRunner(store SourceStore, ingestor DocumentIngestor, options Options) *Runner {
	if options.MaxSourcesPerRun <= 0 {
		options.MaxSourcesPerRun = DefaultMaxSourcesPerRun
	}
	if options.MaxChangedDocumentsPerSource <= 0 {
		options.MaxChangedDocumentsPerSource = DefaultMaxChangedDocumentsPerSource
	}
	if options.Concurrency <= 0 {
		options.Concurrency = DefaultWorkerConcurrency
	}
	if options.Concurrency > MaxWorkerConcurrency {
		options.Concurrency = MaxWorkerConcurrency
	}
	if options.SourceTimeout <= 0 {
		options.SourceTimeout = DefaultSourceTimeout
	}
	if options.LeaseTimeout <= 0 {
		options.LeaseTimeout = DefaultLeaseTimeout
	}
	if options.LeaseOwner == "" {
		options.LeaseOwner = DefaultLeaseOwner
	}
	if options.GitCacheDir == "" {
		options.GitCacheDir = filepath.Join(os.TempDir(), "abra-git-cache")
	}
	if options.GitCloneDepth <= 0 {
		options.GitCloneDepth = DefaultGitCloneDepth
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Runner{store: store, ingestor: ingestor, options: options}
}

func (r *Runner) RunOnce(ctx context.Context) (RunStats, error) {
	ctx, span := observability.Start(ctx, "abra.worker.run_once")
	var runErr error
	defer func() {
		observability.End(span, runErr)
	}()
	if recovered, err := r.store.RecoverStaleIngestionJobs(ctx, r.options.LeaseTimeout); err != nil {
		r.options.Logger.Error("stale ingestion job recovery failed", "error", err)
	} else if recovered > 0 {
		r.options.Logger.Warn("stale ingestion jobs recovered", "count", recovered)
	}
	if _, err := r.store.EnqueueScheduledSources(ctx, r.options.MaxSourcesPerRun); err != nil {
		r.options.Logger.Error("scheduled source enqueue failed", "error", err)
	}
	queuedJobs, err := r.store.ClaimQueuedIngestionJobs(ctx, r.options.MaxSourcesPerRun, r.options.LeaseOwner)
	if err != nil {
		runErr = err
		return RunStats{}, err
	}
	span.SetAttributes(attribute.Int("abra.worker.claimed_jobs", len(queuedJobs)))
	span.SetAttributes(attribute.Int("abra.worker.concurrency", r.options.Concurrency))

	stats := RunStats{Sources: len(queuedJobs)}
	for _, result := range r.runQueuedJobs(ctx, queuedJobs) {
		stats.DocumentsSeen += result.Stats.DocumentsSeen
		stats.DocumentsChanged += result.Stats.DocumentsChanged
		stats.DocumentsSkipped += result.Stats.DocumentsSkipped
		stats.DocumentsDeferred += result.Stats.DocumentsDeferred
		stats.FilesSkippedLarge += result.Stats.FilesSkippedLarge
		stats.FilesSkippedBinary += result.Stats.FilesSkippedBinary
		stats.FilesSkippedGenerated += result.Stats.FilesSkippedGenerated
		stats.ChunksWritten += result.Stats.ChunksWritten
		stats.ClaimsWritten += result.Stats.ClaimsWritten
		if result.Succeeded {
			stats.SourcesSucceeded++
		}
		if result.Failed {
			stats.SourcesFailed++
		}
	}
	runErr = ctx.Err()
	span.SetAttributes(
		attribute.Int("abra.worker.sources", stats.Sources),
		attribute.Int("abra.worker.sources_succeeded", stats.SourcesSucceeded),
		attribute.Int("abra.worker.sources_failed", stats.SourcesFailed),
		attribute.Int("abra.worker.documents_seen", stats.DocumentsSeen),
		attribute.Int("abra.worker.documents_changed", stats.DocumentsChanged),
		attribute.Int("abra.worker.documents_skipped", stats.DocumentsSkipped),
		attribute.Int("abra.worker.documents_deferred", stats.DocumentsDeferred),
		attribute.Int("abra.worker.files_skipped_large", stats.FilesSkippedLarge),
		attribute.Int("abra.worker.files_skipped_binary", stats.FilesSkippedBinary),
		attribute.Int("abra.worker.files_skipped_generated", stats.FilesSkippedGenerated),
		attribute.Int("abra.worker.chunks_written", stats.ChunksWritten),
		attribute.Int("abra.worker.claims_written", stats.ClaimsWritten),
	)
	return stats, runErr
}

type queuedJobResult struct {
	Stats     SourceStats
	Succeeded bool
	Failed    bool
}

func (r *Runner) runQueuedJobs(ctx context.Context, queuedJobs []QueuedIngestionJob) []queuedJobResult {
	if len(queuedJobs) == 0 {
		return nil
	}
	if batch, ok := r.ingestor.(BatchDocumentIngestor); ok {
		return r.runQueuedJobsWithWebhookBatches(ctx, queuedJobs, batch)
	}
	return r.runQueuedJobsIndividually(ctx, queuedJobs)
}

func (r *Runner) runQueuedJobsWithWebhookBatches(ctx context.Context, queuedJobs []QueuedIngestionJob, batch BatchDocumentIngestor) []queuedJobResult {
	results := make([]queuedJobResult, 0, len(queuedJobs))
	sourceJobs := make([]QueuedIngestionJob, 0, len(queuedJobs))
	webhookJobs := make([]QueuedIngestionJob, 0, len(queuedJobs))
	flushWebhooks := func() {
		if len(webhookJobs) == 0 {
			return
		}
		results = append(results, r.runWebhookJobBatch(ctx, webhookJobs, batch)...)
		webhookJobs = webhookJobs[:0]
	}
	for _, queuedJob := range queuedJobs {
		if queuedJob.TriggerType == "webhook" {
			webhookJobs = append(webhookJobs, queuedJob)
			continue
		}
		flushWebhooks()
		sourceJobs = append(sourceJobs, queuedJob)
	}
	flushWebhooks()
	if len(sourceJobs) > 0 {
		results = append(results, r.runQueuedJobsIndividually(ctx, sourceJobs)...)
	}
	return results
}

func (r *Runner) runQueuedJobsIndividually(ctx context.Context, queuedJobs []QueuedIngestionJob) []queuedJobResult {
	if r.options.Concurrency <= 1 || len(queuedJobs) == 1 {
		results := make([]queuedJobResult, 0, len(queuedJobs))
		for _, queuedJob := range queuedJobs {
			results = append(results, r.runQueuedJob(ctx, queuedJob))
		}
		return results
	}

	workerCount := r.options.Concurrency
	if workerCount > len(queuedJobs) {
		workerCount = len(queuedJobs)
	}
	sourceLocks := sourceJobLocks(queuedJobs)
	jobs := make(chan QueuedIngestionJob)
	results := make(chan queuedJobResult, len(queuedJobs))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for queuedJob := range jobs {
				if lock := sourceLocks[queuedJob.Source.ID]; lock != nil {
					lock <- struct{}{}
					result := r.runQueuedJob(ctx, queuedJob)
					<-lock
					results <- result
					continue
				}
				results <- r.runQueuedJob(ctx, queuedJob)
			}
		}()
	}
	for _, queuedJob := range queuedJobs {
		jobs <- queuedJob
	}
	close(jobs)
	wg.Wait()
	close(results)

	out := make([]queuedJobResult, 0, len(queuedJobs))
	for result := range results {
		out = append(out, result)
	}
	return out
}

func sourceJobLocks(queuedJobs []QueuedIngestionJob) map[string]chan struct{} {
	locks := map[string]chan struct{}{}
	for _, queuedJob := range queuedJobs {
		sourceID := queuedJob.Source.ID
		if sourceID == "" {
			continue
		}
		if _, ok := locks[sourceID]; !ok {
			locks[sourceID] = make(chan struct{}, 1)
		}
	}
	return locks
}

func (r *Runner) runQueuedJob(ctx context.Context, queuedJob QueuedIngestionJob) queuedJobResult {
	source := queuedJob.Source
	jobID := queuedJob.ID
	var sourceStats SourceStats
	var err error
	if queuedJob.TriggerType == "webhook" {
		sourceStats, err = r.runWebhookDocument(ctx, jobID)
	} else {
		sourceStats, err = r.runSource(ctx, source, jobID)
	}
	return r.finishQueuedJob(ctx, queuedJob, sourceStats, err)
}

func (r *Runner) finishQueuedJob(ctx context.Context, queuedJob QueuedIngestionJob, sourceStats SourceStats, err error) queuedJobResult {
	source := queuedJob.Source
	jobID := queuedJob.ID
	finalStatus, finishErr := r.store.FinishIngestionJob(ctx, jobID, r.options.LeaseOwner, sourceStats, err)
	if finishErr != nil {
		r.options.Logger.Error("source ingestion job finish failed", "source_config_id", source.ID, "job_id", jobID, "trigger_type", queuedJob.TriggerType, "error", finishErr)
	}
	if err != nil {
		r.options.Logger.Error("source ingestion failed", "source_config_id", source.ID, "job_id", jobID, "trigger_type", queuedJob.TriggerType, "error", err)
		if finalStatus == "failed" && source.ID != "" {
			if markErr := r.store.MarkSourceError(ctx, source.ID, err); markErr != nil {
				r.options.Logger.Error("source error update failed", "source_config_id", source.ID, "job_id", jobID, "error", markErr)
			}
		} else if finalStatus == "retry" {
			r.options.Logger.Warn("source ingestion queued for retry", "source_config_id", source.ID, "job_id", jobID, "trigger_type", queuedJob.TriggerType)
		}
		return queuedJobResult{Stats: sourceStats, Failed: true}
	}
	if finalStatus != "succeeded" {
		r.options.Logger.Warn("source ingestion completed but job was not marked succeeded", "source_config_id", source.ID, "job_id", jobID, "trigger_type", queuedJob.TriggerType, "status", finalStatus)
		return queuedJobResult{Stats: sourceStats, Failed: true}
	}
	if queuedJob.TriggerType != "webhook" && source.ID != "" {
		if err := r.store.MarkSourceSuccess(ctx, source.ID, sourceStats); err != nil {
			r.options.Logger.Error("source success update failed", "source_config_id", source.ID, "job_id", jobID, "error", err)
		}
	}
	return queuedJobResult{Stats: sourceStats, Succeeded: true}
}

func (r *Runner) runWebhookJobBatch(ctx context.Context, queuedJobs []QueuedIngestionJob, batch BatchDocumentIngestor) []queuedJobResult {
	ctx, span := observability.Start(ctx, "abra.worker.webhook_batch")
	var runErr error
	defer func() {
		observability.End(span, runErr)
	}()
	sourceCtx, cancel := context.WithTimeout(ctx, r.options.SourceTimeout)
	defer cancel()
	heartbeatErrs := make([]<-chan error, 0, len(queuedJobs))
	docs := make([]IngestDocumentInput, 0, len(queuedJobs))
	stats := make([]SourceStats, len(queuedJobs))
	for index, queuedJob := range queuedJobs {
		heartbeatErrs = append(heartbeatErrs, r.startHeartbeatLoop(sourceCtx, queuedJob.ID, cancel))
		if err := r.heartbeatJob(sourceCtx, queuedJob.ID); err != nil {
			runErr = err
			return r.finishWebhookBatchWithError(ctx, queuedJobs, stats, err)
		}
		doc, err := r.store.GetWebhookDocument(sourceCtx, queuedJob.ID)
		if err != nil {
			runErr = err
			return r.finishWebhookBatchWithError(ctx, queuedJobs, stats, err)
		}
		doc.Metadata = mergeJobMetadata(doc.Metadata, map[string]any{"ingestion_job_id": queuedJob.ID})
		docs = append(docs, doc)
		stats[index].DocumentsSeen = 1
	}
	if err := firstHeartbeatLoopErr(heartbeatErrs); err != nil {
		runErr = err
		return r.finishWebhookBatchWithError(ctx, queuedJobs, stats, err)
	}
	results, err := batch.IngestDocuments(sourceCtx, docs)
	if err != nil {
		if heartbeatErr := firstHeartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
			runErr = heartbeatErr
			return r.finishWebhookBatchWithError(ctx, queuedJobs, stats, heartbeatErr)
		}
		runErr = err
		return r.finishWebhookBatchWithError(ctx, queuedJobs, stats, err)
	}
	if len(results) != len(docs) {
		err := fmt.Errorf("batch ingest returned %d results for %d webhook inputs", len(results), len(docs))
		runErr = err
		return r.finishWebhookBatchWithError(ctx, queuedJobs, stats, err)
	}
	if heartbeatErr := firstHeartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
		runErr = heartbeatErr
		return r.finishWebhookBatchWithError(ctx, queuedJobs, stats, heartbeatErr)
	}
	out := make([]queuedJobResult, 0, len(queuedJobs))
	for index, queuedJob := range queuedJobs {
		stats[index].DocumentsChanged = 1
		stats[index].ChunksWritten = results[index].Chunks
		stats[index].ClaimsWritten = results[index].Claims
		out = append(out, r.finishQueuedJob(ctx, queuedJob, stats[index], nil))
	}
	span.SetAttributes(
		attribute.Int("abra.webhook.jobs", len(queuedJobs)),
		attribute.Int("abra.webhook.documents_changed", len(results)),
	)
	return out
}

func (r *Runner) finishWebhookBatchWithError(ctx context.Context, queuedJobs []QueuedIngestionJob, stats []SourceStats, err error) []queuedJobResult {
	out := make([]queuedJobResult, 0, len(queuedJobs))
	for index, queuedJob := range queuedJobs {
		out = append(out, r.finishQueuedJob(ctx, queuedJob, stats[index], err))
	}
	return out
}

func (r *Runner) runWebhookDocument(ctx context.Context, jobID string) (SourceStats, error) {
	ctx, span := observability.Start(ctx, "abra.worker.webhook_document")
	var runErr error
	defer func() {
		observability.End(span, runErr)
	}()
	sourceCtx, cancel := context.WithTimeout(ctx, r.options.SourceTimeout)
	defer cancel()
	heartbeatErrs := r.startHeartbeatLoop(sourceCtx, jobID, cancel)
	if err := r.heartbeatJob(sourceCtx, jobID); err != nil {
		runErr = err
		return SourceStats{}, err
	}
	doc, err := r.store.GetWebhookDocument(sourceCtx, jobID)
	if err != nil {
		runErr = err
		return SourceStats{}, err
	}
	doc.Metadata = mergeJobMetadata(doc.Metadata, map[string]any{"ingestion_job_id": jobID})
	result, err := r.ingestor.IngestDocument(sourceCtx, doc)
	if err != nil {
		if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
			runErr = heartbeatErr
			return SourceStats{}, heartbeatErr
		}
		runErr = err
		return SourceStats{DocumentsSeen: 1}, err
	}
	if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
		runErr = heartbeatErr
		return SourceStats{}, heartbeatErr
	}
	stats := SourceStats{
		DocumentsSeen:    1,
		DocumentsChanged: 1,
		ChunksWritten:    result.Chunks,
		ClaimsWritten:    result.Claims,
	}
	span.SetAttributes(
		attribute.Int("abra.source.documents_seen", stats.DocumentsSeen),
		attribute.Int("abra.source.documents_changed", stats.DocumentsChanged),
		attribute.Int("abra.source.chunks_written", stats.ChunksWritten),
		attribute.Int("abra.source.claims_written", stats.ClaimsWritten),
	)
	return stats, nil
}

func (r *Runner) runSource(ctx context.Context, source SourceConfig, jobID string) (SourceStats, error) {
	ctx, span := observability.Start(ctx, "abra.worker.source",
		attribute.String("abra.source.type", string(source.SourceType)),
	)
	var runErr error
	defer func() {
		observability.End(span, runErr)
	}()
	if source.SourceType == ingest.SourceTypeMCP {
		stats, err := r.runMCPSource(ctx, source, jobID)
		runErr = err
		return stats, err
	}
	sourceCtx, cancel := context.WithTimeout(ctx, r.options.SourceTimeout)
	defer cancel()
	heartbeatErrs := r.startHeartbeatLoop(sourceCtx, jobID, cancel)

	spec, err := source.IngestSpec()
	if err != nil {
		runErr = err
		return SourceStats{}, err
	}
	spec, err = r.prepareIngestSpec(sourceCtx, spec)
	if err != nil {
		if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
			runErr = heartbeatErr
			return SourceStats{}, heartbeatErr
		}
		runErr = err
		return SourceStats{}, err
	}
	localIngestor, err := ingest.NewLocalRepoMarkdownIngestor(spec)
	if err != nil {
		runErr = err
		return SourceStats{}, err
	}
	result, err := localIngestor.IngestWithStats(sourceCtx)
	if err != nil {
		if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
			runErr = heartbeatErr
			return SourceStats{}, heartbeatErr
		}
		runErr = err
		return SourceStats{}, err
	}
	docs := result.Documents

	stats := SourceStats{DocumentsSeen: len(docs), SourceDocuments: sourceDocumentRefs(docs)}
	applySkippedFileStats(&stats, result.Skipped)
	span.SetAttributes(attribute.Int("abra.source.documents_seen", len(docs)))
	span.SetAttributes(
		attribute.Int("abra.source.files_skipped_large", stats.FilesSkippedLarge),
		attribute.Int("abra.source.files_skipped_binary", stats.FilesSkippedBinary),
		attribute.Int("abra.source.files_skipped_generated", stats.FilesSkippedGenerated),
	)
	changedInputs := make([]IngestDocumentInput, 0, minInt(len(docs), r.options.MaxChangedDocumentsPerSource))
	states, err := r.documentStates(sourceCtx, docs)
	if err != nil {
		runErr = err
		return stats, err
	}
	for _, doc := range docs {
		if err := sourceCtx.Err(); err != nil {
			if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
				runErr = heartbeatErr
				return stats, heartbeatErr
			}
			runErr = err
			return stats, err
		}
		if err := r.heartbeatJob(sourceCtx, jobID); err != nil {
			runErr = err
			return stats, err
		}
		state := states[documentStateKey(doc)]
		if unchanged(doc, state) {
			stats.DocumentsSkipped++
			continue
		}
		if len(changedInputs) >= r.options.MaxChangedDocumentsPerSource {
			stats.DocumentsDeferred++
			continue
		}
		changedInputs = append(changedInputs, documentInput(source, doc, jobID))
	}
	results, err := r.ingestDocumentBatches(sourceCtx, jobID, changedInputs, DefaultWorkerIngestBatchSize)
	if err != nil {
		if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
			runErr = heartbeatErr
			return stats, heartbeatErr
		}
		runErr = err
		return stats, err
	}
	accumulateResults(&stats, results)
	if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
		runErr = heartbeatErr
		return stats, heartbeatErr
	}
	span.SetAttributes(
		attribute.Int("abra.source.documents_changed", stats.DocumentsChanged),
		attribute.Int("abra.source.documents_skipped", stats.DocumentsSkipped),
		attribute.Int("abra.source.documents_deferred", stats.DocumentsDeferred),
		attribute.Int("abra.source.files_skipped_large", stats.FilesSkippedLarge),
		attribute.Int("abra.source.files_skipped_binary", stats.FilesSkippedBinary),
		attribute.Int("abra.source.files_skipped_generated", stats.FilesSkippedGenerated),
		attribute.Int("abra.source.chunks_written", stats.ChunksWritten),
		attribute.Int("abra.source.claims_written", stats.ClaimsWritten),
	)
	return stats, nil
}

func applySkippedFileStats(stats *SourceStats, skipped []ingest.SkippedFile) {
	for _, file := range skipped {
		switch file.Reason {
		case "too_large":
			stats.FilesSkippedLarge++
		case "binary":
			stats.FilesSkippedBinary++
		case "generated":
			stats.FilesSkippedGenerated++
		}
	}
}

func sourceDocumentRefs(docs []ingest.Document) []SourceDocumentRef {
	refs := make([]SourceDocumentRef, 0, len(docs))
	seen := map[string]struct{}{}
	for _, doc := range docs {
		ref := SourceDocumentRef{
			SourceType: string(doc.SourceType),
			SourceURL:  doc.SourceURL,
			Scope:      doc.Scope,
		}
		key := ref.SourceType + "\x00" + ref.SourceURL + "\x00" + ref.Scope
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, ref)
	}
	return refs
}

func (r *Runner) ingestDocumentBatches(ctx context.Context, jobID string, inputs []IngestDocumentInput, batchSize int) ([]IngestDocumentResult, error) {
	if len(inputs) == 0 {
		return []IngestDocumentResult{}, nil
	}
	if batchSize <= 0 {
		batchSize = DefaultWorkerIngestBatchSize
	}
	results := make([]IngestDocumentResult, 0, len(inputs))
	for start := 0; start < len(inputs); start += batchSize {
		end := minInt(start+batchSize, len(inputs))
		batchResults, err := r.ingestDocumentBatch(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		results = append(results, batchResults...)
		if err := r.heartbeatJob(ctx, jobID); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (r *Runner) ingestDocumentBatch(ctx context.Context, inputs []IngestDocumentInput) ([]IngestDocumentResult, error) {
	if len(inputs) == 0 {
		return []IngestDocumentResult{}, nil
	}
	if batch, ok := r.ingestor.(BatchDocumentIngestor); ok {
		results, err := batch.IngestDocuments(ctx, inputs)
		if err != nil {
			return nil, err
		}
		if len(results) != len(inputs) {
			return nil, fmt.Errorf("batch ingest returned %d results for %d inputs", len(results), len(inputs))
		}
		return results, nil
	}
	results := make([]IngestDocumentResult, 0, len(inputs))
	for _, input := range inputs {
		result, err := r.ingestor.IngestDocument(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("ingest %s: %w", input.SourceURL, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func accumulateResults(stats *SourceStats, results []IngestDocumentResult) {
	stats.DocumentsChanged += len(results)
	for _, result := range results {
		stats.ChunksWritten += result.Chunks
		stats.ClaimsWritten += result.Claims
	}
}

func (r *Runner) startHeartbeatLoop(ctx context.Context, jobID string, cancel context.CancelFunc) <-chan error {
	if jobID == "" {
		return nil
	}
	errs := make(chan error, 1)
	ticker := time.NewTicker(r.heartbeatInterval())
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.heartbeatJob(ctx, jobID); err != nil {
					select {
					case errs <- err:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	return errs
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (r *Runner) heartbeatInterval() time.Duration {
	leaseTimeout := r.options.LeaseTimeout
	if leaseTimeout <= 0 {
		leaseTimeout = DefaultLeaseTimeout
	}
	interval := leaseTimeout / 3
	if interval <= 0 {
		return time.Second
	}
	if interval > 30*time.Second {
		return 30 * time.Second
	}
	if interval < time.Second {
		return interval
	}
	return interval
}

func (r *Runner) heartbeatJob(ctx context.Context, jobID string) error {
	if jobID == "" {
		return nil
	}
	if err := r.store.HeartbeatIngestionJob(ctx, jobID, r.options.LeaseOwner); err != nil {
		return fmt.Errorf("heartbeat ingestion job %s: %w", jobID, err)
	}
	return nil
}

func heartbeatLoopErr(errs <-chan error) error {
	if errs == nil {
		return nil
	}
	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

func firstHeartbeatLoopErr(errs []<-chan error) error {
	for _, item := range errs {
		if err := heartbeatLoopErr(item); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) documentStates(ctx context.Context, docs []ingest.Document) (map[string]DocumentState, error) {
	if len(docs) == 0 {
		return map[string]DocumentState{}, nil
	}
	if batchStore, ok := r.store.(BatchDocumentStateStore); ok {
		return batchStore.DocumentStates(ctx, docs)
	}
	states := make(map[string]DocumentState, len(docs))
	for _, doc := range docs {
		state, err := r.store.DocumentState(ctx, doc)
		if err != nil {
			return nil, fmt.Errorf("read document state for %s: %w", doc.SourceURL, err)
		}
		states[documentStateKey(doc)] = state
	}
	return states, nil
}

func documentStateKey(doc ingest.Document) string {
	return string(doc.SourceType) + "\x00" + doc.SourceURL + "\x00" + doc.Scope
}

func unchanged(doc ingest.Document, state DocumentState) bool {
	if !state.Found {
		return false
	}
	if !state.IngestComplete {
		return false
	}
	if state.IngestFingerprint != "" {
		return state.IngestFingerprint == doc.Fingerprint
	}
	if state.IngestChecksum != "" {
		return state.IngestChecksum == doc.Checksum
	}
	return state.ContentChecksum == doc.Checksum
}

func documentInput(source SourceConfig, doc ingest.Document, jobID string) IngestDocumentInput {
	metadata := map[string]any{}
	for key, value := range source.Metadata {
		metadata[key] = value
	}
	for key, value := range doc.Metadata {
		metadata[key] = value
	}
	metadata["source_config_id"] = source.ID
	metadata["source_config_name"] = source.Name
	if jobID != "" {
		metadata["ingestion_job_id"] = jobID
	}
	metadata["ingest_path"] = doc.Path
	metadata["ingest_checksum"] = doc.Checksum
	metadata["ingest_fingerprint"] = doc.Fingerprint
	if source.Authority != "" {
		metadata["authority"] = source.Authority
	}
	if source.AuthorityScore > 0 {
		metadata["authority_score"] = source.AuthorityScore
	}
	return IngestDocumentInput{
		SourceType: string(doc.SourceType),
		SourceURL:  doc.SourceURL,
		SourceID:   doc.SourceID,
		Title:      doc.Title,
		Scope:      doc.Scope,
		Content:    doc.Content,
		Metadata:   metadata,
	}
}

func mergeJobMetadata(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}
