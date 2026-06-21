package jobs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hermawan22/abra/internal/ingest"
)

func TestRunnerIngestsOnlyChangedDocuments(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "README.md", "# Root\n\nUse cited claims for durable memory.")
	writeTestFile(t, root, "docs/changed.md", "# Changed\n\nFrontend apps should use Playwright for E2E tests.")

	unchangedChecksum := ingest.Checksum([]byte("# Root\n\nUse cited claims for durable memory."))
	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "team:example",
			SourceType: ingest.SourceTypeLocalRepo,
			Name:       "Docs",
			Config: map[string]any{
				"root":           root,
				"include":        []any{"**/*.md"},
				"repository_url": "https://github.com/acme/frontend-docs.git",
				"branch":         "main",
				"commit":         "abc1234",
				"provider":       "github",
			},
		}},
		states: map[string]DocumentState{
			"README.md": {
				Found:             true,
				IngestFingerprint: ingest.Fingerprint("docs", "README.md", unchangedChecksum),
				IngestComplete:    true,
			},
		},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 2 || stats.DocumentsSkipped != 1 || stats.DocumentsChanged != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 1 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
	input := brain.inputs[0]
	if input.Title != "Changed" {
		t.Fatalf("title = %q", input.Title)
	}
	if input.Metadata["ingest_checksum"] == "" || input.Metadata["ingest_fingerprint"] == "" {
		t.Fatalf("missing checksum/fingerprint metadata: %#v", input.Metadata)
	}
	if input.Metadata["ingestion_job_id"] != "job" {
		t.Fatalf("missing ingestion job lineage: %#v", input.Metadata)
	}
	if input.SourceURL != "https://github.com/acme/frontend-docs/blob/abc1234/docs/changed.md" {
		t.Fatalf("source url = %q", input.SourceURL)
	}
	if input.Metadata["git_remote_url"] != "https://github.com/acme/frontend-docs.git" ||
		input.Metadata["git_ref"] != "main" ||
		input.Metadata["git_revision"] != "abc1234" ||
		input.Metadata["git_path"] != "docs/changed.md" {
		t.Fatalf("missing git metadata: %#v", input.Metadata)
	}
	if !store.success {
		t.Fatal("source success was not recorded")
	}
}

func TestRunnerCountsPreReadSkippedFilesByReason(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "src/app.ts", "export const app = true\n")
	writeTestFile(t, root, "src/huge.ts", strings.Repeat("x", 128))
	writeTestFile(t, root, "src/generated/client.ts", "export const generated = true\n")
	if err := os.WriteFile(filepath.Join(root, "src", "binary.ts"), []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "code",
			Scope:      "repo:app",
			SourceType: ingest.SourceTypeLocalRepo,
			Name:       "Code",
			Config: map[string]any{
				"root":           root,
				"include":        []any{"README.md"},
				"include_code":   true,
				"max_file_bytes": 64,
			},
		}},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 1 || stats.DocumentsChanged != 1 ||
		stats.FilesSkippedLarge != 1 || stats.FilesSkippedBinary != 1 || stats.FilesSkippedGenerated != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 1 || brain.inputs[0].Title != "src/app.ts" {
		t.Fatalf("inputs = %+v", brain.inputs)
	}
}

func TestUnchangedRequiresCompletedIngest(t *testing.T) {
	doc := ingest.Document{
		Checksum:    "checksum",
		Fingerprint: "fingerprint",
	}
	state := DocumentState{
		Found:             true,
		ContentChecksum:   doc.Checksum,
		IngestChecksum:    doc.Checksum,
		IngestFingerprint: doc.Fingerprint,
		IngestComplete:    false,
	}
	if unchanged(doc, state) {
		t.Fatal("document with incomplete prior ingest must not be skipped")
	}
	state.IngestComplete = true
	if !unchanged(doc, state) {
		t.Fatal("completed matching document should be skipped")
	}
}

func TestRunnerDefersChangedDocumentsAtLimit(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "# A\n\nA should be ingested.")
	writeTestFile(t, root, "b.md", "# B\n\nB should be deferred.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "company",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states: map[string]DocumentState{},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 1,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsChanged != 1 || stats.DocumentsDeferred != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 1 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
}

func TestRunnerDoesNotMarkSourceErrorForRetryableJob(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "README.md", "# Retry\n\nThis document should retry when ingestion fails.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "company",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states:              map[string]DocumentState{},
		finishStatusOnError: "retry",
	}
	brain := &fakeIngestor{err: errors.New("temporary failure")}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesFailed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if store.markedError {
		t.Fatal("source was marked error while job remained retryable")
	}
}

func TestRunnerStopsWhenJobLeaseIsLost(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "README.md", "# Lease\n\nLong ingestion should stop when the worker loses its job lease.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "company",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states:       map[string]DocumentState{},
		heartbeatErr: errors.New("lease lost"),
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
		LeaseOwner:                   "worker-a",
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesFailed != 1 || stats.DocumentsChanged != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 0 {
		t.Fatalf("ingested %d documents after lease loss", len(brain.inputs))
	}
	if store.success {
		t.Fatal("source was marked successful after lease loss")
	}
	if store.finishLeaseOwner != "worker-a" {
		t.Fatalf("finish lease owner = %q", store.finishLeaseOwner)
	}
}

func TestRunnerDoesNotCountSourceSucceededWhenJobFinishStatusIsNotSucceeded(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "README.md", "# Finish\n\nThe source should not be marked successful unless the job is succeeded.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "company",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states:       map[string]DocumentState{},
		finishStatus: "running",
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesSucceeded != 0 || stats.SourcesFailed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if store.success {
		t.Fatal("source was marked successful despite non-succeeded job status")
	}
}

func TestRunnerIngestsGitRepoSource(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git executable is not available")
	}
	remote := t.TempDir()
	runGitTest(t, gitPath, remote, "init", "--initial-branch", "main")
	runGitTest(t, gitPath, remote, "config", "user.email", "abra@example.local")
	runGitTest(t, gitPath, remote, "config", "user.name", "Abra Test")
	writeTestFile(t, remote, "README.md", "# Remote Repo\n\nRemote repo ingestion should work.")
	writeTestFile(t, remote, "src/app.tsx", "export function App() { return <main>Abra</main>; }\n")
	runGitTest(t, gitPath, remote, "add", ".")
	runGitTest(t, gitPath, remote, "commit", "-m", "seed")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "remote",
			Scope:      "team:example",
			SourceType: ingest.SourceTypeGitRepo,
			Name:       "Remote",
			BaseURL:    remote,
			Config: map[string]any{
				"branch":       "main",
				"include":      []any{"README.md"},
				"include_code": true,
				"code_include": []any{"src/**/*.tsx"},
				"provider":     "bitbucket",
			},
		}},
		states: map[string]DocumentState{},
	}
	brain := &fakeIngestor{}
	cache := filepath.Join(t.TempDir(), "git-cache")
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                5 * time.Second,
		GitCacheDir:                  cache,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 2 || stats.DocumentsChanged != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 2 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
	for _, input := range brain.inputs {
		if input.Metadata["git_remote_url"] != remote ||
			input.Metadata["git_ref"] != "main" ||
			input.Metadata["git_revision"] == "" ||
			input.Metadata["git_cache_key"] == "" {
			t.Fatalf("missing git metadata for %s: %#v", input.SourceURL, input.Metadata)
		}
		if input.SourceType != string(ingest.SourceTypeGitRepo) {
			t.Fatalf("source type = %q", input.SourceType)
		}
	}
	if !store.success {
		t.Fatal("source success was not recorded")
	}
}

func TestRunnerProcessesWebhookDocumentJob(t *testing.T) {
	store := &fakeStore{
		queuedJobs: []QueuedIngestionJob{{
			ID:          "webhook-job",
			TriggerType: "webhook",
			Attempts:    1,
			MaxAttempts: 3,
		}},
		webhookDocument: IngestDocumentInput{
			SourceType: "jira",
			SourceURL:  "https://jira.example.invalid/browse/ABRA-1",
			SourceID:   "ABRA-1",
			Title:      "Webhook doc",
			Scope:      "repo:demo",
			Content:    "Webhook ingestion should be handled by the worker.",
			Metadata:   map[string]any{"connector_kind": "jira"},
		},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Sources != 1 || stats.SourcesSucceeded != 1 || stats.SourcesFailed != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.DocumentsSeen != 1 || stats.DocumentsChanged != 1 || stats.ChunksWritten != 2 || stats.ClaimsWritten != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 1 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
	input := brain.inputs[0]
	if input.SourceURL != "https://jira.example.invalid/browse/ABRA-1" {
		t.Fatalf("source url = %q", input.SourceURL)
	}
	if input.Metadata["connector_kind"] != "jira" || input.Metadata["ingestion_job_id"] != "webhook-job" {
		t.Fatalf("missing webhook metadata: %#v", input.Metadata)
	}
	if store.success {
		t.Fatal("webhook job should not mark a source config successful")
	}
}

func TestRunnerHonorsWorkerConcurrency(t *testing.T) {
	store := &fakeStore{
		queuedJobs: []QueuedIngestionJob{
			{ID: "webhook-job-1", TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
			{ID: "webhook-job-2", TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
			{ID: "webhook-job-3", TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
		},
		webhookDocuments: map[string]IngestDocumentInput{
			"webhook-job-1": webhookInput("webhook-job-1"),
			"webhook-job-2": webhookInput("webhook-job-2"),
			"webhook-job-3": webhookInput("webhook-job-3"),
		},
	}
	brain := newBlockingIngestor()
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		Concurrency:                  2,
		SourceTimeout:                time.Second,
	})

	done := make(chan struct{})
	var stats RunStats
	var err error
	go func() {
		stats, err = runner.RunOnce(context.Background())
		close(done)
	}()

	waitStarted(t, brain.started)
	waitStarted(t, brain.started)
	select {
	case <-brain.started:
		t.Fatal("third job started while concurrency was limited to 2")
	case <-time.After(25 * time.Millisecond):
	}
	close(brain.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not finish")
	}
	if err != nil {
		t.Fatal(err)
	}
	if maxActive := atomic.LoadInt32(&brain.maxActive); maxActive != 2 {
		t.Fatalf("max active ingests = %d, want 2", maxActive)
	}
	if stats.SourcesSucceeded != 3 || stats.DocumentsChanged != 3 || stats.ChunksWritten != 6 || stats.ClaimsWritten != 3 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestRunnerSerializesSameSourceJobs(t *testing.T) {
	store := &fakeStore{
		queuedJobs: []QueuedIngestionJob{
			{ID: "job-1", Source: SourceConfig{ID: "docs"}, TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
			{ID: "job-2", Source: SourceConfig{ID: "docs"}, TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
		},
		webhookDocuments: map[string]IngestDocumentInput{
			"job-1": webhookInput("job-1"),
			"job-2": webhookInput("job-2"),
		},
	}
	brain := newBlockingIngestor()
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		Concurrency:                  2,
		SourceTimeout:                time.Second,
	})

	done := make(chan struct{})
	var err error
	go func() {
		_, err = runner.RunOnce(context.Background())
		close(done)
	}()

	waitStarted(t, brain.started)
	select {
	case <-brain.started:
		t.Fatal("same-source job started concurrently")
	case <-time.After(25 * time.Millisecond):
	}
	close(brain.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not finish")
	}
	if err != nil {
		t.Fatal(err)
	}
	if maxActive := atomic.LoadInt32(&brain.maxActive); maxActive != 1 {
		t.Fatalf("max active ingests = %d, want 1", maxActive)
	}
}

func webhookInput(id string) IngestDocumentInput {
	return IngestDocumentInput{
		SourceType: "webhook",
		SourceURL:  "https://example.invalid/" + id,
		SourceID:   id,
		Title:      id,
		Scope:      "repo:test",
		Content:    "synthetic webhook payload",
		Metadata:   map[string]any{},
	}
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ingestion to start")
	}
}

type fakeStore struct {
	mu                  sync.Mutex
	sources             []SourceConfig
	states              map[string]DocumentState
	queuedJobs          []QueuedIngestionJob
	webhookDocument     IngestDocumentInput
	webhookDocuments    map[string]IngestDocumentInput
	success             bool
	markedError         bool
	jobs                []string
	err                 error
	finishStatus        string
	finishStatusOnError string
	heartbeatErr        error
	heartbeats          int
	finishLeaseOwner    string
}

func (f *fakeStore) RecoverStaleIngestionJobs(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func (f *fakeStore) EnqueueScheduledSources(context.Context, int) (int, error) {
	return len(f.sources), nil
}

func (f *fakeStore) ClaimQueuedIngestionJobs(context.Context, int, string) ([]QueuedIngestionJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queuedJobs) > 0 {
		return f.queuedJobs, nil
	}
	jobs := make([]QueuedIngestionJob, 0, len(f.sources))
	for _, source := range f.sources {
		f.jobs = append(f.jobs, "job")
		jobs = append(jobs, QueuedIngestionJob{ID: "job", Source: source, TriggerType: "schedule", Attempts: 1, MaxAttempts: 3})
	}
	return jobs, nil
}

func (f *fakeStore) HeartbeatIngestionJob(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeats++
	return f.heartbeatErr
}

func (f *fakeStore) FinishIngestionJob(_ context.Context, _ string, leaseOwner string, _ SourceStats, runErr error) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishLeaseOwner = leaseOwner
	if runErr != nil {
		if f.finishStatusOnError != "" {
			return f.finishStatusOnError, nil
		}
		return "failed", nil
	}
	if f.finishStatus != "" {
		return f.finishStatus, nil
	}
	return "succeeded", nil
}

func (f *fakeStore) DocumentState(_ context.Context, doc ingest.Document) (DocumentState, error) {
	if f.err != nil {
		return DocumentState{}, f.err
	}
	return f.states[doc.Path], nil
}

func (f *fakeStore) GetWebhookDocument(_ context.Context, jobID string) (IngestDocumentInput, error) {
	if f.err != nil {
		return IngestDocumentInput{}, f.err
	}
	if f.webhookDocuments != nil {
		if document, ok := f.webhookDocuments[jobID]; ok {
			return document, nil
		}
	}
	return f.webhookDocument, nil
}

func (f *fakeStore) MarkSourceSuccess(context.Context, string, SourceStats) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.success = true
	return nil
}

func (f *fakeStore) MarkSourceError(context.Context, string, error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markedError = true
	return nil
}

type fakeIngestor struct {
	mu     sync.Mutex
	inputs []IngestDocumentInput
	err    error
}

func (f *fakeIngestor) IngestDocument(_ context.Context, input IngestDocumentInput) (IngestDocumentResult, error) {
	if f.err != nil {
		return IngestDocumentResult{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, input)
	return IngestDocumentResult{DocumentID: "doc", Chunks: 2, Claims: 1}, nil
}

type blockingIngestor struct {
	started   chan struct{}
	release   chan struct{}
	active    int32
	maxActive int32
}

func newBlockingIngestor() *blockingIngestor {
	return &blockingIngestor{
		started: make(chan struct{}, 10),
		release: make(chan struct{}),
	}
}

func (b *blockingIngestor) IngestDocument(ctx context.Context, input IngestDocumentInput) (IngestDocumentResult, error) {
	active := atomic.AddInt32(&b.active, 1)
	for {
		maxActive := atomic.LoadInt32(&b.maxActive)
		if active <= maxActive || atomic.CompareAndSwapInt32(&b.maxActive, maxActive, active) {
			break
		}
	}
	b.started <- struct{}{}
	select {
	case <-ctx.Done():
		atomic.AddInt32(&b.active, -1)
		return IngestDocumentResult{}, ctx.Err()
	case <-b.release:
	}
	atomic.AddInt32(&b.active, -1)
	return IngestDocumentResult{DocumentID: input.SourceID, Chunks: 2, Claims: 1}, nil
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGitTest(t *testing.T, gitPath, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(gitPath, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
