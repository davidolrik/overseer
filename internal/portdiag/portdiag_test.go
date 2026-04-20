package portdiag

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"strings"
	"testing"
)

// openListener grabs a free local port and returns the listener and its port.
// Caller must close the listener.
func openListener(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port
}

func TestFindPortHolder_DetectsCurrentProcess(t *testing.T) {
	ln, port := openListener(t)
	defer ln.Close()

	h, err := FindPortHolder(port)
	if err != nil {
		t.Fatalf("FindPortHolder(%d): %v", port, err)
	}
	if h == nil {
		t.Fatal("FindPortHolder returned nil holder without error")
	}
	if h.Port != port {
		t.Errorf("Holder.Port = %d, want %d", h.Port, port)
	}
	if len(h.Chain) == 0 {
		t.Fatal("Holder.Chain is empty")
	}
	if int(h.Chain[0].PID) != os.Getpid() {
		t.Errorf("Chain[0].PID = %d, want %d (our pid)", h.Chain[0].PID, os.Getpid())
	}
}

func TestFindPortHolder_PopulatesUser(t *testing.T) {
	ln, port := openListener(t)
	defer ln.Close()

	h, err := FindPortHolder(port)
	if err != nil {
		t.Fatalf("FindPortHolder(%d): %v", port, err)
	}
	if h.Chain[0].User == "" {
		t.Errorf("expected User to be populated for our own process; got empty")
	}
}

func TestFindPortHolder_WalksAncestors(t *testing.T) {
	ln, port := openListener(t)
	defer ln.Close()

	h, err := FindPortHolder(port)
	if err != nil {
		t.Fatalf("FindPortHolder(%d): %v", port, err)
	}
	if len(h.Chain) < 2 {
		t.Fatalf("expected ancestor chain length >= 2, got %d", len(h.Chain))
	}
	for i := 0; i < len(h.Chain)-1; i++ {
		child := h.Chain[i]
		parent := h.Chain[i+1]
		if child.PPID != parent.PID {
			t.Errorf("Chain[%d].PPID=%d, but Chain[%d].PID=%d (chain not linked)",
				i, child.PPID, i+1, parent.PID)
		}
	}
	last := h.Chain[len(h.Chain)-1]
	if last.PID != 1 && last.PPID != 0 && last.PPID != 1 {
		t.Errorf("expected chain to terminate at PID 1 (or PPID=0/1); last=%+v", last)
	}
}

func TestFindPortHolder_NotFound(t *testing.T) {
	ln, port := openListener(t)
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	_, err := FindPortHolder(port)
	if err == nil {
		ln2, port2 := openListener(t)
		ln2.Close()
		_, err = FindPortHolder(port2)
	}
	if !errors.Is(err, ErrHolderNotFound) {
		t.Fatalf("expected ErrHolderNotFound, got %v", err)
	}
}

func TestWalkAncestors_TerminatesAtPID1(t *testing.T) {
	chain, err := WalkAncestors(int32(os.Getpid()))
	if err != nil {
		t.Fatalf("WalkAncestors(self): %v", err)
	}
	if len(chain) == 0 {
		t.Fatal("chain is empty")
	}
	if int(chain[0].PID) != os.Getpid() {
		t.Errorf("chain[0].PID = %d, want %d", chain[0].PID, os.Getpid())
	}
	last := chain[len(chain)-1]
	if last.PID != 1 && last.PPID != 0 && last.PPID != 1 {
		t.Errorf("expected chain to terminate at PID 1; last=%+v", last)
	}
}

// --- FormatTree unit tests ---

// sampleHolder builds a 4-deep chain similar to the real TablePlus scenario,
// useful for visual format assertions.
func sampleHolder() *Holder {
	return &Holder{
		Port: 25000,
		Addr: "::1",
		Chain: []ProcessInfo{
			{PID: 89229, PPID: 89196, User: "djo", Name: "ssh",
				Cmdline: "ssh -W [p-gate01.olrik.cloud]:64242 zero"},
			{PID: 89196, PPID: 1087, User: "djo", Name: "ssh",
				Cmdline: "ssh -W [p-pg01.nbg1-dc1.olrik.cloud]:64242 p-gate01.olrik.cloud"},
			{PID: 1087, PPID: 1, User: "djo", Name: "TablePlus",
				Cmdline: "/Applications/Setapp/TablePlus.app/Contents/MacOS/TablePlus"},
			{PID: 1, PPID: 0, User: "root", Name: "launchd"},
		},
	}
}

// stripANSI removes color codes so test assertions can match plain content.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == '\033' {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestFormatTree_RootFirstHolderLast(t *testing.T) {
	lines := FormatTree(sampleHolder())
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = stripANSI(l)
	}
	rootIdx, holderIdx := -1, -1
	for i, l := range plain[1:] {
		if strings.Contains(l, "launchd") {
			rootIdx = i
		}
		if strings.Contains(l, "89229") {
			holderIdx = i
		}
	}
	if rootIdx == -1 || holderIdx == -1 {
		t.Fatalf("missing root or holder line:\n%s", strings.Join(plain, "\n"))
	}
	if rootIdx >= holderIdx {
		t.Errorf("root should appear before holder; root=%d holder=%d", rootIdx, holderIdx)
	}
}

func TestFormatTree_PIDsRightAligned(t *testing.T) {
	lines := FormatTree(sampleHolder())
	// max PID width across the sample chain is 5 ("89229", "89196").
	// PID 1 and 1087 must be right-padded to that width.
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = stripANSI(l)
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "    1 root") {
		t.Errorf("expected PID 1 right-padded to 5 chars before user 'root':\n%s", joined)
	}
	if !strings.Contains(joined, " 1087 djo") {
		t.Errorf("expected PID 1087 right-padded to 5 chars before user 'djo':\n%s", joined)
	}
}

func TestFormatTree_DropsCommandLineUnavailable(t *testing.T) {
	lines := FormatTree(sampleHolder())
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "command line unavailable") {
		t.Errorf("expected '(command line unavailable)' to be removed, got:\n%s", joined)
	}
}

func TestFormatTree_DropsPartialAnnotations(t *testing.T) {
	h := &Holder{
		Port: 25000,
		Addr: "::1",
		Chain: []ProcessInfo{
			{PID: 89229, PPID: 1, User: "djo", Name: "ssh", Cmdline: "ssh zero"},
			{PID: 1, PPID: 0, User: "root", Name: "launchd",
				Err: errors.New("invalid argument")},
		},
	}
	joined := strings.Join(FormatTree(h), "\n")
	if strings.Contains(joined, "partial:") {
		t.Errorf("expected per-node '(partial: ...)' annotations to be dropped:\n%s", joined)
	}
}

func TestFormatTree_TreeChars(t *testing.T) {
	lines := FormatTree(sampleHolder())
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = stripANSI(l)
	}
	// Every entry line must start with a "└" connector character.
	for i, l := range plain[1:] {
		if !strings.Contains(l, "└") {
			t.Errorf("line %d has no tree connector: %q", i, l)
		}
	}
}

func TestFormatTree_ConsistentTreeColumnWidth(t *testing.T) {
	lines := FormatTree(sampleHolder())
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = stripANSI(l)
	}
	// Tree column + right-aligned PID column must be the same width on
	// every row, so the user column starts at the same visual column on
	// every line. Compare user-start positions.
	userCol := -1
	for i, l := range plain[1:] {
		col := userStartColAfterPID(l)
		if col < 0 {
			t.Fatalf("could not locate user column in line %d: %q", i, l)
		}
		if userCol == -1 {
			userCol = col
			continue
		}
		if col != userCol {
			t.Errorf("user column not aligned: line %d starts at col %d (want %d):\n%s",
				i, col, userCol, strings.Join(plain, "\n"))
		}
	}
}

func TestFormatTree_IncludesUser(t *testing.T) {
	lines := FormatTree(sampleHolder())
	plain := strings.Join(lines, "\n")
	plain = stripANSI(plain)
	if !strings.Contains(plain, "root") {
		t.Errorf("expected user 'root' in output:\n%s", plain)
	}
	if !strings.Contains(plain, "djo") {
		t.Errorf("expected user 'djo' in output:\n%s", plain)
	}
}

func TestFormatTree_NilHolder(t *testing.T) {
	if got := FormatTree(nil); got != nil {
		t.Errorf("FormatTree(nil) = %v, want nil", got)
	}
}

func TestFormatTree_FallsBackToNameWhenCmdlineEmpty(t *testing.T) {
	h := &Holder{
		Port: 25000,
		Addr: "::1",
		Chain: []ProcessInfo{
			{PID: 1, PPID: 0, User: "root", Name: "launchd"},
		},
	}
	joined := stripANSI(strings.Join(FormatTree(h), "\n"))
	if !strings.Contains(joined, "launchd") {
		t.Errorf("expected 'launchd' fallback in output:\n%s", joined)
	}
}

func TestFormatTree_ColorsSSHHost(t *testing.T) {
	h := &Holder{
		Port: 5432,
		Addr: "127.0.0.1",
		Chain: []ProcessInfo{
			{PID: 100, PPID: 1, User: "djo", Name: "ssh",
				Cmdline: "ssh -N -L 5432:localhost:5432 prod-db"},
		},
	}
	joined := strings.Join(FormatTree(h), "\n")
	wantWrap := colorHostname + "prod-db" + colorReset
	if !strings.Contains(joined, wantWrap) {
		t.Errorf("expected host 'prod-db' colored, output:\n%s", joined)
	}
}

func TestFormatTree_ColorsExecutable(t *testing.T) {
	h := &Holder{
		Port: 5432,
		Addr: "127.0.0.1",
		Chain: []ProcessInfo{
			{PID: 100, PPID: 1, User: "djo", Name: "ssh", Cmdline: "ssh prod-db"},
		},
	}
	joined := strings.Join(FormatTree(h), "\n")
	// First token of cmdline ("ssh") should be colored cyan.
	wantWrap := colorProcessName + "ssh" + colorReset
	if !strings.Contains(joined, wantWrap) {
		t.Errorf("expected first cmdline token 'ssh' colored cyan, output:\n%s", joined)
	}
}

func TestColorizeCmdline(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantSubs []string
	}{
		{
			name: "ssh with host last",
			in:   "ssh prod-db",
			wantSubs: []string{
				colorProcessName + "ssh" + colorReset,
				colorHostname + "prod-db" + colorReset,
			},
		},
		{
			name: "ssh with -N -L flags",
			in:   "ssh -N -L 5432:localhost:5432 prod-db",
			wantSubs: []string{
				colorProcessName + "ssh" + colorReset,
				colorHostname + "prod-db" + colorReset,
			},
		},
		{
			name: "non-ssh executable still gets process color",
			in:   "/Applications/TablePlus.app/Contents/MacOS/TablePlus",
			wantSubs: []string{
				colorProcessName + "/Applications/TablePlus.app/Contents/MacOS/TablePlus" + colorReset,
			},
		},
		{
			name: "absolute ssh path",
			in:   "/usr/bin/ssh -fN -D 25000 zero",
			wantSubs: []string{
				colorProcessName + "/usr/bin/ssh" + colorReset,
				colorHostname + "zero" + colorReset,
			},
		},
		{
			name: "ssh -W bracketed host gets colored",
			in:   "ssh -W [p-gate01.olrik.cloud]:64242 zero",
			wantSubs: []string{
				colorProcessName + "ssh" + colorReset,
				"[" + colorHostname + "p-gate01.olrik.cloud" + colorReset + "]",
				colorPort + "64242" + colorReset,
				colorHostname + "zero" + colorReset,
			},
		},
		{
			name: "ssh -W ipv6 bracketed host gets colored",
			in:   "ssh -W [::1]:22 host",
			wantSubs: []string{
				"[" + colorHostname + "::1" + colorReset + "]",
				colorPort + "22" + colorReset,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := colorizeCmdline(tc.in)
			for _, want := range tc.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("colorizeCmdline(%q) = %q, want substring %q",
						tc.in, got, want)
				}
			}
		})
	}
}

func TestFormatTree_ColorsCurrentUserGreen(t *testing.T) {
	cur, err := user.Current()
	if err != nil {
		t.Skip("cannot determine current user")
	}
	h := &Holder{
		Port: 5432,
		Addr: "127.0.0.1",
		Chain: []ProcessInfo{
			{PID: 100, PPID: 1, User: cur.Username, Name: "ssh", Cmdline: "ssh host"},
		},
	}
	joined := strings.Join(FormatTree(h), "\n")
	want := colorCurrentUser + cur.Username
	if !strings.Contains(joined, want) {
		t.Errorf("expected current user %q in green, got:\n%s", cur.Username, joined)
	}
}

func TestFormatTree_ColorsOtherUserYellow(t *testing.T) {
	h := &Holder{
		Port: 5432,
		Addr: "127.0.0.1",
		Chain: []ProcessInfo{
			// "root-impossible-username" is unlikely to ever match the
			// current user under which tests run.
			{PID: 100, PPID: 1, User: "root-impossible-username", Name: "ssh", Cmdline: "ssh host"},
		},
	}
	joined := strings.Join(FormatTree(h), "\n")
	want := colorOtherUser + "root-impossible-username"
	if !strings.Contains(joined, want) {
		t.Errorf("expected non-current user in yellow, got:\n%s", joined)
	}
}

// userStartColAfterPID returns the visual column (rune count) of the first
// TestFormatConflictTree_HighlightsMatchingPID verifies that the given
// highlightPID renders in bold red while other PIDs stay plain. Column widths
// must remain consistent across highlighted and non-highlighted rows — the
// color escapes are added around padded digits, not inside them.
func TestFormatConflictTree_HighlightsMatchingPID(t *testing.T) {
	chain := []ProcessInfo{
		{PID: 86945, PPID: 1234, User: "djo", Name: "ssh", Cmdline: "ssh zero"},
		{PID: 1234, PPID: 1, User: "djo", Name: "zsh", Cmdline: "zsh"},
		{PID: 1, PPID: 0, User: "root", Name: "launchd"},
	}

	lines := FormatConflictTree("test header", chain, 86945)
	if len(lines) < 2 {
		t.Fatalf("expected at least header + rows, got %v", lines)
	}

	joined := strings.Join(lines, "\n")
	highlighted := colorHighlight + fmt.Sprintf("%*d", 5, 86945) + colorReset
	if !strings.Contains(joined, highlighted) {
		t.Errorf("expected PID 86945 wrapped in bold red %q, got:\n%s",
			highlighted, joined)
	}

	// PIDs that don't match must NOT be wrapped in the highlight color.
	if strings.Contains(joined, colorHighlight+fmt.Sprintf("%*d", 5, 1234)+colorReset) {
		t.Errorf("PID 1234 should not be highlighted, got:\n%s", joined)
	}

	// Stripping ANSI, column width stays consistent across rows so PIDs align.
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = stripANSI(l)
	}
	width := userStartColAfterPID(plain[1])
	for _, row := range plain[2:] {
		if userStartColAfterPID(row) != width {
			t.Errorf("user column shifted — highlight changed visible width:\n%s", strings.Join(plain, "\n"))
		}
	}
}

// TestFormatConflictTree_ZeroHighlightLeavesAllPlain verifies the opt-out
// path used by port-conflict callers that don't need a highlight.
func TestFormatConflictTree_ZeroHighlightLeavesAllPlain(t *testing.T) {
	chain := []ProcessInfo{
		{PID: 42, PPID: 1, User: "djo", Name: "x", Cmdline: "x"},
		{PID: 1, PPID: 0, User: "root", Name: "launchd"},
	}

	lines := FormatConflictTree("hdr", chain, 0)
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, colorHighlight) {
		t.Errorf("expected no highlight escape when highlightPID=0, got:\n%s", joined)
	}
}

// alphabetic character that follows at least one ASCII digit. That's the
// start of the user column in a FormatTree row, regardless of how wide the
// tree prefix or right-aligned PID happen to be.
func userStartColAfterPID(s string) int {
	col := 0
	seenDigit := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			seenDigit = true
		} else if seenDigit && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return col
		}
		col++
	}
	return -1
}
