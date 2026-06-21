package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	mustWrite(t, filepath.Join(bin, "docker"), "#!/bin/sh\nABRA_FAKE_DOCKER=1 exec \"$ABRA_TEST_BINARY\" -test.run '^TestFakeDockerProcess$' -test.paniconexit0=false -- \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeDockerState(t, state, fakeDockerState{Labels: map[string]string{}})
	t.Setenv("ABRA_TEST_BINARY", os.Args[0])
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
		var call fakeDockerCall
		if err := json.Unmarshal(scanner.Bytes(), &call); err != nil {
			f.t.Fatalf("decode fake docker call: %v", err)
		}
		calls = append(calls, call)
	}
	if err := scanner.Err(); err != nil {
		f.t.Fatal(err)
	}
	return calls
}

func TestFakeDockerProcess(t *testing.T) {
	if os.Getenv("ABRA_FAKE_DOCKER") != "1" {
		return
	}
	if err := fakeDockerMain(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func fakeDockerMain() error {
	separator := slices.Index(os.Args, "--")
	if separator < 0 || separator+1 >= len(os.Args) {
		return fmt.Errorf("missing fake docker args")
	}
	args := append([]string(nil), os.Args[separator+1:]...)
	statePath := os.Getenv("ABRA_FAKE_DOCKER_STATE")
	logPath := os.Getenv("ABRA_FAKE_DOCKER_LOG")
	if statePath == "" || logPath == "" {
		return fmt.Errorf("missing fake docker env")
	}
	if err := appendFakeDockerCall(logPath, args); err != nil {
		return err
	}
	state, err := loadFakeDockerState(statePath)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("missing docker command")
	}
	switch args[0] {
	case "container":
		return fakeDockerContainer(state, args[1:])
	case "run":
		updated, output, err := fakeDockerRun(state, args[1:])
		if err != nil {
			return err
		}
		if err := saveFakeDockerState(statePath, updated); err != nil {
			return err
		}
		if output != "" {
			fmt.Println(output)
		}
		return nil
	case "start":
		if !state.Exists {
			return fmt.Errorf("container not found")
		}
		if len(args) > 1 {
			fmt.Println(args[1])
		}
		return nil
	case "rm":
		state.Exists = false
		if err := saveFakeDockerState(statePath, state); err != nil {
			return err
		}
		if len(args) > 2 {
			fmt.Println(args[2])
		}
		return nil
	case "logs":
		if !state.Exists {
			return fmt.Errorf("container not found")
		}
		fmt.Print(state.Logs)
		return nil
	default:
		return fmt.Errorf("unsupported fake docker command: %s", strings.Join(args, " "))
	}
}

func fakeDockerContainer(state fakeDockerState, args []string) error {
	if len(args) == 0 || args[0] != "inspect" {
		return fmt.Errorf("unsupported fake docker container command: %s", strings.Join(args, " "))
	}
	if !state.Exists {
		return fmt.Errorf("container not found")
	}
	format := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--format" && i+1 < len(args) {
			format = args[i+1]
			break
		}
	}
	switch {
	case format == "":
		return nil
	case format == "{{.Config.Image}}":
		fmt.Println(state.Image)
		return nil
	case strings.Contains(format, ".Config.Labels"):
		label := labelFromDockerFormat(format)
		if value := state.Labels[label]; value != "" {
			fmt.Println(value)
		} else {
			fmt.Println("<no value>")
		}
		return nil
	default:
		return fmt.Errorf("unsupported inspect format: %s", format)
	}
}

func fakeDockerRun(state fakeDockerState, args []string) (fakeDockerState, string, error) {
	state.Exists = true
	state.Labels = map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--name":
			i++
		case "--pull":
			i++
		case "--label":
			i++
			key, value, _ := strings.Cut(args[i], "=")
			state.Labels[key] = value
		case "-p", "-v":
			i++
		case "-d":
		default:
			if strings.HasPrefix(arg, "-") {
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
				}
				continue
			}
			state.Image = arg
			return state, "fake-container-id", nil
		}
	}
	return state, "", fmt.Errorf("docker run did not include image")
}

func labelFromDockerFormat(format string) string {
	start := strings.Index(format, "\"")
	end := strings.LastIndex(format, "\"")
	if start >= 0 && end > start {
		return format[start+1 : end]
	}
	return ""
}

func appendFakeDockerCall(path string, args []string) error {
	cwd, _ := os.Getwd()
	bytes, err := json.Marshal(fakeDockerCall{Dir: cwd, Args: args})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(bytes, '\n'))
	return err
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
	raw, err := os.ReadFile(path)
	if err != nil {
		return fakeDockerState{}, err
	}
	var state fakeDockerState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fakeDockerState{}, err
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
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
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
