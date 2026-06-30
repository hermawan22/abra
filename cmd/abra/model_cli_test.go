package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetupAndUpHelpMentionModelAutomation(t *testing.T) {
	setupHelp := commandUsage("setup")
	for _, want := range []string{"abra setup --yes", "abra setup --yes --no-models", "--skip-models", "not a chat model", "CLI commands only", "no manual env file editing"} {
		if !strings.Contains(setupHelp, want) {
			t.Fatalf("setup help missing %q:\n%s", want, setupHelp)
		}
	}
	upHelp := commandUsage("up")
	for _, want := range []string{"abra up [--no-models]", "starts the default local", "embedding runner", "--no-models"} {
		if !strings.Contains(upHelp, want) {
			t.Fatalf("up help missing %q:\n%s", want, upHelp)
		}
	}
}

func TestSetupNormalizesAggressiveWorkerInterval(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"ABRA_API_KEYS=dev-token",
		"WORKER_INTERVAL=1s",
		"",
	}, "\n"))

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--yes", "--no-start"}); err != nil {
			t.Fatalf("setup error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["WORKER_INTERVAL"] != "30s" {
		t.Fatalf("worker interval = %q\noutput:\n%s", values["WORKER_INTERVAL"], output)
	}
}

func TestSetupStackArgsDropProviderBaseURL(t *testing.T) {
	args := parseArgs([]string{
		"setup",
		"--compatible",
		"--base-url", "https://models.example.invalid/v1",
		"--embedding-base-url", "https://models.example.invalid/v2",
		"--token", "test-token",
		"--env-file", ".tmp/custom.env",
	})
	stackArgs := setupStackArgs(args)
	if got := flag(stackArgs, "base-url", ""); got != "" {
		t.Fatalf("stack base-url = %q, want empty", got)
	}
	if got := flag(stackArgs, "embedding-base-url", ""); got != "https://models.example.invalid/v2" {
		t.Fatalf("embedding-base-url = %q", got)
	}
	if cfg(stackArgs).Token != "test-token" || cfg(stackArgs).EnvFile != ".tmp/custom.env" {
		t.Fatalf("stack args lost runtime flags: %+v", cfg(stackArgs))
	}
}

func TestUpgradeVerifiesInstallScriptAttestationBeforeExecuting(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceScript := filepath.Join(root, "install.sh")
	mustWrite(t, sourceScript, "echo should-not-run\n")
	marker := filepath.Join(root, "executed")
	mustWrite(t, filepath.Join(bin, "curl"), `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    out="$1"
  fi
  shift
done
cp "$TEST_INSTALL_SCRIPT" "$out"
`)
	mustWrite(t, filepath.Join(bin, "sh"), `#!/bin/sh
printf executed > "$TEST_INSTALL_MARKER"
`)
	mustWrite(t, filepath.Join(bin, "gh"), `#!/bin/sh
exit 42
`)
	for _, name := range []string{"curl", "sh", "gh"} {
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_INSTALL_SCRIPT", sourceScript)
	t.Setenv("TEST_INSTALL_MARKER", marker)
	t.Setenv("ABRA_INSTALL_SCRIPT", "https://example.invalid/install.sh")
	t.Setenv("ABRA_VERIFY_INSTALL_ATTESTATION", "1")

	err := upgrade(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if err == nil || !strings.Contains(err.Error(), "attestation verification failed for install.sh") {
		t.Fatalf("upgrade error = %v, want install.sh attestation failure", err)
	}
	if fileExists(marker) {
		t.Fatalf("install script executed before successful attestation")
	}
}

func TestSetupYesNoStartDefaultsLocalQwen(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--yes", "--no-start"}); err != nil {
			t.Fatalf("setup error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "local" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:8080/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_MODEL"] != defaultServedModelName {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "1024" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
	if values["EMBEDDING_TIMEOUT"] != "10m" {
		t.Fatalf("timeout = %q", values["EMBEDDING_TIMEOUT"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "6" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "3000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
	}
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "1" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
	}
	if values["WORKER_INTERVAL"] != "30s" {
		t.Fatalf("worker interval = %q", values["WORKER_INTERVAL"])
	}
	if values["RERANKER_PROVIDER"] != "none" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_MODEL"] != "" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
	}
	if !strings.Contains(output, "Reranker: disabled by default") {
		t.Fatalf("setup output should report disabled default reranker:\n%s", output)
	}
	for _, want := range []string{
		"abra up --env-file",
		"abra doctor",
		"go run ./cmd/abra <command>",
		"Agent MCP and repo onboarding:",
		"cd /path/to/project",
		"abra agent bootstrap --agent <agent>   # initializes repo guidance, syncs if needed, and verifies",
		"fully restart the agent runtime",
		"abra agent verify . --scope <scope-from-abra-scope> --json",
		"agents use MCP working_memory_compose / brain_think with the verified scope",
		"optional operator check: abra ask",
		"abra agent status",
		"abra agent init --agent <agent>",
		"abra agent verify",
		"If an agent says Abra has no context",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("setup next steps missing %q:\n%s", want, output)
		}
	}
	if !strings.Contains(output, "abra sync . --code --scope <scope-from-abra-scope>") ||
		!strings.Contains(output, `optional operator check: abra ask "What should I know before changing this project?" --scope <scope-from-abra-scope>`) {
		t.Fatalf("setup next steps should defer scope until after cd and abra scope:\n%s", output)
	}
	verifyIndex := strings.Index(output, "abra agent verify . --scope <scope-from-abra-scope>")
	syncIndex := strings.Index(output, "abra sync . --code --scope <scope-from-abra-scope>")
	if verifyIndex < 0 || syncIndex < 0 || verifyIndex > syncIndex {
		t.Fatalf("setup manual path should verify before conditional sync:\n%s", output)
	}
	for _, want := range []string{"run `abra agent verify . --scope <scope-from-abra-scope> --json` first", "server_ready=true but agent_ready=false", "sync only if verify reports missing scope or empty source-backed memory"} {
		if !strings.Contains(output, want) {
			t.Fatalf("setup recovery guidance missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "rerun `abra scope`, ingest") {
		t.Fatalf("setup recovery should not lead with re-ingest:\n%s", output)
	}
}

func TestSetupProductionGuidesCompatibleProvider(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--production", "--no-start"}); err != nil {
			t.Fatalf("setup production error = %v", err)
		}
	})
	for _, want := range []string{
		"Production env created.",
		"abra config model compatible",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true",
		"abra up --env-file",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("production setup output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "abra config model local") {
		t.Fatalf("production setup should not recommend local model by default:\n%s", output)
	}
}

func TestConfigModelLocalPersistsRunnerControls(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{
		"config", "model", "local",
		"--runner-image", "registry.example/llama.cpp@sha256:abc123",
		"--pull-policy", "never",
		"--readiness-timeout", "45s",
	}); err != nil {
		t.Fatalf("config model local error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["ABRA_LOCAL_EMBEDDING_IMAGE"] != "registry.example/llama.cpp@sha256:abc123" {
		t.Fatalf("runner image = %q", values["ABRA_LOCAL_EMBEDDING_IMAGE"])
	}
	if values["ABRA_LOCAL_EMBEDDING_PULL_POLICY"] != "never" {
		t.Fatalf("pull policy = %q", values["ABRA_LOCAL_EMBEDDING_PULL_POLICY"])
	}
	if values["ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT"] != "45s" {
		t.Fatalf("readiness timeout = %q", values["ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "6" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "3000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
	}
}

func TestConfigModelLocalInfersCompatibleReranker(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"config", "model", "local",
			"--reranker-base-url", "http://localhost:9998/v1",
			"--reranker-model", "custom-reranker",
			"--reranker-api-key", "reranker-key",
			"--reranker-timeout", "45s",
		}); err != nil {
			t.Fatalf("config model local error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["RERANKER_PROVIDER"] != "compatible" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "http://host.docker.internal:9998/v1" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_MODEL"] != "custom-reranker" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
	}
	if values["RERANKER_API_KEY"] != "reranker-key" {
		t.Fatalf("reranker api key = %q", values["RERANKER_API_KEY"])
	}
	if values["RERANKER_TIMEOUT"] != "45s" {
		t.Fatalf("reranker timeout = %q", values["RERANKER_TIMEOUT"])
	}
	if !strings.Contains(output, "Reranker config updated: compatible custom-reranker") {
		t.Fatalf("config output missing reranker summary:\n%s", output)
	}
}

func TestSetupOpenAINonInteractiveRequiresAPIKey(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Chdir(root)

	err := run(context.Background(), []string{"setup", "--yes", "--openai", "--no-start"})
	if err == nil || !strings.Contains(err.Error(), "requires an API key") {
		t.Fatalf("setup --openai error = %v", err)
	}
}

func TestSetupOpenAINonInteractiveUsesEnvAPIKey(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Chdir(root)

	if err := run(context.Background(), []string{"setup", "--yes", "--openai", "--no-start"}); err != nil {
		t.Fatalf("setup --openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "compatible" || values["EMBEDDING_API_KEY"] != "test-openai-key" {
		t.Fatalf("openai values = %#v", values)
	}
}

func TestUpAutoStartsLocalModelsOnlyForLocalProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	localEnv := filepath.Join(home, "quickstart.env")
	mustWrite(t, localEnv, "EMBEDDING_PROVIDER=local\n")
	if !shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should start local models when provider is local")
	}
	mustWrite(t, localEnv, "EMBEDDING_PROVIDER=qwen3\n")
	if !shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should start local models when provider is qwen3 alias")
	}
	mustWrite(t, localEnv, "EMBEDDING_PROVIDER=local-smart\n")
	if !shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should start local models when provider is local-smart alias")
	}
	if shouldStartLocalModelsForUp(parseArgs([]string{"up", "--no-models"})) {
		t.Fatal("up --no-models should not start local models")
	}
	if shouldStartLocalModelsForUp(parseArgs([]string{"up", "--skip-models"})) {
		t.Fatal("up --skip-models should not start local models")
	}

	mustWrite(t, localEnv, "EMBEDDING_PROVIDER=compatible\n")
	if shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should not start local models for compatible providers")
	}

	mustWrite(t, localEnv, "NODE_ENV=production\nEMBEDDING_PROVIDER=local\nALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false\n")
	if shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should not auto-start local models in production without explicit local-embedding override")
	}
	if !productionLocalModelDisallowed(parseArgs([]string{"up"}), map[string]string{
		"NODE_ENV":                             "production",
		"EMBEDDING_PROVIDER":                   "local",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}) {
		t.Fatal("production local model config should be disallowed without explicit approval")
	}
	if productionLocalModelDisallowed(parseArgs([]string{"up", "--allow-production-local-embeddings"}), map[string]string{
		"NODE_ENV":                             "production",
		"EMBEDDING_PROVIDER":                   "local",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}) {
		t.Fatal("production local model config should respect explicit approval flag")
	}
	if !shouldStartLocalModelsForUp(parseArgs([]string{"up", "--allow-production-local-embeddings"})) {
		t.Fatal("up should start local models when production local embeddings are explicitly approved")
	}
	mustWrite(t, localEnv, "NODE_ENV=production\nEMBEDDING_PROVIDER=local\nALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true\n")
	if !shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should auto-start local models in production when local embeddings are explicitly allowed")
	}
}

func TestModelsCommandsRespectActiveCompatibleProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"EMBEDDING_PROVIDER=compatible",
		"EMBEDDING_BASE_URL=https://models.example.com/v1",
		"EMBEDDING_MODEL=embedding-model",
		"",
	}, "\n"))

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"models", "status"}); err != nil {
			t.Fatalf("models status error = %v", err)
		}
	})
	for _, want := range []string{
		"Local embeddings: inactive",
		"provider: compatible",
		"abra config",
		"model status --force",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("models status output missing %q:\n%s", want, output)
		}
	}

	err := run(context.Background(), []string{"models", "up"})
	if err == nil || !strings.Contains(err.Error(), "EMBEDDING_PROVIDER=compatible") {
		t.Fatalf("models up error = %v", err)
	}
}

func TestModelsCommandsTreatLocalAliasesAsActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"EMBEDDING_PROVIDER=qwen3",
		"EMBEDDING_BASE_URL=http://host.docker.internal:9999/v1",
		"EMBEDDING_MODEL=alias-model",
		"EMBEDDING_DIMENSIONS=1024",
		"",
	}, "\n"))

	if err := requireLocalModelProvider(parseArgs([]string{"models", "up"}), "up"); err != nil {
		t.Fatalf("qwen3 alias should be active local provider: %v", err)
	}
	if notice := inactiveLocalModelNotice(parseArgs([]string{"models", "status"})); notice != nil {
		t.Fatalf("qwen3 alias should not be inactive: %#v", notice)
	}
	cfg := embeddingRunner(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if cfg.Model != "alias-model" {
		t.Fatalf("runner model = %q", cfg.Model)
	}
	if cfg.BaseURL != "http://127.0.0.1:9999/v1" {
		t.Fatalf("runner base url = %q", cfg.BaseURL)
	}
}

func TestConfigModelLocalRewritesLoopbackForCompose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)

	if err := run(context.Background(), []string{"config", "model", "local", "--base-url", "http://localhost:8080/v1"}); err != nil {
		t.Fatalf("config model local error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:8080/v1" {
		t.Fatalf("embedding base url = %q", values["EMBEDDING_BASE_URL"])
	}
}

func TestSetupOpenAIStdinNoStart(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	stdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = stdin
		_ = reader.Close()
	})
	_, _ = writer.WriteString("openai-test-key\n")
	_ = writer.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--openai", "--api-key-stdin", "--no-start"}); err != nil {
			t.Fatalf("setup openai error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_BASE_URL"] != "https://api.openai.com/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_MODEL"] != "text-embedding-3-small" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_API_KEY"] != "openai-test-key" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
	if strings.Contains(output, "abra models up") {
		t.Fatalf("openai setup next steps should not suggest local models:\n%s", output)
	}
	if !strings.Contains(output, "verify your OpenAI embedding endpoint is reachable from Abra") {
		t.Fatalf("openai setup next steps should mention OpenAI endpoint readiness:\n%s", output)
	}
}

func TestSetupCompatibleNoStartDoesNotSuggestLocalModels(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--compatible", "--base-url", "http://localhost:9999/v1", "--embedding-model", "custom-embedding", "--dimensions", "768", "--api-key", "compatible-key", "--no-start"}); err != nil {
			t.Fatalf("setup compatible error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:9999/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_MODEL"] != "custom-embedding" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "16" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "6000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
	}
	if strings.Contains(output, "abra models up") {
		t.Fatalf("compatible setup next steps should not suggest local models:\n%s", output)
	}
	if !strings.Contains(output, "verify your compatible embedding endpoint is reachable") {
		t.Fatalf("compatible setup next steps should mention endpoint readiness:\n%s", output)
	}
	if !strings.Contains(output, "rewritten so Abra containers can reach the host service") {
		t.Fatalf("compatible loopback setup should explain host rewrite:\n%s", output)
	}
}

func TestSetupCompatibleConfiguresCustomReranker(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"setup",
			"--compatible",
			"--base-url", "https://models.example/v1",
			"--embedding-model", "custom-embedding",
			"--dimensions", "768",
			"--api-key", "provider-key",
			"--reranker-base-url", "http://localhost:9998/v1",
			"--reranker-model", "custom-reranker",
			"--no-start",
		}); err != nil {
			t.Fatalf("setup compatible error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["RERANKER_PROVIDER"] != "compatible" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "http://host.docker.internal:9998/v1" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_MODEL"] != "custom-reranker" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
	}
	if values["RERANKER_API_KEY"] != "provider-key" {
		t.Fatalf("reranker api key = %q", values["RERANKER_API_KEY"])
	}
	if values["RERANKER_TIMEOUT"] != "30s" {
		t.Fatalf("reranker timeout = %q", values["RERANKER_TIMEOUT"])
	}
	if !strings.Contains(output, "Reranker: compatible custom-reranker") {
		t.Fatalf("setup output missing reranker summary:\n%s", output)
	}
}

func TestSetupCompatibleNonInteractiveRequiresExplicitEndpointAndModel(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{"setup", "--compatible", "--yes", "--no-start"})
	if err == nil {
		t.Fatal("expected explicit endpoint error")
	}
	if !strings.Contains(err.Error(), "--embedding-base-url") || !strings.Contains(err.Error(), "--openai") {
		t.Fatalf("error = %v", err)
	}

	err = run(context.Background(), []string{"setup", "--compatible", "--yes", "--embedding-base-url", "http://localhost:9999/v1", "--no-start"})
	if err == nil {
		t.Fatal("expected explicit model error")
	}
	if !strings.Contains(err.Error(), "--embedding-model") || !strings.Contains(err.Error(), "--openai") {
		t.Fatalf("error = %v", err)
	}
}

func TestSetupRejectsCustomHTTPProviderSelector(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{"setup", "--provider", "custom-http", "--yes", "--no-start"})
	if err == nil {
		t.Fatal("expected unsupported setup provider error")
	}
	if !strings.Contains(err.Error(), `unknown setup embedding provider "custom-http"`) || !strings.Contains(err.Error(), "use local, compatible, or openai") {
		t.Fatalf("error = %v", err)
	}
}

func TestSetupRejectsConflictingProviders(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{"setup", "--local", "--openai", "--no-start"})
	if err == nil {
		t.Fatal("expected conflicting provider error")
	}
	if !strings.Contains(err.Error(), "choose one embedding provider only") {
		t.Fatalf("error = %v", err)
	}
}

func TestSetupOpenAIModelAliasIsEmbeddingModel(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	stdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = stdin
		_ = reader.Close()
	})
	_, _ = writer.WriteString("openai-test-key\n")
	_ = writer.Close()

	if err := run(context.Background(), []string{"setup", "--openai", "--model", "custom-embedding", "--dimensions", "2048", "--api-key-stdin", "--no-start"}); err != nil {
		t.Fatalf("setup openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_MODEL"] != "custom-embedding" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "2048" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
}

func TestUICommandRemoved(t *testing.T) {
	for _, command := range []string{"ui", "dashboard"} {
		t.Run(command, func(t *testing.T) {
			err := run(context.Background(), []string{command})
			if err == nil {
				t.Fatal("expected removed command error")
			}
			if !strings.Contains(err.Error(), "was removed") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	help := commandUsage("ui")
	if strings.Contains(help, "cockpit") {
		t.Fatalf("ui help still describes cockpit: %s", help)
	}
	if !strings.Contains(help, "removed") {
		t.Fatalf("ui help should explain removal: %s", help)
	}
}

func TestConfigModelCompatibleUpdatesEnv(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "https://models.example/v1",
		"--api-key", "secret-model-key",
		"--model", "embed-1536",
		"--dimensions", "1536",
	})
	if err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "compatible" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["EMBEDDING_BASE_URL"] != "https://models.example/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_API_KEY"] != "secret-model-key" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
	if values["EMBEDDING_MODEL"] != "embed-1536" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "1536" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "false" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	}
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "4" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "16" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "6000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
	}
}

func TestConfigModelCompatibleInfersKnownDimensions(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "http://localhost:9999/v1",
		"--model", defaultServedModelName,
	})
	if err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_DIMENSIONS"] != "1024" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
}

func TestConfigModelCompatibleRequiresDimensionsForUnknownModel(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "http://localhost:9999/v1",
		"--model", "custom-embedding",
	})
	if err == nil {
		t.Fatal("expected dimensions error")
	}
	if !strings.Contains(err.Error(), "embedding dimensions are required") || !strings.Contains(err.Error(), "--dimensions") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigModelRejectsCustomHTTPSelector(t *testing.T) {
	err := run(context.Background(), []string{
		"config",
		"model",
		"custom-http",
		"--base-url", "https://provider.example/embed",
		"--model", "custom-model",
		"--dimensions", "1024",
	})
	if err == nil {
		t.Fatal("expected unsupported model config error")
	}
	if !strings.Contains(err.Error(), `unknown model config "custom-http"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigModelCompatibleAllowsNoAPIKey(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "http://localhost:9999/v1",
		"--model", "custom-embed",
		"--dimensions", "768",
	})
	if err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_API_KEY"] != "" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "16" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "6000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "768" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
	if values["EMBEDDING_TIMEOUT"] != "30s" {
		t.Fatalf("timeout = %q", values["EMBEDDING_TIMEOUT"])
	}
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "4" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:9999/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
}

func TestConfigModelCompatibleConfiguresCustomReranker(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"config",
			"model",
			"compatible",
			"--base-url", "https://models.example/v1",
			"--api-key", "embedding-key",
			"--model", "custom-embed",
			"--dimensions", "768",
			"--reranker-base-url", "http://localhost:9998/v1",
			"--reranker-model", "custom-reranker",
			"--reranker-api-key", "reranker-key",
			"--reranker-timeout", "45s",
		})
		if err != nil {
			t.Fatalf("config model compatible error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "compatible" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["RERANKER_PROVIDER"] != "compatible" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "http://host.docker.internal:9998/v1" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_API_KEY"] != "reranker-key" {
		t.Fatalf("reranker api key = %q", values["RERANKER_API_KEY"])
	}
	if values["RERANKER_MODEL"] != "custom-reranker" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
	}
	if values["RERANKER_TIMEOUT"] != "45s" {
		t.Fatalf("reranker timeout = %q", values["RERANKER_TIMEOUT"])
	}
	if !strings.Contains(output, "Reranker config updated: compatible custom-reranker") {
		t.Fatalf("config output missing reranker summary:\n%s", output)
	}
}

func TestConfigModelCompatibleRejectsIncompleteReranker(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "https://models.example/v1",
		"--model", "custom-embed",
		"--dimensions", "768",
		"--reranker-provider", "compatible",
	})
	if err == nil {
		t.Fatal("expected incomplete reranker error")
	}
	if !strings.Contains(err.Error(), "--reranker-base-url") || !strings.Contains(err.Error(), "--reranker-model") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigModelOpenAIDefaults(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	stdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = stdin
		_ = reader.Close()
	})
	_, _ = writer.WriteString("openai-test-key\n")
	_ = writer.Close()

	err = run(context.Background(), []string{
		"config",
		"model",
		"openai",
		"--api-key-stdin",
	})
	if err != nil {
		t.Fatalf("config model openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_BASE_URL"] != "https://api.openai.com/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_MODEL"] != "text-embedding-3-small" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "1536" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
	if values["EMBEDDING_API_KEY"] != "openai-test-key" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
}

func TestConfigModelOpenAIInfersLargeDimensions(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"openai",
		"--api-key", "openai-test-key",
		"--model", "text-embedding-3-large",
	})
	if err != nil {
		t.Fatalf("config model openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_MODEL"] != "text-embedding-3-large" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "3072" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
}

func TestSetupOpenAIInfersLargeDimensions(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{
		"setup",
		"--openai",
		"--embedding-model", "text-embedding-3-large",
		"--api-key", "openai-test-key",
		"--no-start",
	}); err != nil {
		t.Fatalf("setup openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_MODEL"] != "text-embedding-3-large" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "3072" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
}

func TestConfigModelLocalRestoresQwenDefaults(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{
		"config", "model", "compatible",
		"--base-url", "https://models.example/v1",
		"--api-key", "secret-model-key",
		"--model", "embed-1536",
		"--dimensions", "1536",
	}); err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	if err := run(context.Background(), []string{"config", "model", "local"}); err != nil {
		t.Fatalf("config model local error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "local" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:8080/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_API_KEY"] != "" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
	if values["EMBEDDING_TIMEOUT"] != "10m" {
		t.Fatalf("timeout = %q", values["EMBEDDING_TIMEOUT"])
	}
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "1" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
	}
	if values["RERANKER_PROVIDER"] != "none" || values["RERANKER_BASE_URL"] != "" {
		t.Fatalf("reranker fields = provider %q base %q", values["RERANKER_PROVIDER"], values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_MODEL"] != "" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "false" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	}
}

func TestCLITimeoutParsesDurationAndSeconds(t *testing.T) {
	if got := cliTimeout(cliArgs{Flags: map[string]string{"timeout": "10m"}, Bools: map[string]bool{}}, time.Second); got != 10*time.Minute {
		t.Fatalf("duration timeout = %s", got)
	}
	if got := cliTimeout(cliArgs{Flags: map[string]string{"timeout": "45"}, Bools: map[string]bool{}}, time.Second); got != 45*time.Second {
		t.Fatalf("seconds timeout = %s", got)
	}
}

func TestDoJSONRejectsOversizedResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxCLIResponseBody+1))
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doJSON(req, time.Second); err == nil || !strings.Contains(err.Error(), "response body exceeded") {
		t.Fatalf("error = %v, want response body exceeded", err)
	}
}

func TestEmbeddingRunnerUsesLocalQwenDefaults(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"config", "model", "local"}); err != nil {
		t.Fatalf("config model local error = %v", err)
	}
	cfg := embeddingRunner(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if cfg.ModelID != defaultEmbeddingModelID {
		t.Fatalf("model id = %q", cfg.ModelID)
	}
	if cfg.Model != defaultServedModelName {
		t.Fatalf("served model = %q", cfg.Model)
	}
	if cfg.BaseURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("base url = %q", cfg.BaseURL)
	}
	if cfg.Port != "8080" {
		t.Fatalf("port = %q", cfg.Port)
	}
	if cfg.Publish != "127.0.0.1" {
		t.Fatalf("publish = %q", cfg.Publish)
	}
	if got := localRunnerPublish(cfg); got != "127.0.0.1:8080" {
		t.Fatalf("publish binding = %q", got)
	}
	if cfg.Dims != 1024 {
		t.Fatalf("dims = %d", cfg.Dims)
	}
	if cfg.PullPolicy != "missing" {
		t.Fatalf("pull policy = %q", cfg.PullPolicy)
	}
	if cfg.ReadinessTimeout != 10*time.Second {
		t.Fatalf("readiness timeout = %s", cfg.ReadinessTimeout)
	}
	wantImage := "ghcr.io/ggml-org/llama.cpp:server"
	if cfg.Image != wantImage {
		t.Fatalf("image = %q, want %q", cfg.Image, wantImage)
	}
}

func TestEmbeddingRunnerUsesImageAndReadinessEnv(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"models", "status"})
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                     "local",
		"ABRA_LOCAL_EMBEDDING_IMAGE":             "registry.example/llama.cpp@sha256:abc123",
		"ABRA_LOCAL_EMBEDDING_PULL_POLICY":       "never",
		"ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT": "45s",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	cfg := embeddingRunner(args)
	if cfg.Image != "registry.example/llama.cpp@sha256:abc123" {
		t.Fatalf("image = %q", cfg.Image)
	}
	if cfg.PullPolicy != "never" {
		t.Fatalf("pull policy = %q", cfg.PullPolicy)
	}
	if cfg.ReadinessTimeout != 45*time.Second {
		t.Fatalf("readiness timeout = %s", cfg.ReadinessTimeout)
	}
}

func TestLocalRunnerStartupTimeoutUsesFlagAndEnv(t *testing.T) {
	t.Setenv("ABRA_LOCAL_MODEL_STARTUP_TIMEOUT", "3m")
	if got := localRunnerStartupTimeout(parseArgs([]string{"models", "up"})); got != 3*time.Minute {
		t.Fatalf("startup timeout from env = %s", got)
	}
	if got := localRunnerStartupTimeout(parseArgs([]string{"models", "up", "--startup-timeout", "45"})); got != 45*time.Second {
		t.Fatalf("startup timeout from seconds flag = %s", got)
	}
	if got := localRunnerStartupTimeout(parseArgs([]string{"models", "up", "--startup-timeout", "2m"})); got != 2*time.Minute {
		t.Fatalf("startup timeout from duration flag = %s", got)
	}
	if got := localRunnerStartupTimeout(parseArgs([]string{"models", "up", "--startup-timeout", "0s"})); got != 10*time.Minute {
		t.Fatalf("invalid startup timeout fallback = %s", got)
	}
}

func TestProductionLocalRunnerRequiresDigestPinnedImage(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init", "--production"}); err != nil {
		t.Fatalf("init production error = %v", err)
	}
	args := parseArgs([]string{"models", "up"})
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "local",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
		"ABRA_LOCAL_EMBEDDING_IMAGE":           "ghcr.io/ggml-org/llama.cpp:server",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	if err := validateLocalRunnerImagePolicy(args, embeddingRunner(args)); err == nil || !strings.Contains(err.Error(), "explicit operator approval") {
		t.Fatalf("allow policy error = %v", err)
	}
	if err := updateEnvValues(args, map[string]string{
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "true",
	}); err != nil {
		t.Fatalf("update allow env error = %v", err)
	}
	if err := validateLocalRunnerImagePolicy(args, embeddingRunner(args)); err == nil || !strings.Contains(err.Error(), "digest-pinned") {
		t.Fatalf("policy error = %v", err)
	}
	if err := updateEnvValues(args, map[string]string{
		"ABRA_LOCAL_EMBEDDING_IMAGE": "registry.example/llama.cpp@sha256:abc123",
	}); err != nil {
		t.Fatalf("update digest image error = %v", err)
	}
	if err := validateLocalRunnerImagePolicy(args, embeddingRunner(args)); err != nil {
		t.Fatalf("digest image policy error = %v", err)
	}
	if err := updateEnvValues(args, map[string]string{
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}); err != nil {
		t.Fatalf("update disallow env error = %v", err)
	}
	allowedArgs := parseArgs([]string{"models", "up", "--allow-production-local-embeddings"})
	if err := validateLocalRunnerImagePolicy(allowedArgs, embeddingRunner(allowedArgs)); err != nil {
		t.Fatalf("cli allow policy error = %v", err)
	}
}

func TestDownStopsLocalModelsByDefault(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"down"})
	if !shouldStopLocalModelsForDown(args) {
		t.Fatal("down should stop local models for local provider")
	}
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER": "qwen3",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	if !shouldStopLocalModelsForDown(args) {
		t.Fatal("down should stop local models for qwen3 alias")
	}
	keep := parseArgs([]string{"down", "--keep-models"})
	if shouldStopLocalModelsForDown(keep) {
		t.Fatal("down --keep-models should not stop local models")
	}
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER": "compatible",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	if shouldStopLocalModelsForDown(args) {
		t.Fatal("down should not stop models by default for compatible provider")
	}
	forced := parseArgs([]string{"down", "--models"})
	if !shouldStopLocalModelsForDown(forced) {
		t.Fatal("down --models should force model stop")
	}
}

func TestSyncLocalRunnerEnvNormalizesLocalAliases(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"models", "up"})
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "local-smart",
		"EMBEDDING_BASE_URL":                   "http://host.docker.internal:9999/v1",
		"EMBEDDING_MODEL":                      "alias-model",
		"EMBEDDING_DIMENSIONS":                 "1024",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "true",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	if err := syncLocalRunnerEnv(args); err != nil {
		t.Fatalf("sync local runner env error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "local" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["EMBEDDING_MODEL"] != "alias-model" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "true" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	}
}

func TestSyncLocalRunnerEnvCanExplicitlyAllowProductionLocalEmbeddings(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"models", "up", "--allow-production-local-embeddings"})
	if err := syncLocalRunnerEnv(args); err != nil {
		t.Fatalf("sync local runner env error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "true" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	}
}

func TestEmbeddingRunnerIgnoresCompatibleProviderConfig(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{
		"config", "model", "compatible",
		"--base-url", "https://models.example/v1",
		"--model", "embed-3072",
		"--dimensions", "3072",
	}); err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	cfg := embeddingRunner(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if cfg.BaseURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("base url = %q", cfg.BaseURL)
	}
	if cfg.Port != "8080" {
		t.Fatalf("port = %q", cfg.Port)
	}
	if cfg.Model != defaultServedModelName {
		t.Fatalf("model = %q", cfg.Model)
	}
	if cfg.Dims != 1024 {
		t.Fatalf("dims = %d", cfg.Dims)
	}
}

func TestLocalRunnerConfigHashTracksRunnerFields(t *testing.T) {
	cfg := embeddingRunnerConfig{
		Container: defaultEmbeddingContainer,
		Image:     defaultTEIImage(),
		ModelID:   defaultEmbeddingModelID,
		Model:     defaultServedModelName,
		BaseURL:   "http://127.0.0.1:8080/v1",
		Publish:   defaultEmbeddingPublish,
		Port:      "8080",
		CacheDir:  "/tmp/abra-model-cache",
		Dims:      1024,
	}
	base := localRunnerConfigHash(cfg)
	if base == "" {
		t.Fatal("empty config hash")
	}
	changedDims := cfg
	changedDims.Dims = 2048
	if localRunnerConfigHash(changedDims) == base {
		t.Fatal("hash did not change after dimensions changed")
	}
	changedModel := cfg
	changedModel.ModelID = "example/model:Q4_K_M"
	if localRunnerConfigHash(changedModel) == base {
		t.Fatal("hash did not change after model id changed")
	}
	changedPublish := cfg
	changedPublish.Publish = ""
	if localRunnerConfigHash(changedPublish) == base {
		t.Fatal("hash did not change after publish address changed")
	}
}

func TestSyncLocalRunnerEnvUsesSelectedPort(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"models", "up", "--port", "9090"})
	args.Rest = []string{}
	if err := syncLocalRunnerEnv(args); err != nil {
		t.Fatalf("sync local env error = %v", err)
	}
	values, err := readEnvValues(envPath(args))
	if err != nil {
		t.Fatal(err)
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:9090/v1" {
		t.Fatalf("EMBEDDING_BASE_URL = %q", values["EMBEDDING_BASE_URL"])
	}
}

func TestFriendlyProviderErrorAddsModelsHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "EMBEDDING_PROVIDER=local\n")

	err := friendlyProviderError(&httpStatusError{
		Code: 400,
		Body: `{"error":"ai provider request failed: Post \"http://host.docker.internal:8080/v1/embeddings\": dial tcp: connect: connection refused"}`,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "abra model up") {
		t.Fatalf("error = %v", err)
	}
}

func TestFriendlyProviderErrorUsesStructuredPayload(t *testing.T) {
	err := friendlyProviderError(&httpStatusError{
		Code: 401,
		Body: `{"error_kind":"provider_error"}`,
		Payload: map[string]any{
			"error_kind": "provider_error",
			"provider_error": map[string]any{
				"code":        "auth_failed",
				"status_code": float64(401),
				"retryable":   false,
				"message":     "missing authentication header",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "auth_failed") || !strings.Contains(err.Error(), "base URL, model, and dimensions") {
		t.Fatalf("error = %v", err)
	}
}
