package ingest

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const DefaultMaxFileBytes int64 = 1 << 20

type Document struct {
	SourceID    string
	SourceType  SourceType
	SourceURL   string
	Path        string
	Title       string
	Scope       string
	Content     string
	Checksum    string
	Fingerprint string
	Metadata    map[string]string
}

type LocalRepoMarkdownIngestor struct {
	Source SourceSpec
}

type IngestResult struct {
	Documents []Document
	Skipped   []SkippedFile
}

type SkippedFile struct {
	Path   string
	Reason string
	Bytes  int64
}

func NewLocalRepoMarkdownIngestor(source SourceSpec) (*LocalRepoMarkdownIngestor, error) {
	if source.Type == "" {
		source.Type = SourceTypeLocalRepo
	}
	if err := source.Validate(); err != nil {
		return nil, err
	}
	return &LocalRepoMarkdownIngestor{Source: cloneSource(source)}, nil
}

func (i *LocalRepoMarkdownIngestor) IngestWithStats(ctx context.Context) (IngestResult, error) {
	root, err := filepath.Abs(i.Source.Root)
	if err != nil {
		return IngestResult{}, err
	}
	git := sourceGitIdentity(i.Source, root)
	maxFileBytes := i.Source.MaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = DefaultMaxFileBytes
	}

	var docs []Document
	var skipped []SkippedFile
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		rel = normalizePath(rel)
		if rel == "" {
			return nil
		}

		if entry.IsDir() {
			if defaultSkipDir(rel) {
				return filepath.SkipDir
			}
			for _, pattern := range i.Source.Exclude {
				if MatchPattern(pattern, rel) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		contentKind := ""
		if isMarkdownPath(rel) && MatchPath(rel, i.Source.Include, i.Source.Exclude) {
			contentKind = "markdown"
		} else if i.Source.IncludeCode && isCodePath(rel) && MatchPath(rel, codeInclude(i.Source), append(i.Source.Exclude, i.Source.CodeExclude...)) {
			contentKind = "code"
		}
		if contentKind == "" {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxFileBytes {
			skipped = append(skipped, SkippedFile{Path: rel, Reason: "too_large", Bytes: info.Size()})
			return nil
		}
		generated, err := generatedFile(filePath, rel)
		if err != nil {
			return err
		}
		if generated && !i.Source.IncludeGenerated {
			skipped = append(skipped, SkippedFile{Path: rel, Reason: "generated", Bytes: info.Size()})
			return nil
		}
		binary, err := binaryFile(filePath)
		if err != nil {
			return err
		}
		if binary {
			skipped = append(skipped, SkippedFile{Path: rel, Reason: "binary", Bytes: info.Size()})
			return nil
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		checksum := Checksum(content)
		repoPath := repoRelativePath(git, root, rel)
		metadata := cloneMap(i.Source.Metadata)
		for key, value := range gitDocumentMetadata(git, repoPath) {
			metadata[key] = value
		}
		metadata["content_kind"] = contentKind
		if contentKind == "code" {
			metadata["language"] = languageForPath(rel)
		}
		sourceURL := gitFileURL(git, repoPath)
		if sourceURL == "" {
			sourceURL = fileURL(filePath)
		}
		doc := Document{
			SourceID:    i.Source.ID,
			SourceType:  i.Source.Type,
			SourceURL:   sourceURL,
			Path:        rel,
			Title:       documentTitle(rel, string(content), contentKind),
			Scope:       i.Source.Scope,
			Content:     string(content),
			Checksum:    checksum,
			Fingerprint: Fingerprint(i.Source.ID, rel, checksum),
			Metadata:    metadata,
		}
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		return IngestResult{}, err
	}

	sort.Slice(docs, func(a, b int) bool {
		return docs[a].Path < docs[b].Path
	})
	sort.Slice(skipped, func(a, b int) bool {
		return skipped[a].Path < skipped[b].Path
	})
	return IngestResult{Documents: docs, Skipped: skipped}, nil
}

func isMarkdownPath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md", ".markdown", ".mdown":
		return true
	default:
		return false
	}
}

func isCodePath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".json":
		return true
	default:
		return false
	}
}

func codeInclude(source SourceSpec) []string {
	if len(source.CodeInclude) > 0 {
		return source.CodeInclude
	}
	return []string{
		"package.json",
		"**/*.go",
		"**/*.js",
		"**/*.jsx",
		"**/*.ts",
		"**/*.tsx",
	}
}

func defaultSkipDir(rel string) bool {
	switch filepath.Base(rel) {
	case ".git", ".cache", ".next", ".turbo", "build", "coverage", "dist", "node_modules", "target", "vendor":
		return true
	default:
		return false
	}
}

func generatedFile(path, rel string) (bool, error) {
	if generatedPath(rel) {
		return true, nil
	}
	sample, err := readSample(path)
	if err != nil {
		return false, err
	}
	return generatedMarker(sample), nil
}

func generatedPath(rel string) bool {
	normalized := normalizePath(rel)
	base := strings.ToLower(filepath.Base(normalized))
	if strings.Contains(normalized, "/generated/") || strings.Contains(normalized, "/gen/") || strings.Contains(normalized, "/__generated__/") {
		return true
	}
	switch {
	case strings.HasSuffix(base, ".min.js"),
		strings.HasSuffix(base, ".min.css"),
		strings.HasSuffix(base, ".generated.go"),
		strings.HasSuffix(base, "_generated.go"),
		strings.HasSuffix(base, ".pb.go"),
		strings.HasSuffix(base, "_pb.go"),
		strings.HasSuffix(base, ".pb.gw.go"),
		base == "package-lock.json",
		base == "yarn.lock",
		base == "pnpm-lock.yaml":
		return true
	default:
		return false
	}
}

func binaryFile(path string) (bool, error) {
	buf, err := readSample(path)
	if err != nil {
		return false, err
	}
	if len(buf) == 0 {
		return false, nil
	}
	if strings.ContainsRune(string(buf), '\x00') {
		return true, nil
	}
	return !utf8.Valid(buf), nil
}

func readSample(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buf := make([]byte, 8192)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

func generatedMarker(sample []byte) bool {
	text := strings.ToLower(string(sample))
	return strings.Contains(text, "code generated") && strings.Contains(text, "do not edit") ||
		strings.Contains(text, "@generated") ||
		strings.Contains(text, "<auto-generated") ||
		strings.Contains(text, "this file was generated") && strings.Contains(text, "do not edit")
}

func languageForPath(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".go":
		return "go"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".json":
		return "json"
	default:
		return ""
	}
}

func documentTitle(relPath, content, contentKind string) string {
	if contentKind == "code" {
		return relPath
	}
	return markdownTitle(relPath, content)
}

func markdownTitle(relPath, content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			title := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if title != "" {
				return title
			}
		}
	}
	base := filepath.Base(relPath)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func fileURL(filePath string) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}

func (d Document) String() string {
	return fmt.Sprintf("%s %s %s", d.SourceID, d.Path, d.Checksum)
}
