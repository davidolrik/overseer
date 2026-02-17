// Package sshserver provides an in-process SSH server for integration testing.
// It supports password and public key authentication, session channels (for -N),
// and direct-tcpip channels (for -L port forwarding).
//
// The server generates an SSH config file that can be passed to `ssh -F` so the
// system SSH binary can connect without any manual configuration.
package sshserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// Server is an in-process SSH server for testing.
type Server struct {
	t    testing.TB
	opts Options

	config   *ssh.ServerConfig
	listener net.Listener
	wg       sync.WaitGroup
	done     chan struct{}

	configDir     string // t.TempDir() for SSH config and host key
	sshConfigPath string
	alias         string
}

// Options configures the test SSH server.
type Options struct {
	Username       string         // Required
	Password       string         // Enables password auth if set
	AuthorizedKeys []ssh.PublicKey // Enables pubkey auth if set
	HostKey        ssh.Signer     // Generated if nil
	Alias          string         // Defaults to "test-<port>"
}

// New creates a test SSH server. Call Start() to begin listening.
func New(t testing.TB, opts Options) *Server {
	t.Helper()

	if opts.Username == "" {
		t.Fatal("sshserver: Username is required")
	}

	return &Server{
		t:    t,
		opts: opts,
		done: make(chan struct{}),
	}
}

// Start begins listening on a random port and generates SSH config files.
func (s *Server) Start() {
	s.t.Helper()

	// Generate host key if not provided
	hostKey := s.opts.HostKey
	if hostKey == nil {
		hostKey = generateED25519Key(s.t)
	}

	// Configure server authentication
	s.config = &ssh.ServerConfig{}
	s.config.AddHostKey(hostKey)

	if s.opts.Password != "" {
		s.config.PasswordCallback = func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if conn.User() == s.opts.Username && string(password) == s.opts.Password {
				return nil, nil
			}
			return nil, fmt.Errorf("authentication failed for user %q", conn.User())
		}
	}

	if len(s.opts.AuthorizedKeys) > 0 {
		s.config.PublicKeyCallback = func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if conn.User() != s.opts.Username {
				return nil, fmt.Errorf("unknown user %q", conn.User())
			}
			keyBytes := key.Marshal()
			for _, authorized := range s.opts.AuthorizedKeys {
				if bytes.Equal(keyBytes, authorized.Marshal()) {
					return nil, nil
				}
			}
			return nil, fmt.Errorf("unknown public key")
		}
	}

	// Listen on a random port
	var err error
	s.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.t.Fatalf("sshserver: failed to listen: %v", err)
	}

	// Set alias
	s.alias = s.opts.Alias
	if s.alias == "" {
		s.alias = fmt.Sprintf("test-%d", s.Port())
	}

	// Generate SSH config
	s.configDir = s.t.TempDir()
	s.generateSSHConfig()

	// Start accept loop
	s.wg.Add(1)
	go s.acceptLoop()
}

// Stop closes the listener and waits for all connections to finish.
func (s *Server) Stop() {
	close(s.done)
	s.listener.Close()
	s.wg.Wait()
}

// Port returns the port the server is listening on.
func (s *Server) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Addr returns the server address as "127.0.0.1:<port>".
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// SSHConfigPath returns the path to the generated SSH config file.
func (s *Server) SSHConfigPath() string {
	return s.sshConfigPath
}

// Alias returns the SSH config host alias.
func (s *Server) Alias() string {
	return s.alias
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				s.t.Logf("sshserver: accept error: %v", err)
				return
			}
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	// Perform SSH handshake
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		// Authentication failures are expected in tests
		s.t.Logf("sshserver: handshake failed: %v", err)
		return
	}
	defer sshConn.Close()

	// Handle global requests (keepalive, no-more-sessions)
	go s.handleGlobalRequests(reqs)

	// Handle channels
	for {
		select {
		case <-s.done:
			return
		case newChan, ok := <-chans:
			if !ok {
				return
			}
			switch newChan.ChannelType() {
			case "session":
				s.wg.Add(1)
				go s.handleSession(newChan)
			case "direct-tcpip":
				s.wg.Add(1)
				go s.handleDirectTCPIP(newChan)
			default:
				newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			}
		}
	}
}

func (s *Server) handleGlobalRequests(reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "keepalive@openssh.com":
			if req.WantReply {
				req.Reply(true, nil)
			}
		case "no-more-sessions@openssh.com":
			if req.WantReply {
				req.Reply(true, nil)
			}
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func (s *Server) handleSession(newChan ssh.NewChannel) {
	defer s.wg.Done()

	ch, reqs, err := newChan.Accept()
	if err != nil {
		s.t.Logf("sshserver: failed to accept session: %v", err)
		return
	}
	defer ch.Close()

	// Handle session requests (env, shell, exec, subsystem, etc.)
	go func() {
		for req := range reqs {
			switch req.Type {
			case "env":
				if req.WantReply {
					req.Reply(true, nil)
				}
			case "shell", "exec", "subsystem":
				if req.WantReply {
					req.Reply(true, nil)
				}
			default:
				if req.WantReply {
					req.Reply(false, nil)
				}
			}
		}
	}()

	// Block until the server is stopped (supports -N mode)
	<-s.done
}

// directTCPIPPayload is the RFC 4254 payload for direct-tcpip channels.
type directTCPIPPayload struct {
	DestHost   string
	DestPort   uint32
	OriginHost string
	OriginPort uint32
}

func (s *Server) handleDirectTCPIP(newChan ssh.NewChannel) {
	defer s.wg.Done()

	var payload directTCPIPPayload
	if err := ssh.Unmarshal(newChan.ExtraData(), &payload); err != nil {
		newChan.Reject(ssh.ConnectionFailed, "invalid payload")
		return
	}

	// Dial the target
	target := net.JoinHostPort(payload.DestHost, fmt.Sprintf("%d", payload.DestPort))
	targetConn, err := net.Dial("tcp", target)
	if err != nil {
		newChan.Reject(ssh.ConnectionFailed, fmt.Sprintf("failed to connect to %s: %v", target, err))
		return
	}
	defer targetConn.Close()

	ch, _, err := newChan.Accept()
	if err != nil {
		s.t.Logf("sshserver: failed to accept direct-tcpip channel: %v", err)
		return
	}
	defer ch.Close()

	// Bidirectional proxy
	var proxyWg sync.WaitGroup
	proxyWg.Add(2)

	go func() {
		defer proxyWg.Done()
		io.Copy(ch, targetConn)
		ch.CloseWrite()
	}()

	go func() {
		defer proxyWg.Done()
		io.Copy(targetConn, ch)
		targetConn.(*net.TCPConn).CloseWrite()
	}()

	// Wait for copy to finish or server shutdown
	doneCh := make(chan struct{})
	go func() {
		proxyWg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-s.done:
	}
}

func (s *Server) generateSSHConfig() {
	s.sshConfigPath = filepath.Join(s.configDir, "ssh_config")

	config := fmt.Sprintf(`Host %s
    HostName 127.0.0.1
    Port %d
    User %s
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    LogLevel ERROR
`, s.alias, s.Port(), s.opts.Username)

	// Password-only auth needs additional config to prevent pubkey attempts
	if s.opts.Password != "" && len(s.opts.AuthorizedKeys) == 0 {
		config += "    PreferredAuthentications password\n"
		config += "    PubkeyAuthentication no\n"
	}

	if err := os.WriteFile(s.sshConfigPath, []byte(config), 0600); err != nil {
		s.t.Fatalf("sshserver: failed to write SSH config: %v", err)
	}
}

func generateED25519Key(t testing.TB) ssh.Signer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("sshserver: failed to generate ED25519 key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("sshserver: failed to create signer: %v", err)
	}

	return signer
}

// PublicKeys wraps one or more ssh.PublicKey values into a slice.
// Convenience helper for constructing Options.AuthorizedKeys.
func PublicKeys(keys ...ssh.PublicKey) []ssh.PublicKey {
	return keys
}

// GenerateClientKeyPair generates a temporary ED25519 keypair for testing.
// Returns the signer, the public key, and the path to the private key file.
func GenerateClientKeyPair(t testing.TB, dir string) (ssh.Signer, ssh.PublicKey, string) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("sshserver: failed to generate client key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("sshserver: failed to create client signer: %v", err)
	}

	// Write private key in OpenSSH format using the library
	keyPath := filepath.Join(dir, "id_ed25519_test")
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("sshserver: failed to marshal private key: %v", err)
	}

	keyBytes := pem.EncodeToMemory(block)
	if err := os.WriteFile(keyPath, keyBytes, 0600); err != nil {
		t.Fatalf("sshserver: failed to write private key: %v", err)
	}

	return signer, signer.PublicKey(), keyPath
}
