package ingest

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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

func NewLocalRepoMarkdownIngestor(source SourceSpec) (*LocalRepoMarkdownIngestor, error) {
	if source.Type == "" {
		source.Type = SourceTypeLocalRepo
	}
	if err := source.Validate(); err != nil {
		return nil, err
	}
	return &LocalRepoMarkdownIngestor{Source: cloneSource(source)}, nil
}

func (i *LocalRepoMarkdownIngestor) Ingest(ctx context.Context) ([]Document, error) {
	root, err := filepath.Abs(i.Source.Root)
	if err != nil {
		return nil, err
	}
	git := sourceGitIdentity(i.Source, root)

	var docs []Document
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
		return nil, err
	}

	sort.Slice(docs, func(a, b int) bool {
		return docs[a].Path < docs[b].Path
	})
	return docs, nil
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
