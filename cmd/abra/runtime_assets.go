package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func ensureEnv(args cliArgs) error {
	if fileExists(envPath(args)) {
		return ensureEnvHasQuickstartDefaults(args)
	}
	return initEnv(args)
}

func ensureEnvHasQuickstartDefaults(args cliArgs) error {
	path := envPath(args)
	lines, err := readEnvLines(path)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, line := range lines {
		key, value, ok := parseEnvLine(line)
		if ok {
			values[key] = value
		}
	}
	if strings.EqualFold(strings.TrimSpace(values["NODE_ENV"]), "production") {
		return nil
	}
	normalized := []string{}
	for i, line := range lines {
		key, value, ok := parseEnvLine(line)
		if !ok {
			continue
		}
		if key == "RATE_LIMIT_WINDOW" && strings.TrimSpace(value) == "1 minute" {
			lines[i] = "RATE_LIMIT_WINDOW=1m"
			normalized = append(normalized, key)
		}
	}
	if !shouldBackfillQuickstartEnv(args, values) {
		if len(normalized) == 0 {
			return nil
		}
		content := strings.Join(lines, "\n")
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Updated env with shell-safe quickstart values: "+strings.Join(normalized, ", "))
		return nil
	}
	defaults := orderedEnvDefaults(demoEnv())
	missing := []string{}
	for _, item := range defaults {
		if _, exists := values[item.key]; exists {
			continue
		}
		lines = append(lines, item.key+"="+item.value)
		missing = append(missing, item.key)
	}
	if len(missing) == 0 && len(normalized) == 0 {
		return nil
	}
	content := strings.Join(lines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	if len(missing) > 0 {
		fmt.Fprintln(os.Stderr, "Updated env with missing quickstart defaults: "+strings.Join(missing, ", "))
	}
	if len(normalized) > 0 {
		fmt.Fprintln(os.Stderr, "Updated env with shell-safe quickstart values: "+strings.Join(normalized, ", "))
	}
	return nil
}

func shouldBackfillQuickstartEnv(args cliArgs, values map[string]string) bool {
	if boolFlag(args, "demo") {
		return true
	}
	if envPath(args) == defaultEnvPath() {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(values["NODE_ENV"]), "development") {
		return true
	}
	return false
}

func orderedEnvDefaults(content string) []envDefault {
	defaults := []envDefault{}
	seen := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := parseEnvLine(line)
		if !ok || seen[key] {
			continue
		}
		defaults = append(defaults, envDefault{key: key, value: value})
		seen[key] = true
	}
	return defaults
}

func envPath(args cliArgs) string {
	path := flag(args, "env-file", flag(args, "env", defaultEnvPath()))
	return filepath.Clean(path)
}

func defaultEnvPath() string {
	if isAbraSourceCheckout(".") {
		return checkoutEnvPath
	}
	return filepath.Join(userConfigDir(), "quickstart.env")
}

func isAbraSourceCheckout(dir string) bool {
	return hasLocalCompose(dir) &&
		fileExists(filepath.Join(dir, "go.mod")) &&
		fileExists(filepath.Join(dir, "cmd", "abra", "main.go")) &&
		fileExists(filepath.Join(dir, "migrations", "001_init.sql"))
}

func hasLocalCompose(dir string) bool {
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

func userConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("ABRA_HOME")); value != "" {
		return value
	}
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, "abra")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".abra")
	}
	return ".abra"
}

func projectDir(args cliArgs) (string, error) {
	if explicit := flag(args, "project-dir", ""); explicit != "" {
		return filepath.Abs(explicit)
	}
	if isAbraSourceCheckout(".") {
		return filepath.Abs(".")
	}
	return managedRuntimeDir(), nil
}

func ensureProjectDir(ctx context.Context, args cliArgs) (string, error) {
	dir, err := projectDir(args)
	if err != nil {
		return "", err
	}
	if hasLocalCompose(dir) {
		if err := prepareRuntimeProjectDir(dir); err != nil {
			return "", err
		}
		return dir, nil
	}
	if err := downloadRuntimeSource(ctx, dir); err != nil {
		return "", err
	}
	if !hasLocalCompose(dir) {
		return "", fmt.Errorf("runtime bundle did not include docker-compose.yml: %s", dir)
	}
	if err := prepareRuntimeProjectDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func managedRuntimeDir() string {
	return filepath.Join(userConfigDir(), "runtime", runtimeVersion(), "source")
}

func isManagedRuntimeDir(dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absRuntime, err := filepath.Abs(managedRuntimeDir())
	if err != nil {
		return false
	}
	return filepath.Clean(absDir) == filepath.Clean(absRuntime)
}

func prepareRuntimeProjectDir(dir string) error {
	if !isManagedRuntimeDir(dir) {
		return nil
	}
	err := os.Remove(filepath.Join(dir, "docker-compose.dev.yml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func runtimeVersion() string {
	if strings.TrimSpace(version) != "" && version != "dev" {
		return strings.TrimSpace(version)
	}
	return "main"
}

func runtimeSourceURL() string {
	if value := strings.TrimSpace(os.Getenv("ABRA_SOURCE_URL")); value != "" {
		return value
	}
	if runtimeVersion() == "main" {
		return "https://github.com/hermawan22/abra/archive/refs/heads/main.tar.gz"
	}
	return runtimeReleaseAssetURL(runtimeBundleAssetName())
}

func runtimeReleaseTag() string {
	value := strings.TrimSpace(runtimeVersion())
	if value == "" || value == "main" {
		return value
	}
	if strings.HasPrefix(value, "v") {
		return value
	}
	return "v" + value
}

func runtimeBundleAssetName() string {
	return "abra_runtime_" + runtimeReleaseTag() + ".tar.gz"
}

func runtimeReleaseBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("ABRA_RELEASE_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return "https://github.com/" + abraRepository() + "/releases/download/" + url.PathEscape(runtimeReleaseTag())
}

func runtimeReleaseAssetURL(asset string) string {
	return runtimeReleaseBaseURL() + "/" + url.PathEscape(asset)
}

func abraRepository() string {
	if value := strings.TrimSpace(os.Getenv("ABRA_REPO")); value != "" {
		return value
	}
	return "hermawan22/abra"
}

func downloadRuntimeSource(ctx context.Context, targetDir string) error {
	url := runtimeSourceURL()
	fmt.Println("Downloading Abra runtime: " + url)
	tmpDir := targetDir + ".tmp-" + timestamp()
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	archive, err := downloadRuntimeArchive(ctx, url)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	if err := extractTarGz(bytes.NewReader(archive), tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	if err := os.RemoveAll(targetDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	if err := os.Rename(tmpDir, targetDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	return nil
}

func downloadRuntimeArchive(ctx context.Context, archiveURL string) ([]byte, error) {
	raw, err := downloadURL(ctx, archiveURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(os.Getenv("ABRA_SOURCE_URL")) != "" {
		if err := verifyRuntimeSourceOverride(raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
	if runtimeVersion() == "main" {
		fmt.Println("Runtime source override/main branch is not release-verified; use a pinned release for production.")
		return raw, nil
	}
	asset := runtimeBundleAssetName()
	sums, err := downloadURL(ctx, runtimeReleaseAssetURL("SHA256SUMS"))
	if err != nil {
		return nil, fmt.Errorf("download runtime SHA256SUMS: %w", err)
	}
	if err := verifyReleaseChecksum(raw, sums, asset); err != nil {
		return nil, err
	}
	fmt.Println("Verified runtime checksum: " + asset)
	if err := verifyRuntimeAttestation(raw, asset); err != nil {
		return nil, err
	}
	if err := verifyRuntimeAttestation(sums, "SHA256SUMS"); err != nil {
		return nil, err
	}
	return raw, nil
}

func downloadURL(ctx context.Context, value string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s: http %d", value, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func verifyReleaseChecksum(file, sums []byte, asset string) error {
	expected := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("SHA256SUMS does not include %s", asset)
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(file))
	if actual != expected {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	return nil
}

func verifyRuntimeSourceOverride(raw []byte) error {
	expected := strings.TrimSpace(os.Getenv("ABRA_SOURCE_SHA256"))
	if expected != "" {
		expected = strings.ToLower(strings.TrimPrefix(expected, "sha256:"))
		if len(expected) != 64 || !isHexString(expected) {
			return errors.New("invalid ABRA_SOURCE_SHA256; expected a 64-character SHA-256 hex digest")
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(raw))
		if actual != expected {
			return errors.New("checksum mismatch for ABRA_SOURCE_URL runtime archive")
		}
		fmt.Println("Verified runtime source checksum: ABRA_SOURCE_SHA256")
		return nil
	}
	if truthyEnv("ABRA_ALLOW_UNVERIFIED_SOURCE_URL") {
		fmt.Println("Skipped runtime source checksum verification: ABRA_ALLOW_UNVERIFIED_SOURCE_URL=true")
		return nil
	}
	return errors.New("ABRA_SOURCE_URL requires ABRA_SOURCE_SHA256; set ABRA_ALLOW_UNVERIFIED_SOURCE_URL=1 only when intentionally accepting an unverified runtime archive")
}

func isHexString(value string) bool {
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

func truthyEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func verifyRuntimeAttestation(content []byte, asset string) error {
	tmp, err := os.CreateTemp("", "abra-runtime-*-"+asset)
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	defer os.Remove(tmpPath)
	return verifyArtifactAttestationPath(tmpPath, asset, "runtime", "ABRA_VERIFY_RUNTIME_ATTESTATION")
}

func verifyInstallScriptAttestation(scriptPath string) error {
	return verifyArtifactAttestationPath(scriptPath, "install.sh", "install script", "ABRA_VERIFY_INSTALL_ATTESTATION")
}

func verifyArtifactAttestationPath(path, asset, label, specificEnv string) error {
	mode, modeEnv, err := attestationVerificationMode(specificEnv)
	if err != nil {
		return err
	}
	modeLower := strings.ToLower(mode)
	switch modeLower {
	case "0", "false", "no", "off":
		fmt.Println("Skipped " + label + " attestation verification: " + modeEnv + "=" + mode)
		return nil
	}
	if _, err := execLookPath("gh"); err != nil {
		if modeLower == "auto" {
			fmt.Println("GitHub CLI not found; " + label + " attestation verification skipped.")
			fmt.Println("For hardened " + label + " downloads, install gh and set " + specificEnv + "=1.")
			return nil
		}
		return fmt.Errorf("missing GitHub CLI: install gh or set %s=0 to skip %s provenance verification", specificEnv, label)
	}
	if _, err := commandOutput("gh", "attestation", "verify", "--repo", abraRepository(), path); err != nil {
		if modeLower == "auto" {
			return fmt.Errorf("GitHub artifact attestation verification failed for %s; set %s=0 only when you intentionally accept this download without provenance verification", asset, specificEnv)
		}
		return fmt.Errorf("GitHub artifact attestation verification failed for %s: %w", asset, err)
	}
	fmt.Println("Verified " + label + " artifact attestation: " + asset)
	return nil
}

func attestationVerificationMode(specificEnv string) (string, string, error) {
	modeEnv := specificEnv
	mode := strings.TrimSpace(os.Getenv(specificEnv))
	if mode == "" {
		if value := strings.TrimSpace(os.Getenv("ABRA_VERIFY_ATTESTATION")); value != "" {
			mode = value
			modeEnv = "ABRA_VERIFY_ATTESTATION"
		}
	}
	if mode == "" {
		mode = "auto"
	}
	switch strings.ToLower(mode) {
	case "0", "false", "no", "off", "1", "true", "yes", "on", "auto":
		return mode, modeEnv, nil
	default:
		return "", modeEnv, fmt.Errorf("invalid %s=%s; use auto, 1, or 0", modeEnv, mode)
	}
}

func extractTarGz(reader io.Reader, targetDir string) error {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gz.Close()
	targetRoot, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok, err := archiveEntryPath(header.Name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		dst, err := filepath.Abs(filepath.Join(targetRoot, rel))
		if err != nil {
			return err
		}
		if dst != targetRoot && !strings.HasPrefix(dst, targetRoot+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe archive destination: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, tr)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func archiveEntryPath(headerName string) (string, bool, error) {
	name := strings.TrimPrefix(filepath.ToSlash(headerName), "/")
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", false, nil
	}
	if strings.Contains(parts[1], `\`) {
		return "", false, fmt.Errorf("unsafe archive path: %s", headerName)
	}
	rel, err := filepath.Localize(parts[1])
	if err != nil {
		return "", false, fmt.Errorf("unsafe archive path: %s", headerName)
	}
	rel = filepath.Clean(rel)
	if rel == "." || !filepath.IsLocal(rel) {
		return "", false, fmt.Errorf("unsafe archive path: %s", headerName)
	}
	return rel, true, nil
}

func cfg(args cliArgs) contextConfig {
	envFile := envPath(args)
	fileValues := map[string]string{}
	if fileExists(envFile) {
		if values, err := readEnvValues(envFile); err == nil {
			fileValues = values
		}
	}
	fileBaseURL := firstNonEmpty(fileValues["ABRA_BASE_URL"], fileValues["ABRA_URL"])
	if fileBaseURL == "" {
		fileBaseURL = baseURLFromPort(fileValues["ABRA_PORT"])
	}
	envBaseURL := firstNonEmpty(os.Getenv("ABRA_BASE_URL"), os.Getenv("ABRA_URL"))
	if envBaseURL == "" {
		envBaseURL = baseURLFromPort(os.Getenv("ABRA_PORT"))
	}
	return contextConfig{
		EnvFile: envFile,
		BaseURL: flag(args, "base-url", firstNonEmpty(envBaseURL, fileBaseURL, defaultBaseURL)),
		Token:   flag(args, "token", firstNonEmpty(os.Getenv("ABRA_API_TOKEN"), firstCSV(os.Getenv("ABRA_API_KEYS"), ""), fileValues["ABRA_API_TOKEN"], firstCSV(fileValues["ABRA_API_KEYS"], ""), defaultToken)),
	}
}

func baseURLFromPort(port string) string {
	port = strings.TrimSpace(port)
	if port == "" {
		return ""
	}
	return "http://127.0.0.1:" + port
}

func flag(args cliArgs, name, fallback string) string {
	if value := strings.TrimSpace(args.Flags[name]); value != "" {
		return value
	}
	return fallback
}

func defaultConnectorKind(sourceType string) string {
	if strings.TrimSpace(sourceType) == "mcp" {
		return "mcp"
	}
	return "generic"
}

func parseJSONObjectFlag(raw, flagName string) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("--%s must be a JSON object: %w", flagName, err)
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, nil
}

func mergeAnyMaps(base, override map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func parseHeaderEnvFlag(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	values := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			parts = strings.SplitN(item, ":", 2)
		}
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("--header-env entries must be HEADER=ENV_NAME, got %q", item)
		}
		values[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return values, nil
}

func scopeOrDefault(args cliArgs, pathHint string) string {
	if value := flag(args, "scope", ""); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("ABRA_SCOPE")); value != "" {
		return value
	}
	return defaultScope(pathHint)
}

func defaultScope(pathHint string) string {
	root := "."
	if strings.TrimSpace(pathHint) != "" {
		root = pathHint
	}
	if scope := scopeFromRepositoryURL(root); scope != "" {
		return scope
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	if gitRoot := findGitRoot(abs); gitRoot != "" {
		abs = gitRoot
	}
	name := slug(filepath.Base(abs))
	if name == "" {
		name = "local"
	}
	return "repo:" + name
}

func scopeFromRepositoryURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "git@") {
		if idx := strings.Index(raw, ":"); idx >= 0 && idx+1 < len(raw) {
			return scopeFromRepoPath(raw[idx+1:])
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return scopeFromRepoPath(parsed.Path)
}

func scopeFromRepoPath(path string) string {
	parts := strings.Split(strings.Trim(strings.TrimSuffix(path, ".git"), "/"), "/")
	if len(parts) >= 2 {
		name := slug(parts[len(parts)-2] + "-" + parts[len(parts)-1])
		if name != "" {
			return "repo:" + name
		}
	}
	if len(parts) == 1 {
		if name := slug(parts[0]); name != "" {
			return "repo:" + name
		}
	}
	return ""
}

func findGitRoot(start string) string {
	dir := start
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func boolFlag(args cliArgs, name string) bool {
	if args.Bools[name] {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(args.Flags[name]))
	return value == "1" || value == "true" || value == "yes"
}

func intFlag(args cliArgs, name string, fallback int) int {
	value := flag(args, name, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func floatFlag(args cliArgs, name string, fallback float64) float64 {
	value := flag(args, name, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func cliTimeout(args cliArgs, fallback time.Duration) time.Duration {
	value := firstNonEmpty(flag(args, "timeout", ""), os.Getenv("ABRA_CLI_TIMEOUT"))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err == nil && parsed > 0 {
		return parsed
	}
	seconds, err := strconv.Atoi(value)
	if err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func firstCSV(value, fallback string) string {
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func csv(value string) []string {
	parts := []string{}
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
}

func stringMapToAny(input map[string]string) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func localFileURL(root, relPath string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(root, filepath.FromSlash(relPath)))}
	return u.String()
}

func waitForSourceJob(ctx context.Context, args cliArgs, scope, sourceID, jobID string) (map[string]any, error) {
	timeout := waitTimeout(args)
	deadline := time.Now().Add(timeout)
	quiet := boolFlag(args, "json")
	for {
		limit := "1"
		if jobID != "" {
			limit = "20"
		}
		path := "/ingestion/jobs?scope=" + urlQueryEscape(scope) + "&source_config_id=" + urlQueryEscape(sourceID) + "&limit=" + limit
		result, _, err := getJSON(ctx, args, path)
		if err == nil {
			jobs, _ := result["ingestion_jobs"].([]any)
			if job := matchingIngestionJob(jobs, jobID); job != nil {
				status := stringValue(job["status"], "")
				switch status {
				case "succeeded":
					if !quiet {
						fmt.Printf("Job succeeded: %s seen=%v changed=%v chunks=%v claims=%v\n", stringValue(job["id"], jobID), job["documents_seen"], job["documents_changed"], job["chunks_written"], job["claims_written"])
					}
					return job, nil
				case "failed", "canceled":
					return job, fmt.Errorf("job %s %s: %s", stringValue(job["id"], jobID), status, stringValue(job["last_error"], ""))
				}
				if !quiet {
					fmt.Println("Job " + stringValue(job["id"], jobID) + " " + status + "...")
				}
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	return nil, errors.New("job did not finish within " + timeout.String() + "; run `abra jobs --scope " + scope + "`")
}

func verifySourceRecall(ctx context.Context, args cliArgs, scope, sourceID, query string) error {
	payload, err := verifySourceRecallPayload(ctx, args, scope, sourceID, query)
	if err != nil {
		return err
	}
	fmt.Printf("Recall verified: query=%q claims=%v documents=%v source=%s\n",
		stringValue(payload["query"], query),
		payload["claims"],
		payload["supporting_documents"],
		sourceID,
	)
	return nil
}

func verifySourceRecallPayload(ctx context.Context, args cliArgs, scope, sourceID, query string) (map[string]any, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("--verify requires --verify-query when the source name is empty")
	}
	result, err := callMCPTool(ctx, args, "recall", map[string]any{
		"query":              query,
		"scope":              scope,
		"limit":              intFlag(args, "verify-limit", 5),
		"include_unverified": boolFlag(args, "include-unverified"),
	})
	if err != nil {
		return nil, err
	}
	claims := lenSlice(result["claims"])
	docs := lenSlice(result["supporting_documents"])
	if claims == 0 && docs == 0 {
		return nil, fmt.Errorf("recall verification returned no source-backed context for query %q in scope %s; inspect `abra sources logs %s` and try a more specific --verify-query", query, scope, sourceID)
	}
	return map[string]any{
		"query":                query,
		"scope":                scope,
		"source_config_id":     sourceID,
		"claims":               claims,
		"supporting_documents": docs,
	}, nil
}

func matchingIngestionJob(jobs []any, jobID string) map[string]any {
	for _, raw := range jobs {
		job, _ := raw.(map[string]any)
		if job == nil {
			continue
		}
		if jobID == "" || stringValue(job["id"], "") == jobID {
			return job
		}
	}
	return nil
}

func waitTimeout(args cliArgs) time.Duration {
	value := firstNonEmpty(flag(args, "wait-timeout", ""), flag(args, "timeout", ""), os.Getenv("ABRA_CLI_WAIT_TIMEOUT"))
	if value == "" {
		return time.Minute
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return time.Minute
	}
	return timeout
}

func stringValue(value any, fallback string) string {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			return typed
		}
	case fmt.Stringer:
		return typed.String()
	}
	return fallback
}

func boolValue(value any, fallback bool) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes":
			return true
		case "0", "false", "no":
			return false
		}
	}
	return fallback
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	}
	return 0
}

func lenSlice(value any) int {
	if items, ok := value.([]any); ok {
		return len(items)
	}
	return 0
}

func printJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func timestamp() string {
	return time.Now().UTC().Format("20060102150405")
}

func slug(value string) string {
	value = strings.ToLower(value)
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func demoEnv() string {
	composeFile := "docker-compose.yml"
	abraImage := defaultRuntimeImageRef()
	if isAbraSourceCheckout(".") {
		composeFile = "docker-compose.yml:docker-compose.dev.yml"
		abraImage = "abra:local"
	}
	return strings.NewReplacer(
		"{{COMPOSE_FILE}}", composeFile,
		"{{ABRA_IMAGE}}", abraImage,
	).Replace(demoEnvTemplate)
}

func defaultRuntimeImageRef() string {
	if !isAbraSourceCheckout(".") {
		if image := firstRuntimeImageDigest(); image != "" {
			return image
		}
	}
	version := runtimeVersion()
	if version == "main" {
		return "ghcr.io/hermawan22/abra:main"
	}
	return "ghcr.io/hermawan22/abra:" + version
}

func firstRuntimeImageDigest() string {
	raw, err := os.ReadFile(filepath.Join(managedRuntimeDir(), "IMAGE_DIGEST"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.Contains(line, "@sha256:") {
			return line
		}
	}
	return ""
}

func ensureRuntimeImageDigest(args cliArgs) error {
	if isAbraSourceCheckout(".") {
		return nil
	}
	digest := firstRuntimeImageDigest()
	if digest == "" {
		return nil
	}
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return err
	}
	current := strings.TrimSpace(values["ABRA_IMAGE"])
	if current == "" || strings.Contains(current, "@sha256:") || current == digest {
		return nil
	}
	if !strings.HasPrefix(current, "ghcr.io/hermawan22/abra:") {
		return nil
	}
	return updateEnvValues(args, map[string]string{"ABRA_IMAGE": digest})
}
