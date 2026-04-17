package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestParseLocalForwardPorts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []int
	}{
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "no forwards",
			in: `hostname example.com
port 22
user djo
`,
			want: nil,
		},
		{
			name: "single dynamicforward port-only",
			in:   "dynamicforward 25000\n",
			want: []int{25000},
		},
		{
			name: "single dynamicforward with bind addr",
			in:   "dynamicforward 127.0.0.1:25000\n",
			want: []int{25000},
		},
		{
			name: "single dynamicforward with ipv6 bind addr",
			in:   "dynamicforward [::1]:25000\n",
			want: []int{25000},
		},
		{
			name: "single localforward",
			in:   "localforward 5432 [localhost]:5432\n",
			want: []int{5432},
		},
		{
			name: "single localforward with ipv4 bind",
			in:   "localforward 127.0.0.1:5432 db.example.com:5432\n",
			want: []int{5432},
		},
		{
			name: "single localforward with ipv6 bind",
			in:   "localforward [::1]:5432 db.example.com:5432\n",
			want: []int{5432},
		},
		{
			name: "remoteforward is ignored (bind is on remote)",
			in:   "remoteforward 8080 localhost:8080\n",
			want: nil,
		},
		{
			name: "mixed forwards",
			in: `hostname example.com
port 22
dynamicforward 25000
localforward 5432 [localhost]:5432
remoteforward 9000 localhost:9000
localforward [::1]:6379 redis.example.com:6379
`,
			want: []int{25000, 5432, 6379},
		},
		{
			name: "duplicate ports deduped",
			in: `dynamicforward 25000
dynamicforward 127.0.0.1:25000
localforward 5432 [localhost]:5432
localforward 5432 [localhost]:5432
`,
			want: []int{25000, 5432},
		},
		{
			name: "case insensitive directive",
			in:   "DynamicForward 25000\n",
			want: []int{25000},
		},
		{
			name: "leading whitespace and tabs are tolerated",
			in:   "\tdynamicforward\t25000\n  localforward 5432 [localhost]:5432\n",
			want: []int{25000, 5432},
		},
		{
			name: "value 'any' or zero are skipped",
			in: `localforward 0 host:1
dynamicforward any
`,
			want: nil,
		},
		{
			name: "unparseable port spec is skipped without breaking later lines",
			in: `dynamicforward not-a-port
localforward 5432 [localhost]:5432
`,
			want: []int{5432},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLocalForwardPorts(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtractLocalForwardPorts_WithSSHConfigFile(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "ssh_config")
	cfg := `Host fwdtest
    HostName 10.0.0.1
    Port 22
    DynamicForward 25000
    LocalForward 5432 db.internal:5432
    RemoteForward 9000 localhost:9000
`
	if err := os.WriteFile(configPath, []byte(cfg), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := extractLocalForwardPorts("fwdtest", nil, configPath)
	sort.Ints(got)
	want := []int{5432, 25000}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractLocalForwardPorts_NoForwards(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "ssh_config")
	cfg := `Host plain
    HostName 10.0.0.1
    Port 22
`
	if err := os.WriteFile(configPath, []byte(cfg), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := extractLocalForwardPorts("plain", nil, configPath)
	if len(got) != 0 {
		t.Errorf("expected no ports, got %v", got)
	}
}
