package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type fakeDocker struct {
	t     *testing.T
	root  string
	state string
	log   string
}

type fakeDockerCall struct {
	Dir  string   `json:"dir"`
	Args []string `json:"args"`
}

type fakeDockerState struct {
	Exists bool              `json:"exists"`
	Image  string            `json:"image"`
	Labels map[string]string `json:"labels"`
	Logs   string            `json:"logs"`
}

func newFakeDocker(t *testing.T) *fakeDocker {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	state := filepath.Join(root, "state.json")
	log := filepath.Join(root, "docker.jsonl")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(bin, "docker"), fakeDockerScript())
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeDockerState(t, state, fakeDockerState{Labels: map[string]string{}})
	t.Setenv("ABRA_FAKE_DOCKER_STATE", state)
	t.Setenv("ABRA_FAKE_DOCKER_LOG", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &fakeDocker{t: t, root: root, state: state, log: log}
}

func (f *fakeDocker) seedContainer(image string, labels map[string]string) {
	f.t.Helper()
	if labels == nil {
		labels = map[string]string{}
	}
	writeFakeDockerState(f.t, f.state, fakeDockerState{
		Exists: true,
		Image:  image,
		Labels: labels,
	})
}

func (f *fakeDocker) writeLogs(text string) {
	f.t.Helper()
	state := readFakeDockerState(f.t, f.state)
	state.Logs = text
	writeFakeDockerState(f.t, f.state, state)
}

func (f *fakeDocker) calls() []fakeDockerCall {
	f.t.Helper()
	file, err := os.Open(f.log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		f.t.Fatal(err)
	}
	defer file.Close()
	var calls []fakeDockerCall
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 2)
		if len(parts) != 2 {
			f.t.Fatalf("decode fake docker call: %q", scanner.Text())
		}
		calls = append(calls, fakeDockerCall{Dir: parts[0], Args: strings.Fields(parts[1])})
	}
	if err := scanner.Err(); err != nil {
		f.t.Fatal(err)
	}
	return calls
}

func fakeDockerScript() string {
	return `#!/bin/sh
set -eu
state="${ABRA_FAKE_DOCKER_STATE:?}"
log="${ABRA_FAKE_DOCKER_LOG:?}"
mkdir -p "$state" "$(dirname "$log")"
printf '%s\t%s\n' "$PWD" "$*" >> "$log"
exists="$(cat "$state/exists" 2>/dev/null || printf 0)"
cmd="${1:-}"
if [ "$#" -gt 0 ]; then
  shift
fi
case "$cmd" in
  container)
    sub="${1:-}"
    if [ "$sub" != "inspect" ] || [ "$exists" != "1" ]; then
      exit 1
    fi
    format=""
    previous=""
    for arg in "$@"; do
      if [ "$previous" = "--format" ]; then
        format="$arg"
        break
      fi
      previous="$arg"
    done
    if [ "$format" = "{{.Config.Image}}" ]; then
      cat "$state/image"
      printf '\n'
      exit 0
    fi
    case "$format" in
      *Config.Labels*)
        label="$(printf '%s\n' "$format" | sed -n 's/.*"\([^"]*\)".*/\1/p')"
        value="$(grep -F "$label=" "$state/labels" 2>/dev/null | tail -n 1 | cut -d= -f2- || true)"
        if [ -n "$value" ]; then
          printf '%s\n' "$value"
        else
          printf '<no value>\n'
        fi
        ;;
    esac
    ;;
  run)
    printf 1 > "$state/exists"
    : > "$state/labels"
    image=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --name|--pull|-p|-v)
          shift 2
          ;;
        --label)
          printf '%s\n' "$2" >> "$state/labels"
          shift 2
          ;;
        -d)
          shift
          ;;
        -*)
          if [ "$#" -gt 1 ]; then
            case "$2" in
              -*) shift ;;
              *) shift 2 ;;
            esac
          else
            shift
          fi
          ;;
        *)
          image="$1"
          break
          ;;
      esac
    done
    if [ -z "$image" ]; then
      exit 1
    fi
    printf '%s' "$image" > "$state/image"
    printf 'fake-container-id\n'
    ;;
  start)
    if [ "$exists" != "1" ]; then
      exit 1
    fi
    printf '%s\n' "${1:-}"
    ;;
  rm)
    printf 0 > "$state/exists"
    if [ "${1:-}" = "-f" ]; then
      printf '%s\n' "${2:-}"
    else
      printf '%s\n' "${1:-}"
    fi
    ;;
  logs)
    if [ "$exists" != "1" ]; then
      exit 1
    fi
    cat "$state/logs" 2>/dev/null || true
    ;;
  *)
    exit 1
    ;;
esac
`
}

func readFakeDockerState(t *testing.T, path string) fakeDockerState {
	t.Helper()
	state, err := loadFakeDockerState(path)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func loadFakeDockerState(path string) (fakeDockerState, error) {
	exists, _ := os.ReadFile(filepath.Join(path, "exists"))
	image, _ := os.ReadFile(filepath.Join(path, "image"))
	logs, _ := os.ReadFile(filepath.Join(path, "logs"))
	state := fakeDockerState{
		Exists: strings.TrimSpace(string(exists)) == "1",
		Image:  strings.TrimSpace(string(image)),
		Logs:   string(logs),
		Labels: map[string]string{},
	}
	rawLabels, _ := os.ReadFile(filepath.Join(path, "labels"))
	for _, line := range strings.Split(string(rawLabels), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			state.Labels[key] = value
		}
	}
	if state.Labels == nil {
		state.Labels = map[string]string{}
	}
	return state, nil
}

func writeFakeDockerState(t *testing.T, path string, state fakeDockerState) {
	t.Helper()
	if err := saveFakeDockerState(path, state); err != nil {
		t.Fatal(err)
	}
}

func saveFakeDockerState(path string, state fakeDockerState) error {
	if state.Labels == nil {
		state.Labels = map[string]string{}
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	exists := "0"
	if state.Exists {
		exists = "1"
	}
	if err := os.WriteFile(filepath.Join(path, "exists"), []byte(exists), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "image"), []byte(state.Image), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "logs"), []byte(state.Logs), 0o644); err != nil {
		return err
	}
	var labels strings.Builder
	for key, value := range state.Labels {
		labels.WriteString(key)
		labels.WriteString("=")
		labels.WriteString(value)
		labels.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(path, "labels"), []byte(labels.String()), 0o644)
}

func newEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings" {
			t.Fatalf("embedding request = %s %s", r.Method, r.URL.Path)
		}
		writeTestJSON(t, w, map[string]any{"data": []map[string]any{{"embedding": []float64{0.1}}}})
	}))
}

func setupModelsLifecycleTest(t *testing.T) (string, *httptest.Server, *fakeDocker) {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"EMBEDDING_PROVIDER=local",
		"EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1",
		"EMBEDDING_MODEL=" + defaultServedModelName,
		"EMBEDDING_DIMENSIONS=1024",
		"EMBEDDING_TIMEOUT=10m",
		"",
	}, "\n"))
	server := newEmbeddingServer(t)
	t.Cleanup(server.Close)
	docker := newFakeDocker(t)
	return home, server, docker
}

func TestModelsUpCreatesContainerAndSyncsEnv(t *testing.T) {
	home, server, docker := setupModelsLifecycleTest(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")

	var runErr error
	output := captureStdout(t, func() {
		runErr = run(context.Background(), []string{
			"models", "up",
			"--base-url", server.URL + "/v1",
			"--cache-dir", cacheDir,
		})
	})
	if runErr != nil {
		t.Fatalf("models up error = %v", runErr)
	}
	if !strings.Contains(output, "Local embeddings ready") {
		t.Fatalf("models up output missing readiness:\n%s", output)
	}
	calls := docker.calls()
	runCall := findFakeDockerCall(t, calls, "run")
	wantPort := portFromBaseURL(server.URL + "/v1")
	for _, want := range []string{
		"--name", defaultEmbeddingContainer,
		"--pull", "missing",
		"--label", localRunnerModelLabel + "=" + defaultEmbeddingModelID,
		"--label", localRunnerDimsLabel + "=1024",
		"-p", "127.0.0.1:" + wantPort + ":8080",
		"-v", cacheDir + ":/root/.cache/huggingface",
		defaultTEIImage(),
		"-hf", defaultEmbeddingModelID,
		"--embedding",
		"--pooling", "last",
		"--ctx-size", "32768",
	} {
		if !slices.Contains(runCall.Args, want) {
			t.Fatalf("docker run missing %q:\n%v", want, runCall.Args)
		}
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatal(err)
	}
	wantBaseURL := "http://host.docker.internal:" + wantPort + "/v1"
	if values["EMBEDDING_BASE_URL"] != wantBaseURL {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
}

func TestModelsUpStartsExistingMatchingContainer(t *testing.T) {
	_, server, docker := setupModelsLifecycleTest(t)
	args := parseArgs([]string{"models", "up", "--base-url", server.URL + "/v1"})
	cfg := embeddingRunner(args)
	docker.seedContainer(cfg.Image, map[string]string{localRunnerHashLabel: localRunnerConfigHash(cfg)})

	if err := modelsUp(context.Background(), args); err != nil {
		t.Fatalf("models up error = %v", err)
	}
	calls := docker.calls()
	if hasFakeDockerCall(calls, "run") || hasFakeDockerCall(calls, "rm") {
		t.Fatalf("existing matching container should not run/rm: %#v", calls)
	}
	start := findFakeDockerCall(t, calls, "start")
	if !slices.Contains(start.Args, defaultEmbeddingContainer) {
		t.Fatalf("docker start args = %v", start.Args)
	}
}

func TestModelsUpRecreatesContainerWhenConfigHashDiffers(t *testing.T) {
	_, server, docker := setupModelsLifecycleTest(t)
	docker.seedContainer(defaultTEIImage(), map[string]string{localRunnerHashLabel: "stale"})

	if err := run(context.Background(), []string{"models", "up", "--base-url", server.URL + "/v1"}); err != nil {
		t.Fatalf("models up error = %v", err)
	}
	calls := docker.calls()
	rmIndex := fakeDockerCallIndex(calls, "rm")
	runIndex := fakeDockerCallIndex(calls, "run")
	if rmIndex < 0 || runIndex < 0 || rmIndex > runIndex {
		t.Fatalf("expected rm before run, calls=%#v", calls)
	}
}

func TestModelsStatusJSONIncludesDockerConfigMatch(t *testing.T) {
	_, server, docker := setupModelsLifecycleTest(t)
	args := parseArgs([]string{"models", "status", "--json", "--base-url", server.URL + "/v1"})
	cfg := embeddingRunner(args)
	docker.seedContainer(cfg.Image, map[string]string{localRunnerHashLabel: localRunnerConfigHash(cfg)})

	var statusErr error
	output := captureStdout(t, func() {
		statusErr = modelsStatus(context.Background(), args)
	})
	if statusErr != nil {
		t.Fatalf("models status error = %v", statusErr)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("decode status json: %v\n%s", err, output)
	}
	if status["ready"] != true || status["config_matches"] != true {
		t.Fatalf("status = %#v", status)
	}
	if status["expected_config_hash"] == "" || status["container_config_hash"] == "" {
		t.Fatalf("missing config hashes: %#v", status)
	}
	if hasFakeDockerCall(docker.calls(), "run") || hasFakeDockerCall(docker.calls(), "rm") {
		t.Fatalf("status should not mutate docker: %#v", docker.calls())
	}
}

func TestModelsLogsUsesTailAndStreamsOutput(t *testing.T) {
	_, _, docker := setupModelsLifecycleTest(t)
	docker.seedContainer(defaultTEIImage(), map[string]string{})
	docker.writeLogs("model ready\n")

	var logsErr error
	output := captureStdout(t, func() {
		logsErr = run(context.Background(), []string{"models", "logs", "--tail", "7"})
	})
	if logsErr != nil {
		t.Fatalf("models logs error = %v", logsErr)
	}
	if !strings.Contains(output, "model ready") {
		t.Fatalf("logs output = %q", output)
	}
	call := findFakeDockerCall(t, docker.calls(), "logs")
	if strings.Join(call.Args, " ") != "logs --tail 7 "+defaultEmbeddingContainer {
		t.Fatalf("logs call = %v", call.Args)
	}
}

func TestModelsDownRemovesPresentContainerAndNoopsWhenAbsent(t *testing.T) {
	_, _, docker := setupModelsLifecycleTest(t)
	docker.seedContainer(defaultTEIImage(), map[string]string{})
	var downErr error
	output := captureStdout(t, func() {
		downErr = run(context.Background(), []string{"models", "down"})
	})
	if downErr != nil {
		t.Fatalf("models down error = %v", downErr)
	}
	if !strings.Contains(output, "Stopped local embedding container") {
		t.Fatalf("down output = %q", output)
	}
	if call := findFakeDockerCall(t, docker.calls(), "rm"); strings.Join(call.Args, " ") != "rm -f "+defaultEmbeddingContainer {
		t.Fatalf("rm call = %v", call.Args)
	}

	downErr = nil
	output = captureStdout(t, func() {
		downErr = run(context.Background(), []string{"models", "down"})
	})
	if downErr != nil {
		t.Fatalf("models down absent error = %v", downErr)
	}
	if !strings.Contains(output, "Local embedding container is not present") {
		t.Fatalf("down absent output = %q", output)
	}
}

func findFakeDockerCall(t *testing.T, calls []fakeDockerCall, command string) fakeDockerCall {
	t.Helper()
	for _, call := range calls {
		if len(call.Args) > 0 && call.Args[0] == command {
			return call
		}
	}
	t.Fatalf("missing docker %s call in %#v", command, calls)
	return fakeDockerCall{}
}

func hasFakeDockerCall(calls []fakeDockerCall, command string) bool {
	return fakeDockerCallIndex(calls, command) >= 0
}

func fakeDockerCallIndex(calls []fakeDockerCall, command string) int {
	for i, call := range calls {
		if len(call.Args) > 0 && call.Args[0] == command {
			return i
		}
	}
	return -1
}
