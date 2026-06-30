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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
	Exists    bool              `json:"exists"`
	Status    string            `json:"status"`
	Running   bool              `json:"running"`
	ExitCode  int               `json:"exit_code"`
	Error     string            `json:"error"`
	OOMKilled bool              `json:"oom_killed"`
	Image     string            `json:"image"`
	Labels    map[string]string `json:"labels"`
	Logs      string            `json:"logs"`
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
	writeFakeDockerState(t, state, defaultEmbeddingContainer, fakeDockerState{Labels: map[string]string{}})
	t.Setenv("ABRA_FAKE_DOCKER_STATE", state)
	t.Setenv("ABRA_FAKE_DOCKER_LOG", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &fakeDocker{t: t, root: root, state: state, log: log}
}

func (f *fakeDocker) seedContainer(image string, labels map[string]string) {
	f.seedNamedContainer(defaultEmbeddingContainer, image, labels)
}

func (f *fakeDocker) seedNamedContainer(name, image string, labels map[string]string) {
	f.t.Helper()
	if labels == nil {
		labels = map[string]string{}
	}
	writeFakeDockerState(f.t, f.state, name, fakeDockerState{
		Exists:  true,
		Status:  "running",
		Running: true,
		Image:   image,
		Labels:  labels,
	})
}

func (f *fakeDocker) writeLogs(text string) {
	f.t.Helper()
	state := readFakeDockerState(f.t, f.state)
	state.Logs = text
	writeFakeDockerState(f.t, f.state, defaultEmbeddingContainer, state)
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
mkdir -p "$state/containers"
container_dir() {
  printf '%s/containers/%s' "$state" "$1"
}
container_exists() {
  if [ -f "$(container_dir "$1")/exists" ]; then
    cat "$(container_dir "$1")/exists"
  else
    printf 0
  fi
}
container_running() {
  if [ -f "$(container_dir "$1")/running" ]; then
    cat "$(container_dir "$1")/running"
  else
    container_exists "$1"
  fi
}
cmd="${1:-}"
if [ "$#" -gt 0 ]; then
  shift
fi
case "$cmd" in
  info)
    if [ "${1:-}" = "--format" ]; then
      printf 'fake-docker\n'
      exit 0
    fi
    printf 'ServerVersion: fake-docker\n'
    ;;
  container)
    sub="${1:-}"
    if [ "$#" -gt 0 ]; then
      shift
    fi
    format=""
    previous=""
    name=""
    for arg in "$@"; do
      if [ "$previous" = "--format" ]; then
        format="$arg"
        previous=""
        continue
      fi
      if [ "$arg" = "--format" ]; then
        previous="--format"
        continue
      fi
      name="$arg"
    done
    if [ "$sub" != "inspect" ] || [ -z "$name" ] || [ "$(container_exists "$name")" != "1" ]; then
      exit 1
    fi
    dir="$(container_dir "$name")"
    if [ "$format" = "{{.Config.Image}}" ]; then
      cat "$dir/image"
      printf '\n'
      exit 0
    fi
    if [ "$format" = "{{.State.Running}}" ]; then
      if [ "$(container_running "$name")" = "1" ]; then
        printf 'true\n'
      else
        printf 'false\n'
      fi
      exit 0
    fi
    if [ "$format" = "{{json .State}}" ]; then
      status="$(cat "$dir/status" 2>/dev/null || true)"
      if [ -z "$status" ]; then
        if [ "$(container_running "$name")" = "1" ]; then
          status="running"
        else
          status="exited"
        fi
      fi
      running=false
      if [ "$(container_running "$name")" = "1" ]; then
        running=true
      fi
      exit_code="$(cat "$dir/exit_code" 2>/dev/null || printf 0)"
      error="$(cat "$dir/error" 2>/dev/null || true)"
      oom_killed=false
      if [ "$(cat "$dir/oom_killed" 2>/dev/null || printf 0)" = "1" ]; then
        oom_killed=true
      fi
      printf '{"Status":"%s","Running":%s,"ExitCode":%s,"Error":"%s","OOMKilled":%s}\n' "$status" "$running" "$exit_code" "$error" "$oom_killed"
      exit 0
    fi
    case "$format" in
      *Config.Labels*)
        label="$(printf '%s\n' "$format" | sed -n 's/.*"\([^"]*\)".*/\1/p')"
        value="$(grep -F "$label=" "$dir/labels" 2>/dev/null | tail -n 1 | cut -d= -f2- || true)"
        if [ -n "$value" ]; then
          printf '%s\n' "$value"
        else
          printf '<no value>\n'
        fi
        ;;
    esac
    ;;
  run)
    image=""
    name=""
    labels_file="$(mktemp)"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --name)
          name="$2"
          shift 2
          ;;
        --pull|-p|-v)
          shift 2
          ;;
        --label)
          printf '%s\n' "$2" >> "$labels_file"
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
    if [ -z "$name" ]; then
      exit 1
    fi
    dir="$(container_dir "$name")"
    mkdir -p "$dir"
    printf 1 > "$dir/exists"
    printf running > "$dir/status"
    printf 1 > "$dir/running"
    printf 0 > "$dir/exit_code"
    : > "$dir/error"
    printf 0 > "$dir/oom_killed"
    cp "$labels_file" "$dir/labels"
    rm -f "$labels_file"
    printf '%s' "$image" > "$dir/image"
    : > "$dir/logs"
    printf 'fake-container-id\n'
    ;;
  start)
    name="${1:-}"
    if [ "$(container_exists "$name")" != "1" ]; then
      exit 1
    fi
    printf running > "$(container_dir "$name")/status"
    printf 1 > "$(container_dir "$name")/running"
    printf 0 > "$(container_dir "$name")/exit_code"
    printf '%s\n' "$name"
    ;;
  rm)
    if [ "${1:-}" = "-f" ]; then
      name="${2:-}"
    else
      name="${1:-}"
    fi
    if [ -n "$name" ]; then
      dir="$(container_dir "$name")"
      mkdir -p "$dir"
      printf 0 > "$dir/exists"
      printf removed > "$dir/status"
      printf 0 > "$dir/running"
    fi
    printf '%s\n' "$name"
    ;;
  logs)
    name=""
    previous=""
    for arg in "$@"; do
      if [ "$previous" = "--tail" ]; then
        previous=""
        continue
      fi
      if [ "$arg" = "--tail" ]; then
        previous="--tail"
        continue
      fi
      name="$arg"
    done
    if [ -z "$name" ] || [ "$(container_exists "$name")" != "1" ]; then
      exit 1
    fi
    cat "$(container_dir "$name")/logs" 2>/dev/null || true
    ;;
  *)
    exit 1
    ;;
esac
`
}

func readFakeDockerState(t *testing.T, path string) fakeDockerState {
	t.Helper()
	state, err := loadFakeDockerState(path, defaultEmbeddingContainer)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func loadFakeDockerState(path, name string) (fakeDockerState, error) {
	containerPath := filepath.Join(path, "containers", name)
	exists, _ := os.ReadFile(filepath.Join(containerPath, "exists"))
	status, _ := os.ReadFile(filepath.Join(containerPath, "status"))
	running, _ := os.ReadFile(filepath.Join(containerPath, "running"))
	exitCode, _ := os.ReadFile(filepath.Join(containerPath, "exit_code"))
	stateErr, _ := os.ReadFile(filepath.Join(containerPath, "error"))
	oomKilled, _ := os.ReadFile(filepath.Join(containerPath, "oom_killed"))
	image, _ := os.ReadFile(filepath.Join(containerPath, "image"))
	logs, _ := os.ReadFile(filepath.Join(containerPath, "logs"))
	existsValue := strings.TrimSpace(string(exists)) == "1"
	runningValue := strings.TrimSpace(string(running)) == "1"
	if len(running) == 0 {
		runningValue = existsValue
	}
	statusValue := strings.TrimSpace(string(status))
	if statusValue == "" {
		if runningValue {
			statusValue = "running"
		} else if existsValue {
			statusValue = "exited"
		}
	}
	state := fakeDockerState{
		Exists:    existsValue,
		Status:    statusValue,
		Running:   runningValue,
		ExitCode:  intFromString(strings.TrimSpace(string(exitCode)), 0),
		Error:     strings.TrimSpace(string(stateErr)),
		OOMKilled: strings.TrimSpace(string(oomKilled)) == "1",
		Image:     strings.TrimSpace(string(image)),
		Logs:      string(logs),
		Labels:    map[string]string{},
	}
	rawLabels, _ := os.ReadFile(filepath.Join(containerPath, "labels"))
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

func writeFakeDockerState(t *testing.T, path, name string, state fakeDockerState) {
	t.Helper()
	if err := saveFakeDockerState(path, name, state); err != nil {
		t.Fatal(err)
	}
}

func saveFakeDockerState(path, name string, state fakeDockerState) error {
	if state.Labels == nil {
		state.Labels = map[string]string{}
	}
	containerPath := filepath.Join(path, "containers", name)
	if err := os.MkdirAll(containerPath, 0o755); err != nil {
		return err
	}
	exists := "0"
	if state.Exists {
		exists = "1"
	}
	if err := os.WriteFile(filepath.Join(containerPath, "exists"), []byte(exists), 0o644); err != nil {
		return err
	}
	status := strings.TrimSpace(state.Status)
	if status == "" {
		if state.Running {
			status = "running"
		} else if state.Exists {
			status = "exited"
		} else {
			status = "removed"
		}
	}
	if err := os.WriteFile(filepath.Join(containerPath, "status"), []byte(status), 0o644); err != nil {
		return err
	}
	running := "0"
	if state.Running {
		running = "1"
	}
	if err := os.WriteFile(filepath.Join(containerPath, "running"), []byte(running), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(containerPath, "exit_code"), []byte(strconv.Itoa(state.ExitCode)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(containerPath, "error"), []byte(state.Error), 0o644); err != nil {
		return err
	}
	oomKilled := "0"
	if state.OOMKilled {
		oomKilled = "1"
	}
	if err := os.WriteFile(filepath.Join(containerPath, "oom_killed"), []byte(oomKilled), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(containerPath, "image"), []byte(state.Image), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(containerPath, "logs"), []byte(state.Logs), 0o644); err != nil {
		return err
	}
	var labels strings.Builder
	for key, value := range state.Labels {
		labels.WriteString(key)
		labels.WriteString("=")
		labels.WriteString(value)
		labels.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(containerPath, "labels"), []byte(labels.String()), 0o644)
}

func newEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("model request = %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/embeddings":
			writeTestJSON(t, w, map[string]any{"data": []map[string]any{{"embedding": []float64{0.1}}}})
		case "/v1/rerank":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode rerank request: %v", err)
			}
			if _, ok := body["documents"]; !ok {
				t.Fatalf("rerank request missing documents: %#v", body)
			}
			writeTestJSON(t, w, map[string]any{"results": []map[string]any{{"index": 0, "score": 1.0}}})
		default:
			t.Fatalf("model request = %s %s", r.Method, r.URL.Path)
		}
	}))
}

func TestWaitLocalRunnersReadyChecksEmbeddingAndRerankerConcurrently(t *testing.T) {
	embeddingStarted := make(chan struct{})
	rerankerStarted := make(chan struct{})
	var embeddingOnce sync.Once
	var rerankerOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/embeddings":
			embeddingOnce.Do(func() { close(embeddingStarted) })
			select {
			case <-rerankerStarted:
				writeTestJSON(t, w, map[string]any{"data": []map[string]any{{"embedding": []float64{0.1}}}})
			case <-time.After(500 * time.Millisecond):
				http.Error(w, "reranker readiness did not run concurrently", http.StatusServiceUnavailable)
			}
		case "/v1/rerank":
			rerankerOnce.Do(func() { close(rerankerStarted) })
			select {
			case <-embeddingStarted:
				writeTestJSON(t, w, map[string]any{"results": []map[string]any{{"index": 0, "score": 1.0}}})
			case <-time.After(500 * time.Millisecond):
				http.Error(w, "embedding readiness did not run concurrently", http.StatusServiceUnavailable)
			}
		default:
			t.Fatalf("model request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := waitLocalRunnersReady(context.Background(), []embeddingRunnerConfig{
			{Kind: "embedding", Model: defaultServedModelName, BaseURL: server.URL + "/v1", Dims: 1024, ReadinessTimeout: time.Second},
			{Kind: "reranker", Model: defaultRerankerServedModelName, BaseURL: server.URL + "/v1", ReadinessTimeout: time.Second},
		}, 2*time.Second)
		if err != nil {
			t.Fatalf("waitLocalRunnersReady error = %v", err)
		}
	})
	for _, want := range []string{"Waiting for embedding endpoint", "Waiting for reranker endpoint", "Local embedding ready", "Local reranker ready"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestWaitLocalRunnerReadyStopsOnContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	err := waitLocalRunnerReady(ctx, embeddingRunnerConfig{
		Kind:             "embedding",
		Model:            defaultServedModelName,
		BaseURL:          server.URL + "/v1",
		Dims:             1024,
		ReadinessTimeout: 100 * time.Millisecond,
	}, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "readiness canceled") {
		t.Fatalf("waitLocalRunnerReady error = %v, want canceled", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("waitLocalRunnerReady ignored canceled context, elapsed=%s", elapsed)
	}
}

func TestWaitLocalRunnerReadyReturnsWhenContainerExited(t *testing.T) {
	docker := newFakeDocker(t)
	writeFakeDockerState(t, docker.state, defaultEmbeddingContainer, fakeDockerState{
		Exists:   true,
		Status:   "exited",
		Running:  false,
		ExitCode: 42,
		Image:    defaultTEIImage(),
		Labels:   map[string]string{localRunnerHashLabel: "test"},
		Logs:     "model failed to load\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	started := time.Now()
	err := waitLocalRunnerReady(context.Background(), embeddingRunnerConfig{
		Kind:             "embedding",
		Container:        defaultEmbeddingContainer,
		Model:            defaultServedModelName,
		BaseURL:          server.URL + "/v1",
		Dims:             1024,
		ReadinessTimeout: 100 * time.Millisecond,
	}, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "container exited before readiness") || !strings.Contains(err.Error(), "exit_code=42") || !strings.Contains(err.Error(), "abra model logs --tail 120") {
		t.Fatalf("waitLocalRunnerReady error = %v, want container exited", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("waitLocalRunnerReady should fail fast for exited container, elapsed=%s", elapsed)
	}
}

func TestWaitLocalRunnerReadyDoesNotTreatCreatedContainerAsExited(t *testing.T) {
	docker := newFakeDocker(t)
	writeFakeDockerState(t, docker.state, defaultEmbeddingContainer, fakeDockerState{
		Exists:  true,
		Status:  "created",
		Running: false,
		Image:   defaultTEIImage(),
		Labels:  map[string]string{localRunnerHashLabel: "test"},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := waitLocalRunnerReady(context.Background(), embeddingRunnerConfig{
		Kind:             "embedding",
		Container:        defaultEmbeddingContainer,
		Model:            defaultServedModelName,
		BaseURL:          server.URL + "/v1",
		Dims:             1024,
		ReadinessTimeout: 50 * time.Millisecond,
	}, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("waitLocalRunnerReady error = %v, want normal readiness timeout", err)
	}
	if strings.Contains(err.Error(), "container exited before readiness") {
		t.Fatalf("created container should not be reported as exited: %v", err)
	}
}

func TestWaitLocalRunnersReadyPrefersCausalErrorOverCanceledPeer(t *testing.T) {
	docker := newFakeDocker(t)
	writeFakeDockerState(t, docker.state, defaultRerankerContainer, fakeDockerState{
		Exists:   true,
		Status:   "exited",
		Running:  false,
		ExitCode: 42,
		Image:    defaultTEIImage(),
		Labels:   map[string]string{localRerankerRunnerHashLabel: "test"},
		Logs:     "reranker failed to load\n",
	})
	_ = docker
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := waitLocalRunnersReady(context.Background(), []embeddingRunnerConfig{
		{
			Kind:             "embedding",
			Model:            defaultServedModelName,
			BaseURL:          server.URL + "/v1",
			Dims:             1024,
			ReadinessTimeout: time.Second,
		},
		{
			Kind:             "reranker",
			Container:        defaultRerankerContainer,
			Model:            defaultRerankerServedModelName,
			BaseURL:          server.URL + "/v1",
			ReadinessTimeout: time.Second,
		},
	}, 2*time.Second)
	if err == nil {
		t.Fatal("waitLocalRunnersReady error = nil, want causal reranker error")
	}
	if !strings.Contains(err.Error(), "local reranker container exited before readiness") || !strings.Contains(err.Error(), "exit_code=42") {
		t.Fatalf("waitLocalRunnersReady error = %v, want reranker exit", err)
	}
	if strings.Contains(err.Error(), "readiness canceled") {
		t.Fatalf("waitLocalRunnersReady returned canceled peer instead of causal error: %v", err)
	}
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
		"RERANKER_PROVIDER=local",
		"RERANKER_BASE_URL=http://host.docker.internal:8081/v1",
		"RERANKER_MODEL=" + defaultRerankerServedModelName,
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
			"--reranker-base-url", server.URL + "/v1",
			"--cache-dir", cacheDir,
		})
	})
	if runErr != nil {
		t.Fatalf("models up error = %v", runErr)
	}
	if !strings.Contains(output, "Local embedding ready") || !strings.Contains(output, "Local reranker ready") {
		t.Fatalf("models up output missing readiness:\n%s", output)
	}
	calls := docker.calls()
	runCall := findFakeDockerRunByName(t, calls, defaultEmbeddingContainer)
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
	rerankRun := findFakeDockerRunByName(t, calls, defaultRerankerContainer)
	for _, want := range []string{
		"--name", defaultRerankerContainer,
		"--label", localRerankerModelLabel + "=" + defaultRerankerModelID,
		"-p", "127.0.0.1:" + wantPort + ":8080",
		defaultTEIImage(),
		"-hf", defaultRerankerModelID,
		"--reranking",
		"--pooling", "rank",
	} {
		if !slices.Contains(rerankRun.Args, want) {
			t.Fatalf("docker reranker run missing %q:\n%v", want, rerankRun.Args)
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
	if values["RERANKER_BASE_URL"] != wantBaseURL {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_PROVIDER"] != "local" || values["RERANKER_MODEL"] != defaultRerankerServedModelName {
		t.Fatalf("reranker config = provider %q model %q", values["RERANKER_PROVIDER"], values["RERANKER_MODEL"])
	}
}

func TestModelsUpStartsExistingMatchingContainer(t *testing.T) {
	_, server, docker := setupModelsLifecycleTest(t)
	args := parseArgs([]string{"models", "up", "--base-url", server.URL + "/v1", "--reranker-base-url", server.URL + "/v1"})
	cfg := embeddingRunner(args)
	docker.seedContainer(cfg.Image, map[string]string{localRunnerHashLabel: localRunnerConfigHash(cfg)})
	rerankerCfg := rerankerRunner(args)
	docker.seedNamedContainer(rerankerCfg.Container, rerankerCfg.Image, map[string]string{localRerankerRunnerHashLabel: localRunnerConfigHash(rerankerCfg)})

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
	if !hasFakeDockerStart(calls, defaultRerankerContainer) {
		t.Fatalf("missing reranker start: %#v", calls)
	}
}

func TestModelsUpRecreatesContainerWhenConfigHashDiffers(t *testing.T) {
	_, server, docker := setupModelsLifecycleTest(t)
	docker.seedContainer(defaultTEIImage(), map[string]string{localRunnerHashLabel: "stale"})

	if err := run(context.Background(), []string{"models", "up", "--base-url", server.URL + "/v1", "--reranker-base-url", server.URL + "/v1"}); err != nil {
		t.Fatalf("models up error = %v", err)
	}
	calls := docker.calls()
	rmIndex := fakeDockerCallIndex(calls, "rm")
	runIndex := fakeDockerCallIndex(calls, "run")
	if rmIndex < 0 || runIndex < 0 || rmIndex > runIndex {
		t.Fatalf("expected rm before run, calls=%#v", calls)
	}
}

func TestModelsUpSkipsLocalRerankerForCustomRerankerProvider(t *testing.T) {
	home, server, docker := setupModelsLifecycleTest(t)
	envFile := filepath.Join(home, "quickstart.env")
	mustWrite(t, envFile, strings.Join([]string{
		"EMBEDDING_PROVIDER=local",
		"EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1",
		"EMBEDDING_MODEL=" + defaultServedModelName,
		"EMBEDDING_DIMENSIONS=1024",
		"RERANKER_PROVIDER=compatible",
		"RERANKER_BASE_URL=https://rerank.example/v1",
		"RERANKER_MODEL=custom-reranker",
		"",
	}, "\n"))

	if err := run(context.Background(), []string{"models", "up", "--base-url", server.URL + "/v1"}); err != nil {
		t.Fatalf("models up error = %v", err)
	}
	calls := docker.calls()
	if hasFakeDockerRun(calls, defaultRerankerContainer) {
		t.Fatalf("custom reranker provider should not start local reranker: %#v", calls)
	}
	values, err := readEnvValues(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if values["RERANKER_PROVIDER"] != "compatible" || values["RERANKER_BASE_URL"] != "https://rerank.example/v1" || values["RERANKER_MODEL"] != "custom-reranker" {
		t.Fatalf("custom reranker config was not preserved: %#v", values)
	}
}

func TestModelsStatusJSONIncludesDockerConfigMatch(t *testing.T) {
	_, server, docker := setupModelsLifecycleTest(t)
	args := parseArgs([]string{"models", "status", "--json", "--base-url", server.URL + "/v1", "--reranker-base-url", server.URL + "/v1"})
	cfg := embeddingRunner(args)
	docker.seedContainer(cfg.Image, map[string]string{localRunnerHashLabel: localRunnerConfigHash(cfg)})
	rerankerCfg := rerankerRunner(args)
	docker.seedNamedContainer(rerankerCfg.Container, rerankerCfg.Image, map[string]string{localRerankerRunnerHashLabel: localRunnerConfigHash(rerankerCfg)})

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
	if status["container_state"] != "running" || status["container_running"] != true || intValue(status["container_exit_code"]) != 0 {
		t.Fatalf("status missing container state: %#v", status)
	}
	rerankerStatus, ok := status["reranker"].(map[string]any)
	if !ok || rerankerStatus["ready"] != true || rerankerStatus["config_matches"] != true {
		t.Fatalf("reranker status = %#v", status["reranker"])
	}
	if status["expected_config_hash"] == "" || status["container_config_hash"] == "" {
		t.Fatalf("missing config hashes: %#v", status)
	}
	if hasFakeDockerCall(docker.calls(), "run") || hasFakeDockerCall(docker.calls(), "rm") {
		t.Fatalf("status should not mutate docker: %#v", docker.calls())
	}
}

func TestModelsStatusJSONPrioritizesExitedContainerState(t *testing.T) {
	_, server, docker := setupModelsLifecycleTest(t)
	args := parseArgs([]string{"models", "status", "--json", "--base-url", server.URL + "/v1", "--reranker-provider", "none"})
	cfg := embeddingRunner(args)
	writeFakeDockerState(t, docker.state, cfg.Container, fakeDockerState{
		Exists:    true,
		Status:    "exited",
		Running:   false,
		ExitCode:  137,
		OOMKilled: true,
		Image:     cfg.Image,
		Labels:    map[string]string{localRunnerHashLabel: localRunnerConfigHash(cfg)},
		Logs:      "out of memory\n",
	})

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
	if status["ready"] != false || status["container_state"] != "exited" || intValue(status["container_exit_code"]) != 137 || status["container_oom_killed"] != true {
		t.Fatalf("status missing exited state: %#v", status)
	}
	errorText := stringValue(status["error"], "")
	if !strings.Contains(errorText, "container exited before readiness") || !strings.Contains(errorText, "oom_killed=true") {
		t.Fatalf("status error = %q", status["error"])
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

func findFakeDockerRunByName(t *testing.T, calls []fakeDockerCall, name string) fakeDockerCall {
	t.Helper()
	for _, call := range calls {
		if len(call.Args) > 2 && call.Args[0] == "run" && slices.Contains(call.Args, "--name") && slices.Contains(call.Args, name) {
			return call
		}
	}
	t.Fatalf("missing docker run for %s in %#v", name, calls)
	return fakeDockerCall{}
}

func hasFakeDockerStart(calls []fakeDockerCall, name string) bool {
	for _, call := range calls {
		if len(call.Args) > 1 && call.Args[0] == "start" && call.Args[1] == name {
			return true
		}
	}
	return false
}

func hasFakeDockerRun(calls []fakeDockerCall, name string) bool {
	for _, call := range calls {
		if len(call.Args) > 2 && call.Args[0] == "run" && slices.Contains(call.Args, "--name") && slices.Contains(call.Args, name) {
			return true
		}
	}
	return false
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
