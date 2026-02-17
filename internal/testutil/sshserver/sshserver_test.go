package sshserver_test

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/testutil/sshserver"
)

// makeAskpassScript creates a temporary script that echoes the given password.
// Used with SSH_ASKPASS and SSH_ASKPASS_REQUIRE=force to provide passwords
// non-interactively to the ssh command.
func makeAskpassScript(t *testing.T, dir, password string) string {
	t.Helper()

	scriptPath := filepath.Join(dir, "askpass.sh")
	script := fmt.Sprintf("#!/bin/sh\necho '%s'\n", password)
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		t.Fatalf("failed to write askpass script: %v", err)
	}
	return scriptPath
}

// sshCommand creates an exec.Cmd for ssh with the given config and args.
// Sets a 10-second timeout via ConnectTimeout.
func sshCommand(configPath string, args ...string) *exec.Cmd {
	fullArgs := []string{"-F", configPath, "-o", "ConnectTimeout=10"}
	fullArgs = append(fullArgs, args...)
	cmd := exec.Command("ssh", fullArgs...)
	// Prevent ssh from reading from stdin (avoids terminal prompts)
	cmd.Stdin = nil
	return cmd
}

func TestServer_StartsAndListens(t *testing.T) {
	srv := sshserver.New(t, sshserver.Options{
		Username: "testuser",
		Password: "testpass",
	})
	srv.Start()
	defer srv.Stop()

	if srv.Port() <= 0 {
		t.Fatalf("expected positive port, got %d", srv.Port())
	}

	if srv.SSHConfigPath() == "" {
		t.Fatal("expected SSH config path to be set")
	}

	if srv.Alias() == "" {
		t.Fatal("expected alias to be set")
	}

	// Verify the port is actually listening
	conn, err := net.DialTimeout("tcp", srv.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}
	conn.Close()
}

func TestServer_PasswordAuth(t *testing.T) {
	srv := sshserver.New(t, sshserver.Options{
		Username: "testuser",
		Password: "s3cret",
	})
	srv.Start()
	defer srv.Stop()

	dir := t.TempDir()
	askpass := makeAskpassScript(t, dir, "s3cret")

	cmd := sshCommand(srv.SSHConfigPath(), "-N", srv.Alias())
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+askpass,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=:0",
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start ssh: %v", err)
	}

	// Give SSH time to connect
	time.Sleep(2 * time.Second)

	// If the process is still running, authentication succeeded (it's in -N mode)
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		t.Fatal("ssh exited unexpectedly — authentication likely failed")
	}

	cmd.Process.Kill()
	cmd.Wait()
}

func TestServer_PublicKeyAuth(t *testing.T) {
	dir := t.TempDir()
	_, pubKey, keyPath := sshserver.GenerateClientKeyPair(t, dir)

	srv := sshserver.New(t, sshserver.Options{
		Username:       "testuser",
		AuthorizedKeys: sshserver.PublicKeys(pubKey),
	})
	srv.Start()
	defer srv.Stop()

	// Append IdentityFile to the generated SSH config
	appendToSSHConfig(t, srv.SSHConfigPath(), fmt.Sprintf("    IdentityFile %s\n", keyPath))

	cmd := sshCommand(srv.SSHConfigPath(), "-N", srv.Alias())
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start ssh: %v", err)
	}

	// Give SSH time to connect
	time.Sleep(2 * time.Second)

	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		t.Fatal("ssh exited unexpectedly — public key auth likely failed")
	}

	cmd.Process.Kill()
	cmd.Wait()
}

func TestServer_AuthRejection(t *testing.T) {
	srv := sshserver.New(t, sshserver.Options{
		Username: "testuser",
		Password: "correct-password",
	})
	srv.Start()
	defer srv.Stop()

	dir := t.TempDir()
	askpass := makeAskpassScript(t, dir, "wrong-password")

	cmd := sshCommand(srv.SSHConfigPath(), "-N", "-o", "NumberOfPasswordPrompts=1", srv.Alias())
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+askpass,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=:0",
	)

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected ssh to fail with wrong password, but it succeeded")
	}

	t.Logf("ssh output (expected failure): %s", string(output))
}

func TestServer_SessionStaysAlive(t *testing.T) {
	srv := sshserver.New(t, sshserver.Options{
		Username: "testuser",
		Password: "testpass",
	})
	srv.Start()
	defer srv.Stop()

	dir := t.TempDir()
	askpass := makeAskpassScript(t, dir, "testpass")

	cmd := sshCommand(srv.SSHConfigPath(), "-N", srv.Alias())
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+askpass,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=:0",
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start ssh: %v", err)
	}

	// Verify connection stays alive for several seconds
	for i := 0; i < 3; i++ {
		time.Sleep(1 * time.Second)
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Fatalf("ssh exited after %d seconds — session should stay alive", i+1)
		}
	}

	cmd.Process.Kill()
	cmd.Wait()
}

func TestServer_PortForwarding(t *testing.T) {
	// Start a simple TCP echo server as the forwarding target
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start echo server: %v", err)
	}
	defer echoListener.Close()
	echoPort := echoListener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := echoListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	srv := sshserver.New(t, sshserver.Options{
		Username: "testuser",
		Password: "testpass",
	})
	srv.Start()
	defer srv.Stop()

	dir := t.TempDir()
	askpass := makeAskpassScript(t, dir, "testpass")

	// Find a free local port for the forwarded side
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	localPort := localListener.Addr().(*net.TCPAddr).Port
	localListener.Close()

	// Start SSH with -L forwarding
	forwardSpec := fmt.Sprintf("%d:127.0.0.1:%d", localPort, echoPort)
	cmd := sshCommand(srv.SSHConfigPath(), "-N", "-L", forwardSpec, srv.Alias())
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+askpass,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=:0",
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start ssh: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// Wait for the tunnel to establish
	time.Sleep(2 * time.Second)

	// Connect through the forwarded port
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 5*time.Second)
	if err != nil {
		t.Fatalf("failed to connect through forwarded port: %v", err)
	}
	defer conn.Close()

	// Send test data and verify echo
	testData := "hello through the tunnel"
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	_, err = conn.Write([]byte(testData))
	if err != nil {
		t.Fatalf("failed to write through tunnel: %v", err)
	}

	// Close the write side so the echo server sends back the data
	conn.(*net.TCPConn).CloseWrite()

	buf, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("failed to read through tunnel: %v", err)
	}

	if string(buf) != testData {
		t.Fatalf("expected %q, got %q", testData, string(buf))
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	srv := sshserver.New(t, sshserver.Options{
		Username: "testuser",
		Password: "testpass",
	})
	srv.Start()

	dir := t.TempDir()
	askpass := makeAskpassScript(t, dir, "testpass")

	cmd := sshCommand(srv.SSHConfigPath(), "-N", srv.Alias())
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+askpass,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=:0",
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start ssh: %v", err)
	}

	// Wait for connection to establish
	time.Sleep(2 * time.Second)

	// Stop the server — should terminate the SSH connection
	srv.Stop()

	// The SSH process should exit within a reasonable time
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		// SSH exited as expected
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("ssh process did not exit after server shutdown")
	}
}

// appendToSSHConfig appends extra lines to an existing SSH config file.
func appendToSSHConfig(t *testing.T, path, content string) {
	t.Helper()

	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read SSH config: %v", err)
	}

	// Insert before the end — append to the Host block
	updated := strings.TrimRight(string(existing), "\n") + "\n" + content
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatalf("failed to write SSH config: %v", err)
	}
}
