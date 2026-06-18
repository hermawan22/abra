package graph

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type CodeFile struct {
	Path      string
	Content   string
	SourceID  string
	SourceURL string
}

var (
	importFromRE         = regexp.MustCompile(`(?m)\bimport\s+(?:type\s+)?(?:[^;"']+?\s+from\s+)?["']([^"']+)["']`)
	requireRE            = regexp.MustCompile(`(?m)\brequire\(\s*["']([^"']+)["']\s*\)`)
	exportNamedRE        = regexp.MustCompile(`(?m)\bexport\s+(?:async\s+)?(?:function|class|const|let|var|interface|type|enum)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	exportDefaultNamedRE = regexp.MustCompile(`(?m)\bexport\s+default\s+(?:async\s+)?(?:function|class)?\s*([A-Za-z_$][A-Za-z0-9_$]*)?`)
	functionComponentRE  = regexp.MustCompile(`(?m)\b(?:function|const)\s+([A-Z][A-Za-z0-9_$]*)\b`)
)

// ExtractCodeFile returns deterministic graph candidates from a single source file.
// It intentionally uses cheap static extraction so ingestion stays predictable.
func ExtractCodeFile(file CodeFile) CandidateSet {
	path := normalizeCodePath(file.Path)
	content := strings.TrimSpace(file.Content)
	if path == "" || content == "" || !IsCodeGraphPath(path) {
		return CandidateSet{}
	}

	entities := map[string]*EntityCandidate{}
	relations := map[string]RelationCandidate{}
	add := func(entity EntityCandidate) {
		if entity.Name == "" {
			return
		}
		key := strings.ToLower(entity.Type + "\x00" + entity.Name)
		if existing, ok := entities[key]; ok {
			existing.Mentions += max(entity.Mentions, 1)
			return
		}
		if entity.Mentions == 0 {
			entity.Mentions = 1
		}
		entities[key] = &entity
	}
	rel := func(from, to, typ, evidence string, confidence float64) {
		if from == "" || to == "" || from == to {
			return
		}
		key := strings.ToLower(from + "\x00" + typ + "\x00" + to)
		if _, ok := relations[key]; ok {
			return
		}
		relations[key] = RelationCandidate{
			From:       from,
			To:         to,
			Type:       typ,
			Evidence:   evidence,
			SourceID:   file.SourceID,
			SourceURL:  file.SourceURL,
			Confidence: confidence,
		}
	}

	add(EntityCandidate{Name: path, Type: "file", Mentions: 1})
	if filepath.Ext(path) == ".go" {
		return extractGoCodeFile(file, path, content, entities, relations)
	}
	if route := nextRouteFromPath(path); route != "" {
		add(EntityCandidate{Name: route, Type: "route", Mentions: 1})
		rel(route, path, "implemented_by", path, 0.82)
	}

	if filepath.Base(path) == "package.json" {
		for _, dep := range packageDependencies(content) {
			add(EntityCandidate{Name: dep, Type: "package", Mentions: 1})
			rel(path, dep, "depends_on", "package.json dependency", 0.78)
		}
	}

	for _, module := range importedModules(content) {
		target := resolveImport(path, module)
		entityType := "package"
		if isRelativeImport(module) {
			entityType = "file"
		}
		add(EntityCandidate{Name: target, Type: entityType, Mentions: 1})
		rel(path, target, "imports", "import "+module, 0.75)
	}

	for _, symbol := range exportedSymbols(content, path) {
		add(EntityCandidate{Name: symbol, Type: "symbol", Mentions: 1})
		rel(path, symbol, "exports", "export "+symbol, 0.74)
	}

	for _, component := range componentSymbols(content) {
		add(EntityCandidate{Name: component, Type: "component", Mentions: 1})
		rel(path, component, "defines_component", "component "+component, 0.7)
	}

	return candidateSetFromMaps(entities, relations)
}

func IsCodeGraphPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".json":
		return true
	default:
		return false
	}
}

func extractGoCodeFile(file CodeFile, path, content string, entities map[string]*EntityCandidate, relations map[string]RelationCandidate) CandidateSet {
	add := func(entity EntityCandidate) {
		if entity.Name == "" {
			return
		}
		key := strings.ToLower(entity.Type + "\x00" + entity.Name)
		if existing, ok := entities[key]; ok {
			existing.Mentions += max(entity.Mentions, 1)
			return
		}
		if entity.Mentions == 0 {
			entity.Mentions = 1
		}
		entities[key] = &entity
	}
	rel := func(from, to, typ, evidence string, confidence float64) {
		if from == "" || to == "" || from == to {
			return
		}
		key := strings.ToLower(from + "\x00" + typ + "\x00" + to)
		if _, ok := relations[key]; ok {
			return
		}
		relations[key] = RelationCandidate{
			From:       from,
			To:         to,
			Type:       typ,
			Evidence:   evidence,
			SourceID:   file.SourceID,
			SourceURL:  file.SourceURL,
			Confidence: confidence,
		}
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), path, content, 0)
	if err != nil && parsed == nil {
		return candidateSetFromMaps(entities, relations)
	}
	if parsed.Name != nil {
		packageName := "go:" + parsed.Name.Name
		add(EntityCandidate{Name: packageName, Type: "package", Mentions: 1})
		rel(path, packageName, "declares_package", "package "+parsed.Name.Name, 0.82)
	}
	for _, spec := range parsed.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		if importPath == "" {
			continue
		}
		add(EntityCandidate{Name: importPath, Type: "package", Mentions: 1})
		rel(path, importPath, "imports", "import "+importPath, 0.78)
	}
	for _, decl := range parsed.Decls {
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			name := goFuncSymbol(typed)
			if name == "" {
				continue
			}
			add(EntityCandidate{Name: name, Type: "symbol", Mentions: 1})
			rel(path, name, "defines_symbol", "func "+name, 0.76)
			if ast.IsExported(typed.Name.Name) {
				rel(path, name, "exports", "export "+name, 0.74)
			}
		case *ast.GenDecl:
			for _, spec := range typed.Specs {
				for _, name := range goSpecSymbols(spec) {
					add(EntityCandidate{Name: name, Type: "symbol", Mentions: 1})
					rel(path, name, "defines_symbol", strings.ToLower(typed.Tok.String())+" "+name, 0.74)
					if ast.IsExported(name) {
						rel(path, name, "exports", "export "+name, 0.72)
					}
				}
			}
		}
	}
	return candidateSetFromMaps(entities, relations)
}

func goFuncSymbol(decl *ast.FuncDecl) string {
	if decl == nil || decl.Name == nil {
		return ""
	}
	name := decl.Name.Name
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return name
	}
	receiver := goReceiverName(decl.Recv.List[0].Type)
	if receiver == "" {
		return name
	}
	return receiver + "." + name
}

func goReceiverName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return goReceiverName(typed.X)
	case *ast.IndexExpr:
		return goReceiverName(typed.X)
	case *ast.IndexListExpr:
		return goReceiverName(typed.X)
	default:
		return ""
	}
}

func goSpecSymbols(spec ast.Spec) []string {
	switch typed := spec.(type) {
	case *ast.TypeSpec:
		if typed.Name == nil {
			return nil
		}
		return []string{typed.Name.Name}
	case *ast.ValueSpec:
		out := make([]string, 0, len(typed.Names))
		for _, name := range typed.Names {
			if name != nil && name.Name != "_" {
				out = append(out, name.Name)
			}
		}
		return out
	default:
		return nil
	}
}

func importedModules(content string) []string {
	seen := map[string]struct{}{}
	for _, re := range []*regexp.Regexp{importFromRE, requireRE} {
		for _, match := range re.FindAllStringSubmatch(content, -1) {
			if len(match) > 1 {
				module := strings.TrimSpace(match[1])
				if module != "" {
					seen[module] = struct{}{}
				}
			}
		}
	}
	return sortedKeys(seen)
}

func exportedSymbols(content, path string) []string {
	seen := map[string]struct{}{}
	for _, match := range exportNamedRE.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && match[1] != "" {
			seen[match[1]] = struct{}{}
		}
	}
	for _, match := range exportDefaultNamedRE.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && match[1] != "" {
			seen[match[1]] = struct{}{}
			continue
		}
		if base := symbolFromFile(path); base != "" {
			seen[base] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func componentSymbols(content string) []string {
	seen := map[string]struct{}{}
	for _, match := range functionComponentRE.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && match[1] != "" {
			seen[match[1]] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func packageDependencies(content string) []string {
	var raw struct {
		Dependencies         map[string]any `json:"dependencies"`
		DevDependencies      map[string]any `json:"devDependencies"`
		PeerDependencies     map[string]any `json:"peerDependencies"`
		OptionalDependencies map[string]any `json:"optionalDependencies"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, deps := range []map[string]any{raw.Dependencies, raw.DevDependencies, raw.PeerDependencies, raw.OptionalDependencies} {
		for name := range deps {
			seen[name] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func resolveImport(fromPath, module string) string {
	module = strings.TrimSpace(module)
	if !isRelativeImport(module) {
		return module
	}
	base := filepath.Dir(fromPath)
	return normalizeCodePath(filepath.Clean(filepath.Join(base, module)))
}

func isRelativeImport(module string) bool {
	return strings.HasPrefix(module, "./") || strings.HasPrefix(module, "../")
}

func nextRouteFromPath(path string) string {
	if !strings.HasPrefix(path, "src/pages/") {
		return ""
	}
	route := strings.TrimPrefix(path, "src/pages/")
	ext := filepath.Ext(route)
	route = strings.TrimSuffix(route, ext)
	for _, internal := range []string{"_app", "_document", "_error"} {
		if route == internal {
			return ""
		}
	}
	route = strings.ReplaceAll(route, "/index", "")
	parts := strings.Split(route, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]") {
			parts[i] = ":" + strings.TrimSuffix(strings.TrimPrefix(part, "["), "]")
		}
	}
	route = "/" + strings.Join(parts, "/")
	route = strings.ReplaceAll(route, "//", "/")
	if route == "/" {
		return "/"
	}
	return strings.TrimSuffix(route, "/")
}

func symbolFromFile(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if base == "" || base == "index" {
		base = filepath.Base(filepath.Dir(path))
	}
	parts := regexp.MustCompile(`[^A-Za-z0-9]+`).Split(base, -1)
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		out += strings.ToUpper(part[:1]) + part[1:]
	}
	return out
}

func normalizeCodePath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "./")
	return strings.Trim(path, "/")
}

func candidateSetFromMaps(entities map[string]*EntityCandidate, relations map[string]RelationCandidate) CandidateSet {
	out := CandidateSet{
		Entities:  make([]EntityCandidate, 0, len(entities)),
		Relations: make([]RelationCandidate, 0, len(relations)),
	}
	for _, entity := range entities {
		out.Entities = append(out.Entities, *entity)
	}
	for _, relation := range relations {
		out.Relations = append(out.Relations, relation)
	}
	sort.Slice(out.Entities, func(a, b int) bool {
		left := out.Entities[a].Type + "\x00" + strings.ToLower(out.Entities[a].Name)
		right := out.Entities[b].Type + "\x00" + strings.ToLower(out.Entities[b].Name)
		return left < right
	})
	sort.Slice(out.Relations, func(a, b int) bool {
		return relationSortKey(out.Relations[a]) < relationSortKey(out.Relations[b])
	})
	return out
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
