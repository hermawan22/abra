package ingest

import (
	"bufio"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type gitIdentity struct {
	RemoteURL   string
	Ref         string
	Revision    string
	Provider    string
	ProjectPath string
	Root        string
}

func sourceGitIdentity(source SourceSpec, root string) gitIdentity {
	identity := inspectGitIdentity(root)
	if source.GitRemoteURL != "" {
		identity.RemoteURL = strings.TrimSpace(source.GitRemoteURL)
	}
	if source.GitRef != "" {
		identity.Ref = strings.TrimSpace(source.GitRef)
	}
	if source.GitRevision != "" {
		identity.Revision = strings.TrimSpace(source.GitRevision)
	}
	if source.GitProvider != "" {
		identity.Provider = strings.TrimSpace(source.GitProvider)
	}
	if source.GitProjectPath != "" {
		identity.ProjectPath = strings.Trim(strings.TrimSpace(source.GitProjectPath), "/")
	}
	return identity
}

func inspectGitIdentity(root string) gitIdentity {
	repoRoot, gitDir := findGitDir(root)
	if gitDir == "" {
		return gitIdentity{}
	}

	identity := gitIdentity{
		Root:      repoRoot,
		RemoteURL: gitRemoteURL(filepath.Join(gitDir, "config")),
	}
	head := strings.TrimSpace(readText(filepath.Join(gitDir, "HEAD")))
	if strings.HasPrefix(head, "ref: ") {
		refPath := strings.TrimSpace(strings.TrimPrefix(head, "ref: "))
		identity.Ref = shortGitRef(refPath)
		identity.Revision = gitRefRevision(gitDir, refPath)
	} else if isLikelySHA(head) {
		identity.Revision = head
	}
	return identity
}

func findGitDir(start string) (string, string) {
	dir := start
	for {
		candidate := filepath.Join(dir, ".git")
		if info, err := os.Stat(candidate); err == nil {
			if info.IsDir() {
				return dir, candidate
			}
			if text := readText(candidate); strings.HasPrefix(strings.TrimSpace(text), "gitdir:") {
				gitDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "gitdir:"))
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(dir, gitDir)
				}
				return dir, filepath.Clean(gitDir)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

func gitRemoteURL(configPath string) string {
	file, err := os.Open(configPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	inOrigin := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if !inOrigin || !strings.HasPrefix(line, "url") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "url" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func gitRefRevision(gitDir, refPath string) string {
	if revision := strings.TrimSpace(readText(filepath.Join(gitDir, filepath.FromSlash(refPath)))); revision != "" {
		return revision
	}
	file, err := os.Open(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == refPath {
			return fields[0]
		}
	}
	return ""
}

func gitDocumentMetadata(identity gitIdentity, repoPath string) map[string]string {
	metadata := map[string]string{}
	put := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			metadata[key] = strings.TrimSpace(value)
		}
	}
	put("git_remote_url", identity.RemoteURL)
	put("git_ref", identity.Ref)
	put("git_revision", identity.Revision)
	put("git_provider", identity.Provider)
	put("git_project_path", identity.ProjectPath)
	put("git_path", repoPath)
	put("git_root", identity.Root)
	return metadata
}

func gitFileURL(identity gitIdentity, repoPath string) string {
	remote := strings.TrimSpace(identity.RemoteURL)
	if remote == "" || repoPath == "" {
		return ""
	}
	webURL := remoteWebURL(remote)
	if webURL == "" {
		return ""
	}
	ref := identity.Revision
	if ref == "" {
		ref = identity.Ref
	}
	if ref == "" {
		ref = "HEAD"
	}
	provider := strings.ToLower(identity.Provider)
	if provider == "" {
		provider = remoteProvider(webURL)
	}
	escapedPath := escapePath(repoPath)
	switch provider {
	case "github":
		return strings.TrimSuffix(webURL, "/") + "/blob/" + url.PathEscape(ref) + "/" + escapedPath
	case "gitlab":
		return strings.TrimSuffix(webURL, "/") + "/-/blob/" + url.PathEscape(ref) + "/" + escapedPath
	case "bitbucket":
		return strings.TrimSuffix(webURL, "/") + "/src/" + url.PathEscape(ref) + "/" + escapedPath
	default:
		return strings.TrimSuffix(webURL, "/") + "?ref=" + url.QueryEscape(ref) + "&path=" + url.QueryEscape(repoPath)
	}
}

func repoRelativePath(identity gitIdentity, root, rel string) string {
	if identity.Root == "" {
		return rel
	}
	prefix, err := filepath.Rel(identity.Root, root)
	if err != nil || prefix == "." {
		return rel
	}
	return normalizePath(filepath.Join(prefix, rel))
}

func remoteWebURL(remote string) string {
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
	if strings.HasPrefix(remote, "ssh://git@") {
		u, err := url.Parse(remote)
		if err != nil {
			return ""
		}
		return "https://" + u.Host + "/" + strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
	}
	u, err := url.Parse(remote)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Scheme = "https"
	u.Path = strings.TrimSuffix(u.Path, ".git")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func remoteProvider(webURL string) string {
	u, err := url.Parse(webURL)
	if err != nil {
		return ""
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

func shortGitRef(ref string) string {
	ref = strings.TrimSpace(ref)
	for _, prefix := range []string{"refs/heads/", "refs/tags/", "refs/remotes/origin/"} {
		if strings.HasPrefix(ref, prefix) {
			return strings.TrimPrefix(ref, prefix)
		}
	}
	return ref
}

func escapePath(path string) string {
	parts := strings.Split(normalizePath(path), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func isLikelySHA(value string) bool {
	if len(value) < 7 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F') {
			continue
		}
		return false
	}
	return true
}

func readText(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}
