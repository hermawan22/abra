package ingest

import "testing"

func TestMatchPathIncludesAndExcludes(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		include []string
		exclude []string
		want    bool
	}{
		{
			name:    "double star includes root markdown",
			path:    "README.md",
			include: []string{"**/*.md"},
			want:    true,
		},
		{
			name:    "double star includes nested markdown",
			path:    "docs/adr/001.md",
			include: []string{"**/*.md"},
			want:    true,
		},
		{
			name:    "exclude segment wins",
			path:    "docs/vendor/notes.md",
			include: []string{"docs/**/*.md"},
			exclude: []string{"vendor"},
			want:    false,
		},
		{
			name:    "directory suffix excludes subtree",
			path:    "private/notes/decision.md",
			include: []string{"**/*.md"},
			exclude: []string{"private/"},
			want:    false,
		},
		{
			name:    "include miss",
			path:    "src/app.ts",
			include: []string{"docs/**/*.md"},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchPath(tt.path, tt.include, tt.exclude); got != tt.want {
				t.Fatalf("MatchPath() = %v, want %v", got, tt.want)
			}
		})
	}
}
