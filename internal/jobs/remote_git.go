package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hermawan22/abra/internal/ingest"
)

func (r *Runner) prepareIngestSpec(ctx context.Context, spec ingest.SourceSpec) (ingest.SourceSpec, error) {
	if spec.Type != ingest.SourceTypeGitRepo || strings.TrimSpace(spec.GitRemoteURL) == "" {
		return spec, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return ingest.SourceSpec{}, fmt.Errorf("git executable is required for git_repo ingestion: %w", err)
	}
	if err := os.MkdirAll(r.options.GitCacheDir, 0o755); err != nil {
		return ingest.SourceSpec{}, fmt.Errorf("prepare git cache: %w", err)
	}

	cacheDir := filepath.Join(r.options.GitCacheDir, gitCacheKey(spec))
	depth := spec.GitDepth
	if depth <= 0 {
		depth = r.options.GitCloneDepth
	}
	if depth <= 0 {
		depth = DefaultGitCloneDepth
	}
	if err := checkoutGitRepo(ctx, cacheDir, spec.GitRemoteURL, spec.GitRef, depth); err != nil {
		return ingest.SourceSpec{}, err
	}

	revision, err := gitOutput(ctx, cacheDir, "rev-parse", "HEAD")
	if err != nil {
		return ingest.SourceSpec{}, err
	}
	if strings.TrimSpace(spec.GitRef) == "" {
		if ref, refErr := gitOutput(ctx, cacheDir, "rev-parse", "--abbrev-ref", "HEAD"); refErr == nil && ref != "HEAD" {
			spec.GitRef = ref
		}
	}
	spec.Root = cacheDir
	spec.GitRevision = strings.TrimSpace(revision)
	if spec.GitProvider == "" {
		spec.GitProvider = providerFromRemote(spec.GitRemoteURL)
	}
	if spec.Metadata == nil {
		spec.Metadata = map[string]string{}
	}
	spec.Metadata["git_cache_key"] = filepath.Base(cacheDir)
	return spec, nil
}

func checkoutGitRepo(ctx context.Context, cacheDir, remoteURL, ref string, depth int) error {
	if isGitCheckout(cacheDir) {
		if _, err := gitOutput(ctx, cacheDir, "remote", "set-url", "origin", remoteURL); err != nil {
			return err
		}
		fetchArgs := []string{"fetch", "--prune", "--depth", strconv.Itoa(depth), "origin"}
		if strings.TrimSpace(ref) != "" {
			fetchArgs = append(fetchArgs, ref)
		}
		if _, err := gitOutput(ctx, cacheDir, fetchArgs...); err != nil {
			return err
		}
		_, err := gitOutput(ctx, cacheDir, "checkout", "--force", "FETCH_HEAD")
		return err
	}

	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("reset git cache: %w", err)
	}
	args := []string{"clone", "--depth", strconv.Itoa(depth)}
	if strings.TrimSpace(ref) != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, remoteURL, cacheDir)
	if _, err := gitOutput(ctx, "", args...); err != nil {
		return err
	}
	return nil
}

func isGitCheckout(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("git %s failed: %s", safeGitOperation(args), redactGitSecrets(text))
	}
	return text, nil
}

func gitCacheKey(spec ingest.SourceSpec) string {
	sum := sha256.Sum256([]byte(spec.ID + "\x00" + spec.GitRemoteURL + "\x00" + spec.GitRef))
	prefix := safeCachePrefix(spec.ID)
	if prefix == "" {
		prefix = "source"
	}
	return prefix + "-" + hex.EncodeToString(sum[:])[:16]
}

func safeCachePrefix(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '-' || r == '_':
			out.WriteRune(r)
		default:
			out.WriteByte('-')
		}
		if out.Len() >= 48 {
			break
		}
	}
	return strings.Trim(out.String(), "-_")
}

func safeGitOperation(args []string) string {
	if len(args) == 0 {
		return "command"
	}
	switch args[0] {
	case "clone", "fetch", "pull", "checkout", "remote", "rev-parse":
		return args[0]
	default:
		return "command"
	}
}

func redactGitSecrets(text string) string {
	fields := strings.Fields(text)
	for i, field := range fields {
		fields[i] = redactURLCredentials(field)
	}
	return strings.Join(fields, " ")
}

func redactURLCredentials(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.User == nil {
		return value
	}
	u.User = url.User("redacted")
	return u.String()
}

func providerFromRemote(remote string) string {
	web := remoteWebURLForProvider(remote)
	if web == "" {
		return "generic"
	}
	u, err := url.Parse(web)
	if err != nil {
		return "generic"
	}
	host := strings.ToLower(u.Host)
	switch {
	case strings.Contains(host, "github"):
		return "github"
	case strings.Contains(host, "gitlab"):
		return "gitlab"
	case strings.Contains(host, "bitbucket"):
		return "bitbucket"
	default:
		return "generic"
	}
}

func remoteWebURLForProvider(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if strings.HasPrefix(remote, "git@") {
		hostPath := strings.TrimPrefix(remote, "git@")
		host, path, ok := strings.Cut(hostPath, ":")
		if !ok {
			return ""
		}
		return "https://" + host + "/" + strings.TrimSuffix(path, ".git")
	}
	u, err := url.Parse(remote)
	if err != nil || u.Host == "" {
		return ""
	}
	u.Scheme = "https"
	u.User = nil
	u.Path = strings.TrimSuffix(u.Path, ".git")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
