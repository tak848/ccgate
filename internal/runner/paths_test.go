package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

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
		{"~", true},
		{"$HOME/docs", true},
		{"${HOME}/docs", true},
		{"$HOME", true},
		{"~user/docs", false},
		{"$HOMEDIR/docs", false},
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

// TestExtractBashPathsFindsTargets pins which raw tokens a command
// yields. The expected paths go through expandPaths too, so the cases
// stay independent of OS path semantics and only check extraction.
func TestExtractBashPathsFindsTargets(t *testing.T) {
	t.Parallel()

	const cwd = "/tmp/repo"
	cases := map[string]struct {
		command string
		raw     []string
	}{
		"redirect without a space": {
			command: `echo hi >/etc/motd`,
			raw:     []string{"/etc/motd"},
		},
		"append redirect with fd": {
			command: `make 2>>~/build.log`,
			raw:     []string{"~/build.log"},
		},
		"redirect inside a token": {
			command: `cat ./a>/tmp/out`,
			raw:     []string{"./a", "/tmp/out"},
		},
		"fd duplication is not a path": {
			command: `go test ./... 2>&1`,
			raw:     []string{"./..."},
		},
		"home directory alone": {
			command: `rm -rf ~`,
			raw:     []string{"~"},
		},
		"HOME variable": {
			command: `rm -rf "$HOME/.ssh" ${HOME}/.aws`,
			raw:     []string{"$HOME/.ssh", "${HOME}/.aws"},
		},
		"value after = without a leading --": {
			command: `dd if=./disk.img of=/dev/sda`,
			raw:     []string{"./disk.img", "/dev/sda"},
		},
		"URL query value is not a path": {
			command: `curl 'https://example.com/api?path=/etc/passwd' -o ./out.json`,
			raw:     []string{"./out.json"},
		},
		">& with a file operand redirects to that file": {
			command: `echo hello >& /tmp/out`,
			raw:     []string{"/tmp/out"},
		},
		"descriptor operands are not files": {
			command: `cmd 2>&1 3>&- 4<&0`,
			raw:     nil,
		},
		"relative redirection target": {
			command: `echo hi > out.txt`,
			raw:     []string{"out.txt"},
		},
		"here-doc delimiter is not a file": {
			command: `cat <<EOF`,
			raw:     nil,
		},
		"quoted redirection is text": {
			command: `printf '%s\n' 'hi >/tmp/out'`,
			raw:     nil,
		},
		"quoted > inside a path keeps the path whole": {
			command: `rm -rf './a>/etc/passwd'`,
			raw:     []string{"./a>/etc/passwd"},
		},
		"single-quoted HOME is literal text": {
			command: `rg '$HOME' ./README.md`,
			raw:     []string{"./README.md"},
		},
		"quoted tilde is literal text": {
			command: `ls "~/docs"`,
			raw:     nil,
		},
		"path containing = stays one path": {
			command: `rm -rf ./cache=/etc`,
			raw:     []string{"./cache=/etc"},
		},
		"commands after ; and |": {
			command: `echo hi;rm -rf /opt/x|tee /tmp/log`,
			raw:     []string{"/opt/x", "/tmp/log"},
		},
		"subshell": {
			command: `(cd /tmp && rm -rf ./build)`,
			raw:     []string{"/tmp", "./build"},
		},
		"command substitution": {
			command: `echo $(cat /etc/hosts) > ./hosts.txt`,
			raw:     []string{"/etc/hosts", "./hosts.txt"},
		},
		"process substitution": {
			command: `diff <(sort ./a) <(sort ./b)`,
			raw:     []string{"./a", "./b"},
		},
		"here-doc body is not a command": {
			command: "cat <<EOF > out.txt\n> /etc/passwd\nEOF",
			raw:     []string{"out.txt"},
		},
		"commit message from a here-doc is not a path": {
			command: "git commit -m \"$(cat <<'EOF'\nfix: handle /etc/hosts lookup\nEOF\n)\"",
			raw:     nil,
		},
		"command substitution in an unquoted here-doc runs": {
			command: "bash <<EOF\n$(rm -rf /x)\nEOF",
			raw:     []string{"/x"},
		},
		"comment is not a command": {
			command: `ls /x # rm -rf /etc`,
			raw:     []string{"/x"},
		},
		"for loop items": {
			command: `for f in ./a ./b; do rm "$f"; done`,
			raw:     []string{"./a", "./b"},
		},
		"assignments": {
			command: `PREFIX=/opt/x make install && export DEST=~/opt`,
			raw:     []string{"/opt/x", "~/opt"},
		},
		"tilde after = of an option": {
			command: `./configure --prefix=~/opt`,
			raw:     []string{"~/opt", "./configure"},
		},
		"quoted HOME after = is literal text": {
			command: `curl --output='$HOME/out' https://example.com`,
			raw:     nil,
		},
		"whole option quoted": {
			command: `curl '--output=/etc/example' "--config=$HOME/.curlrc" '--trace=$HOME/t' https://example.com`,
			raw:     []string{"/etc/example", "$HOME/.curlrc"},
		},
		"array elements": {
			command: `targets=( /etc/important ./build ); rm -rf "${targets[@]}"`,
			raw:     []string{"/etc/important", "./build"},
		},
		"test operands": {
			command: `[[ -e /etc/x ]] && echo found`,
			raw:     []string{"/etc/x"},
		},
		"value unknown before the command runs": {
			command: `rm -rf "$TMPDIR/cache" $(mktemp -d)/x`,
			raw:     nil,
		},
		"command that does not parse falls back to words": {
			command: `echo 'unterminated ./x`,
			raw:     []string{"./x"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := extractBashPaths(cwd, tc.command)
			want := expandPaths(cwd, tc.raw...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("extractBashPaths(%q) = %v, want %v", tc.command, got, want)
			}
		})
	}
}

func TestExpandPathsHomeForms(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	cases := map[string]struct {
		value string
		want  string
	}{
		"tilde alone":    {value: "~", want: home},
		"tilde slash":    {value: "~/docs/a.md", want: filepath.Join(home, "docs", "a.md")},
		"HOME variable":  {value: "$HOME/.ssh", want: filepath.Join(home, ".ssh")},
		"braced HOME":    {value: "${HOME}/.aws", want: filepath.Join(home, ".aws")},
		"HOME alone":     {value: "$HOME", want: home},
		"other variable": {value: "$HOMEDIR/x", want: filepath.Join("/cwd", "$HOMEDIR", "x")},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := expandPaths("/cwd", tc.value)
			if len(got) != 1 || got[0] != filepath.Clean(tc.want) {
				t.Fatalf("expandPaths(%q) = %v, want [%s]", tc.value, got, tc.want)
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
