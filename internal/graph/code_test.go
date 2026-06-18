package graph

import (
	"reflect"
	"testing"
)

func TestExtractCodeFileFindsImportsExportsComponentsAndRoute(t *testing.T) {
	got := ExtractCodeFile(CodeFile{
		Path:      "src/pages/users/[id]/index.tsx",
		SourceID:  "doc-1",
		SourceURL: "file://repo/src/pages/users/[id]/index.tsx",
		Content: `
import React from 'react';
import { Button } from 'example-ui-kit';
import helper from '../../../helpers/user';
const UserDetail = () => <Button />;
export default UserDetail;
export function getServerSideProps() { return {}; }
`,
	})

	wantRelations := []RelationCandidate{
		{From: "/users/:id", To: "src/pages/users/[id]/index.tsx", Type: "implemented_by", Evidence: "src/pages/users/[id]/index.tsx", SourceID: "doc-1", SourceURL: "file://repo/src/pages/users/[id]/index.tsx", Confidence: 0.82},
		{From: "src/pages/users/[id]/index.tsx", To: "UserDetail", Type: "defines_component", Evidence: "component UserDetail", SourceID: "doc-1", SourceURL: "file://repo/src/pages/users/[id]/index.tsx", Confidence: 0.7},
		{From: "src/pages/users/[id]/index.tsx", To: "getServerSideProps", Type: "exports", Evidence: "export getServerSideProps", SourceID: "doc-1", SourceURL: "file://repo/src/pages/users/[id]/index.tsx", Confidence: 0.74},
		{From: "src/pages/users/[id]/index.tsx", To: "UserDetail", Type: "exports", Evidence: "export UserDetail", SourceID: "doc-1", SourceURL: "file://repo/src/pages/users/[id]/index.tsx", Confidence: 0.74},
		{From: "src/pages/users/[id]/index.tsx", To: "example-ui-kit", Type: "imports", Evidence: "import example-ui-kit", SourceID: "doc-1", SourceURL: "file://repo/src/pages/users/[id]/index.tsx", Confidence: 0.75},
		{From: "src/pages/users/[id]/index.tsx", To: "react", Type: "imports", Evidence: "import react", SourceID: "doc-1", SourceURL: "file://repo/src/pages/users/[id]/index.tsx", Confidence: 0.75},
		{From: "src/pages/users/[id]/index.tsx", To: "src/helpers/user", Type: "imports", Evidence: "import ../../../helpers/user", SourceID: "doc-1", SourceURL: "file://repo/src/pages/users/[id]/index.tsx", Confidence: 0.75},
	}
	if !reflect.DeepEqual(got.Relations, wantRelations) {
		t.Fatalf("relations = %#v, want %#v", got.Relations, wantRelations)
	}
}

func TestExtractCodeFileFindsPackageDependencies(t *testing.T) {
	got := ExtractCodeFile(CodeFile{
		Path: "package.json",
		Content: `{
		  "dependencies": {"next": "16.2.9", "react": "18.3.1"},
		  "devDependencies": {"jest": "28.1.0"}
		}`,
	})
	wantRelations := []RelationCandidate{
		{From: "package.json", To: "jest", Type: "depends_on", Evidence: "package.json dependency", Confidence: 0.78},
		{From: "package.json", To: "next", Type: "depends_on", Evidence: "package.json dependency", Confidence: 0.78},
		{From: "package.json", To: "react", Type: "depends_on", Evidence: "package.json dependency", Confidence: 0.78},
	}
	if !reflect.DeepEqual(got.Relations, wantRelations) {
		t.Fatalf("relations = %#v, want %#v", got.Relations, wantRelations)
	}
}

func TestExtractCodeFileFindsGoImportsPackageAndSymbols(t *testing.T) {
	got := ExtractCodeFile(CodeFile{
		Path:      "internal/memory/composer.go",
		SourceID:  "doc-go",
		SourceURL: "file://repo/internal/memory/composer.go",
		Content: `package memory

import (
	"context"
	"github.com/hermawan22/abra/internal/store"
)

type Composer struct{}
const DefaultLimit = 6
var ErrMissing = context.Canceled

func NewComposer(store.Store) *Composer { return &Composer{} }
func (c *Composer) Compose(ctx context.Context) error { return nil }
func helper() {}
`,
	})

	wantRelations := []RelationCandidate{
		{From: "internal/memory/composer.go", To: "go:memory", Type: "declares_package", Evidence: "package memory", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.82},
		{From: "internal/memory/composer.go", To: "Composer", Type: "defines_symbol", Evidence: "type Composer", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.74},
		{From: "internal/memory/composer.go", To: "Composer.Compose", Type: "defines_symbol", Evidence: "func Composer.Compose", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.76},
		{From: "internal/memory/composer.go", To: "DefaultLimit", Type: "defines_symbol", Evidence: "const DefaultLimit", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.74},
		{From: "internal/memory/composer.go", To: "ErrMissing", Type: "defines_symbol", Evidence: "var ErrMissing", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.74},
		{From: "internal/memory/composer.go", To: "helper", Type: "defines_symbol", Evidence: "func helper", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.76},
		{From: "internal/memory/composer.go", To: "NewComposer", Type: "defines_symbol", Evidence: "func NewComposer", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.76},
		{From: "internal/memory/composer.go", To: "Composer", Type: "exports", Evidence: "export Composer", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.72},
		{From: "internal/memory/composer.go", To: "Composer.Compose", Type: "exports", Evidence: "export Composer.Compose", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.74},
		{From: "internal/memory/composer.go", To: "DefaultLimit", Type: "exports", Evidence: "export DefaultLimit", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.72},
		{From: "internal/memory/composer.go", To: "ErrMissing", Type: "exports", Evidence: "export ErrMissing", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.72},
		{From: "internal/memory/composer.go", To: "NewComposer", Type: "exports", Evidence: "export NewComposer", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.74},
		{From: "internal/memory/composer.go", To: "context", Type: "imports", Evidence: "import context", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.78},
		{From: "internal/memory/composer.go", To: "github.com/hermawan22/abra/internal/store", Type: "imports", Evidence: "import github.com/hermawan22/abra/internal/store", SourceID: "doc-go", SourceURL: "file://repo/internal/memory/composer.go", Confidence: 0.78},
	}
	if !reflect.DeepEqual(got.Relations, wantRelations) {
		t.Fatalf("relations = %#v, want %#v", got.Relations, wantRelations)
	}
}

func TestExtractCodeFileIgnoresUnsupportedPath(t *testing.T) {
	got := ExtractCodeFile(CodeFile{Path: "README.md", Content: "import x from 'y'"})
	if len(got.Entities) != 0 || len(got.Relations) != 0 {
		t.Fatalf("expected unsupported markdown to produce no code graph: %#v", got)
	}
}
