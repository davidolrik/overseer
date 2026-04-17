package daemon

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestFindPortConflicts_OnlyReturnsHeldPorts verifies the discovery phase
// returns holders only for ports that are actually in use, ignoring free ones.
func TestFindPortConflicts_OnlyReturnsHeldPorts(t *testing.T) {
	heldLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer heldLn.Close()
	heldPort := heldLn.Addr().(*net.TCPAddr).Port

	freeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	freePort := freeLn.Addr().(*net.TCPAddr).Port
	freeLn.Close()

	conflicts := findPortConflicts([]int{heldPort, freePort})
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].Port != heldPort {
		t.Errorf("expected conflict on port %d, got %d", heldPort, conflicts[0].Port)
	}
	if int(conflicts[0].Chain[0].PID) != os.Getpid() {
		t.Errorf("expected holder PID %d, got %d", os.Getpid(), conflicts[0].Chain[0].PID)
	}
}

// TestEmitPortConflictTree_StreamsHeader verifies the emit phase formats
// each holder's tree through send.
func TestEmitPortConflictTree_StreamsHeader(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	conflicts := findPortConflicts([]int{port})
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	var lines []string
	emitPortConflictTree(conflicts, func(m, _ string) {
		lines = append(lines, m)
	})

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "process tree") {
		t.Errorf("expected 'process tree' header:\n%s", joined)
	}
	if !strings.Contains(joined, strconv.Itoa(os.Getpid())) {
		t.Errorf("expected our own PID (%d) in output:\n%s", os.Getpid(), joined)
	}
}

func TestPortConflictHeadline(t *testing.T) {
	cases := []struct {
		name  string
		ports []int
		want  string
	}{
		{"single", []int{25000}, "port 25000 is already in use"},
		{"multiple", []int{25000, 5432}, "ports 25000, 5432 are already in use"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := portConflictHeadline(tc.ports); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
