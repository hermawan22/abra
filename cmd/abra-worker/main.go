package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/jobs"
	"github.com/hermawan22/abra/internal/observability"
	"github.com/hermawan22/abra/internal/store"
	"go.opentelemetry.io/otel/attribute"
)

type brainIngestor struct {
	service *brain.Service
}

func (b brainIngestor) IngestDocument(ctx context.Context, input jobs.IngestDocumentInput) (jobs.IngestDocumentResult, error) {
	result, err := b.service.IngestDocument(ctx, brain.IngestDocumentInput{
		SourceType:      input.SourceType,
		SourceURL:       input.SourceURL,
		SourceID:        input.SourceID,
		Title:           input.Title,
		Scope:           input.Scope,
		Content:         input.Content,
		SourceUpdatedAt: input.SourceUpdatedAt,
		Metadata:        input.Metadata,
	})
	if err != nil {
		return jobs.IngestDocumentResult{}, err
	}
	return jobs.IngestDocumentResult{
		DocumentID: result.DocumentID,
		Chunks:     result.Chunks,
		Claims:     result.Claims,
	}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config failed", "error", err)
		os.Exit(1)
	}
	shutdownTracing, err := observability.SetupTracing(ctx, cfg.Tracing, "abra-worker")
	if err != nil {
		slog.Error("tracing setup failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			slog.Error("tracing shutdown failed", "error", err)
		}
	}()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	repo, err := jobs.OpenRepository(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("worker repository failed", "error", err)
		os.Exit(1)
	}
	defer repo.Close()

	brainService, err := brain.New(cfg, db)
	if err != nil {
		slog.Error("brain service failed", "error", err)
		os.Exit(1)
	}
	leaseOwner := workerLeaseOwner()
	runner := jobs.NewRunner(repo, brainIngestor{service: brainService}, jobs.Options{
		MaxSourcesPerRun:             cfg.WorkerMaxSourcesPerRun,
		MaxChangedDocumentsPerSource: cfg.WorkerMaxChangedDocumentsPerSource,
		Concurrency:                  cfg.WorkerConcurrency,
		SourceTimeout:                cfg.WorkerSourceTimeout,
		LeaseTimeout:                 cfg.WorkerLeaseTimeout,
		LeaseOwner:                   leaseOwner,
		GitCacheDir:                  cfg.GitCacheDir,
		GitCloneDepth:                cfg.GitCloneDepth,
		Logger:                       slog.Default(),
	})

	ticker := time.NewTicker(cfg.WorkerInterval)
	defer ticker.Stop()

	slog.Info("abra worker started", "interval", cfg.WorkerInterval.String(), "max_sources_per_run", cfg.WorkerMaxSourcesPerRun, "concurrency", cfg.WorkerConcurrency, "lease_owner", leaseOwner)
	run(ctx, cfg, db, runner)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run(ctx, cfg, db, runner)
		}
	}
}

func workerLeaseOwner() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	return "abra-worker:" + hostname + ":" + strconv.Itoa(os.Getpid())
}

func run(ctx context.Context, cfg config.Config, db *store.Store, runner *jobs.Runner) {
	ctx, span := observability.Start(ctx, "abra.worker.cycle")
	var runErr error
	defer func() {
		observability.End(span, runErr)
	}()
	expired, err := db.ExpireClaims(ctx)
	if err != nil {
		runErr = err
		slog.Error("claim expiry failed", "error", err)
	} else if expired > 0 {
		slog.Info("expired stale claims", "count", expired)
	}
	span.SetAttributes(attribute.Int64("abra.claims.expired", expired))

	rateLimitRetention := 24 * time.Hour
	if cfg.RateLimitWindow*2 > rateLimitRetention {
		rateLimitRetention = cfg.RateLimitWindow * 2
	}
	prunedRateLimitBuckets, err := db.PruneRateLimitBuckets(ctx, rateLimitRetention)
	if err != nil {
		runErr = err
		slog.Error("rate limit bucket pruning failed", "error", err)
	} else if prunedRateLimitBuckets > 0 {
		slog.Info("pruned rate limit buckets", "count", prunedRateLimitBuckets)
	}
	span.SetAttributes(attribute.Int64("abra.rate_limit.buckets_pruned", prunedRateLimitBuckets))

	stats, err := runner.RunOnce(ctx)
	if err != nil {
		runErr = err
		slog.Error("source ingestion cycle failed", "error", err)
	} else {
		span.SetAttributes(
			attribute.Int("abra.worker.sources", stats.Sources),
			attribute.Int("abra.worker.sources_succeeded", stats.SourcesSucceeded),
			attribute.Int("abra.worker.sources_failed", stats.SourcesFailed),
			attribute.Int("abra.worker.documents_seen", stats.DocumentsSeen),
			attribute.Int("abra.worker.documents_changed", stats.DocumentsChanged),
			attribute.Int("abra.worker.chunks_written", stats.ChunksWritten),
			attribute.Int("abra.worker.claims_written", stats.ClaimsWritten),
		)
		slog.Info(
			"source ingestion cycle finished",
			"sources", stats.Sources,
			"succeeded", stats.SourcesSucceeded,
			"failed", stats.SourcesFailed,
			"documents_seen", stats.DocumentsSeen,
			"documents_changed", stats.DocumentsChanged,
			"documents_skipped", stats.DocumentsSkipped,
			"documents_deferred", stats.DocumentsDeferred,
			"chunks_written", stats.ChunksWritten,
			"claims_written", stats.ClaimsWritten,
		)
	}

	if _, err := deliverAuditSink(ctx, cfg.AuditSink, db, slog.Default()); err != nil {
		runErr = err
		slog.Error("audit sink delivery failed", "error", err)
	}
}
