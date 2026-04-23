package hookctx

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestShellSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple tokens",
			input: "git status",
			want:  []string{"git", "status"},
		},
		{
			name:  "double quotes",
			input: `git -C "../other repo" status`,
			want:  []string{"git", "-C", "../other repo", "status"},
		},
		{
			name:  "single quotes",
			input: "echo 'hello world'",
			want:  []string{"echo", "hello world"},
		},
		{
			name:  "escaped space",
			input: `echo hello\ world`,
			want:  []string{"echo", "hello world"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "tabs and newlines",
			input: "a\tb\nc",
			want:  []string{"a", "b", "c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shellSplit(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("shellSplit(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLooksLikePathToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		token string
		want  bool
	}{
		{".", true},
		{"..", true},
		{"/usr/bin", true},
		{"./local", true},
		{"../parent", true},
		{"~/docs", true},
		{"git", false},
		{"--verbose", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			t.Parallel()
			if got := looksLikePathToken(tt.token); got != tt.want {
				t.Fatalf("looksLikePathToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

func TestUniqueNonEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "dedup",
			input: []string{"a", "b", "a", "c", "b"},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "empty strings removed",
			input: []string{"a", "", "b", ""},
			want:  []string{"a", "b"},
		},
		{
			name:  "nil input",
			input: nil,
			want:  []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := uniqueNonEmpty(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("uniqueNonEmpty(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractBashPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cwd     string
		command string
		wantLen int
	}{
		{
			name:    "git -C path",
			cwd:     "/tmp/repo",
			command: `git -C "../other" status`,
			wantLen: 1,
		},
		{
			name:    "git -Cpath (inline)",
			cwd:     "/tmp/repo",
			command: `git -C../other status --file=/tmp/x`,
			wantLen: 2,
		},
		{
			name:    "absolute path",
			cwd:     "/tmp/repo",
			command: `cat /etc/passwd`,
			wantLen: 1,
		},
		{
			name:    "relative path",
			cwd:     "/tmp/repo",
			command: `cat ./README.md`,
			wantLen: 1,
		},
		{
			name:    "no paths",
			cwd:     "/tmp/repo",
			command: `echo hello`,
			wantLen: 0,
		},
		{
			name:    "empty command",
			cwd:     "/tmp/repo",
			command: "",
			wantLen: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractBashPaths(tt.cwd, tt.command)
			if len(got) != tt.wantLen {
				t.Fatalf("extractBashPaths(%q, %q) got %d paths %v, want %d", tt.cwd, tt.command, len(got), got, tt.wantLen)
			}
		})
	}
}

func TestExpandPaths(t *testing.T) {
	t.Parallel()

	// "absolute path" depends on OS semantics: filepath.IsAbs returns false
	// for "/usr/bin/foo" on Windows, so we feed an OS-native absolute path
	// instead (mirrors Go stdlib's TestClean / TestJoin approach of swapping
	// winXxxtests for OS-specific cases).
	absIn, absWant := "/usr/bin/foo", "/usr/bin/foo"
	if runtime.GOOS == "windows" {
		absIn, absWant = `C:\usr\bin\foo`, `C:\usr\bin\foo`
	}

	tests := []struct {
		name   string
		cwd    string
		values []string
		want   []string // forward-slash form; converted to OS-native via filepath.FromSlash below
	}{
		{
			name:   "absolute path",
			cwd:    "/cwd",
			values: []string{absIn},
			want:   []string{absWant},
		},
		{
			name:   "relative path",
			cwd:    "/cwd",
			values: []string{"sub/file.go"},
			want:   []string{"/cwd/sub/file.go"},
		},
		{
			name:   "empty value skipped",
			cwd:    "/cwd",
			values: []string{"", "a.go"},
			want:   []string{"/cwd/a.go"},
		},
		{
			name:   "no cwd",
			cwd:    "",
			values: []string{"relative"},
			want:   []string{"relative"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := expandPaths(tt.cwd, tt.values...)
			want := make([]string, len(tt.want))
			for i, w := range tt.want {
				want[i] = filepath.FromSlash(w)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("expandPaths(%q, %v) = %v, want %v", tt.cwd, tt.values, got, want)
			}
		})
	}
}
