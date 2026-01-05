package state

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// Probe is a sensor that emits readings to a channel.
// Probes are simpler than the old Sensor interface - they just measure
// and emit readings, without managing state or subscriptions.
type Probe interface {
	// Name returns the probe name
	Name() string

	// Start begins the probe's polling/monitoring
	Start(ctx context.Context, output chan<- SensorReading)

	// Check performs a single check and returns the reading
	Check(ctx context.Context) SensorReading
}

// TCPProbe checks network connectivity via TCP connections
type TCPProbe struct {
	name     string
	targets  []TCPTarget
	timeout  time.Duration
	interval time.Duration
	logger   *slog.Logger
}

// TCPTarget defines a host to check for connectivity
type TCPTarget struct {
	Host    string // Host address (IP or hostname)
	Port    string // Port to connect to
	Network string // "tcp", "tcp4", "tcp6"
}

// DefaultTCPTargets returns the default TCP targets for connectivity checking
func DefaultTCPTargets() []TCPTarget {
	return []TCPTarget{
		// Cloudflare DNS IPv4
		{Host: "1.1.1.1", Port: "443", Network: "tcp4"},
		{Host: "1.0.0.1", Port: "443", Network: "tcp4"},
		// Cloudflare DNS IPv6
		{Host: "2606:4700:4700::1111", Port: "443", Network: "tcp6"},
		{Host: "2606:4700:4700::1001", Port: "443", Network: "tcp6"},
		// Google DNS IPv4
		{Host: "8.8.8.8", Port: "443", Network: "tcp4"},
		{Host: "8.8.4.4", Port: "443", Network: "tcp4"},
		// Google DNS IPv6
		{Host: "2001:4860:4860::8888", Port: "443", Network: "tcp6"},
		{Host: "2001:4860:4860::8844", Port: "443", Network: "tcp6"},
	}
}

// NewTCPProbe creates a new TCP connectivity probe
func NewTCPProbe(logger *slog.Logger) *TCPProbe {
	if logger == nil {
		logger = slog.Default()
	}
	return &TCPProbe{
		name:     "tcp",
		targets:  DefaultTCPTargets(),
		timeout:  5 * time.Second,
		interval: 10 * time.Second,
		logger:   logger,
	}
}

func (p *TCPProbe) Name() string { return p.name }

func (p *TCPProbe) Start(ctx context.Context, output chan<- SensorReading) {
	go func() {
		// Do an initial check immediately
		reading := p.Check(ctx)
		select {
		case output <- reading:
		case <-ctx.Done():
			return
		}

		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reading := p.Check(ctx)
				select {
				case output <- reading:
				default:
					// Output buffer full, skip this reading
				}
			}
		}
	}()

	p.logger.Info("TCP probe started", "interval", p.interval)
}

func (p *TCPProbe) Check(ctx context.Context) SensorReading {
	start := time.Now()

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	isOnline := false

	// Try each target until one succeeds
	for _, target := range p.targets {
		dialer := net.Dialer{
			Timeout: p.timeout / time.Duration(len(p.targets)),
		}

		addr := net.JoinHostPort(target.Host, target.Port)
		conn, err := dialer.DialContext(ctx, target.Network, addr)
		if err != nil {
			continue
		}

		conn.Close()
		isOnline = true
		break
	}

	return SensorReading{
		Sensor:    p.name,
		Timestamp: time.Now(),
		Online:    &isOnline,
		Latency:   time.Since(start),
	}
}

// IPProbe checks public IP address via DNS queries
type IPProbe struct {
	name      string
	resolvers []IPResolver
	timeout   time.Duration
	network   string // "udp4" or "udp6"
	offlineIP string // IP to return when offline
	logger    *slog.Logger

	// Hysteresis to prevent flapping
	mu             sync.Mutex
	lastStableIP   string // Last confirmed stable IP
	pendingIP      string // IP we're seeing but not yet confirmed
	pendingCount   int    // How many times we've seen pendingIP
	stabilityCount int    // How many times we need to see an IP before accepting it
}

// IPResolver defines a DNS resolver for public IP detection
type IPResolver struct {
	ResolverAddr string // DNS server address
	Hostname     string // Hostname to query
	QueryType    string // "A", "TXT", or "AAAA"
}

// DefaultIPv4Resolvers returns the default resolvers for IPv4 detection
func DefaultIPv4Resolvers() []IPResolver {
	return []IPResolver{
		{ResolverAddr: "resolver1.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "A"},
		{ResolverAddr: "resolver2.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "A"},
		{ResolverAddr: "ns1.google.com.:53", Hostname: "o-o.myaddr.l.google.com.", QueryType: "TXT"},
		{ResolverAddr: "ns1-1.akamaitech.net.:53", Hostname: "whoami.akamai.net.", QueryType: "A"},
	}
}

// DefaultIPv6Resolvers returns the default resolvers for IPv6 detection
func DefaultIPv6Resolvers() []IPResolver {
	return []IPResolver{
		{ResolverAddr: "resolver1.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "AAAA"},
		{ResolverAddr: "resolver2.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "AAAA"},
		{ResolverAddr: "ns1.google.com.:53", Hostname: "o-o.myaddr.l.google.com.", QueryType: "TXT"},
	}
}

// NewIPv4Probe creates a new IPv4 address probe
func NewIPv4Probe(logger *slog.Logger) *IPProbe {
	if logger == nil {
		logger = slog.Default()
	}
	return &IPProbe{
		name:           "public_ipv4",
		resolvers:      DefaultIPv4Resolvers(),
		timeout:        15 * time.Second,
		network:        "udp4",
		offlineIP:      "169.254.0.0",
		logger:         logger,
		stabilityCount: 2, // Require 2 consecutive readings before accepting a new IP
	}
}

// NewIPv6Probe creates a new IPv6 address probe
func NewIPv6Probe(logger *slog.Logger) *IPProbe {
	if logger == nil {
		logger = slog.Default()
	}
	return &IPProbe{
		name:           "public_ipv6",
		resolvers:      DefaultIPv6Resolvers(),
		timeout:        15 * time.Second,
		network:        "udp6",
		offlineIP:      "fe80::",
		logger:         logger,
		stabilityCount: 2, // Require 2 consecutive readings before accepting a new IP
	}
}

func (p *IPProbe) Name() string { return p.name }

func (p *IPProbe) Start(ctx context.Context, output chan<- SensorReading) {
	// IP probes don't poll continuously - they're checked on demand
	// or when triggered by network changes
	p.logger.Debug("IP probe ready", "name", p.name)
}

func (p *IPProbe) Check(ctx context.Context) SensorReading {
	start := time.Now()

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	// Query all resolvers in parallel and use consensus
	detectedIP := p.checkWithConsensus(ctx)

	// Apply hysteresis for additional stability
	stableIP := p.applyHysteresis(detectedIP)

	if stableIP == "" {
		// All resolvers failed - return offline IP
		return SensorReading{
			Sensor:    p.name,
			Timestamp: time.Now(),
			IP:        net.ParseIP(p.offlineIP),
			Value:     p.offlineIP,
			Latency:   time.Since(start),
		}
	}

	return SensorReading{
		Sensor:    p.name,
		Timestamp: time.Now(),
		IP:        net.ParseIP(stableIP),
		Value:     stableIP,
		Latency:   time.Since(start),
	}
}

// checkWithConsensus queries multiple resolvers in parallel and returns
// the IP that appears most frequently (consensus approach)
func (p *IPProbe) checkWithConsensus(ctx context.Context) string {
	network := p.network
	results := make(chan string, len(p.resolvers))
	var wg sync.WaitGroup

	// Query all resolvers in parallel
	for _, resolver := range p.resolvers {
		wg.Add(1)
		go func(res IPResolver) {
			defer wg.Done()

			resolverAddr := res.ResolverAddr
			r := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					d := net.Dialer{Timeout: p.timeout / 2} // Shorter timeout per resolver
					return d.DialContext(ctx, network, resolverAddr)
				},
			}

			var publicIP string
			var err error

			switch res.QueryType {
			case "TXT":
				var records []string
				records, err = r.LookupTXT(ctx, res.Hostname)
				if err == nil && len(records) > 0 {
					publicIP = strings.Trim(strings.TrimSpace(records[0]), "\"")
				}
			case "AAAA":
				var ips []net.IP
				ips, err = r.LookupIP(ctx, "ip6", res.Hostname)
				if err == nil && len(ips) > 0 {
					publicIP = ips[0].String()
				}
			default: // "A"
				var ips []net.IP
				ips, err = r.LookupIP(ctx, "ip4", res.Hostname)
				if err == nil && len(ips) > 0 {
					publicIP = ips[0].String()
				}
			}

			if err == nil && publicIP != "" {
				results <- publicIP
			}
		}(resolver)
	}

	// Close results channel when all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Count votes for each IP
	votes := make(map[string]int)
	for ip := range results {
		votes[ip]++
	}

	// Find the IP with the most votes
	var bestIP string
	bestCount := 0
	for ip, count := range votes {
		if count > bestCount {
			bestIP = ip
			bestCount = count
		}
	}

	// Log if there was disagreement
	if len(votes) > 1 {
		p.logger.Debug("IP resolver disagreement, using consensus",
			"sensor", p.name,
			"votes", votes,
			"winner", bestIP)
	}

	return bestIP
}

// applyHysteresis prevents IP flapping by requiring consecutive readings
// of the same IP before accepting a change
func (p *IPProbe) applyHysteresis(detectedIP string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	// If no IP detected, don't change state
	if detectedIP == "" {
		return p.lastStableIP
	}

	// If this is the first reading, accept it immediately
	if p.lastStableIP == "" {
		p.lastStableIP = detectedIP
		p.pendingIP = ""
		p.pendingCount = 0
		return detectedIP
	}

	// If detected IP matches current stable IP, reset pending state
	if detectedIP == p.lastStableIP {
		p.pendingIP = ""
		p.pendingCount = 0
		return p.lastStableIP
	}

	// Different IP detected - track it
	if detectedIP == p.pendingIP {
		// Same pending IP seen again
		p.pendingCount++
		if p.pendingCount >= p.stabilityCount {
			// IP is stable, accept the change
			p.logger.Info("IP change confirmed after stability check",
				"sensor", p.name,
				"old_ip", p.lastStableIP,
				"new_ip", detectedIP,
				"readings", p.pendingCount)
			p.lastStableIP = detectedIP
			p.pendingIP = ""
			p.pendingCount = 0
			return detectedIP
		}
		// Not yet stable, return old IP
		p.logger.Debug("IP change pending stability",
			"sensor", p.name,
			"stable_ip", p.lastStableIP,
			"pending_ip", detectedIP,
			"count", p.pendingCount,
			"required", p.stabilityCount)
		return p.lastStableIP
	}

	// New different IP, start tracking it
	p.pendingIP = detectedIP
	p.pendingCount = 1
	p.logger.Debug("New IP detected, starting stability tracking",
		"sensor", p.name,
		"stable_ip", p.lastStableIP,
		"pending_ip", detectedIP)
	return p.lastStableIP
}

// NetworkMonitorProbe watches for network changes and triggers checks
type NetworkMonitorProbe struct {
	name     string
	interval time.Duration // Polling interval for platforms without events
	logger   *slog.Logger

	// For triggering IP checks
	ipv4Probe      *IPProbe
	ipv6Probe      *IPProbe
	localIPv4Probe *LocalIPProbe

	mu            sync.Mutex
	lastCheckTime time.Time
	minInterval   time.Duration // Minimum time between checks (debounce)
}

// NewNetworkMonitorProbe creates a new network monitor probe
func NewNetworkMonitorProbe(ipv4 *IPProbe, ipv6 *IPProbe, localIPv4 *LocalIPProbe, logger *slog.Logger) *NetworkMonitorProbe {
	if logger == nil {
		logger = slog.Default()
	}
	return &NetworkMonitorProbe{
		name:           "network_monitor",
		interval:       5 * time.Second,
		logger:         logger,
		ipv4Probe:      ipv4,
		ipv6Probe:      ipv6,
		localIPv4Probe: localIPv4,
		minInterval:    2 * time.Second,
	}
}

func (p *NetworkMonitorProbe) Name() string { return p.name }

func (p *NetworkMonitorProbe) Start(ctx context.Context, output chan<- SensorReading) {
	go func() {
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// On each tick, check IPs and emit readings
				p.checkAndEmit(ctx, output)
			}
		}
	}()

	p.logger.Info("Network monitor probe started", "interval", p.interval)
}

func (p *NetworkMonitorProbe) checkAndEmit(ctx context.Context, output chan<- SensorReading) {
	// Debounce rapid checks
	p.mu.Lock()
	if time.Since(p.lastCheckTime) < p.minInterval {
		p.mu.Unlock()
		return
	}
	p.lastCheckTime = time.Now()
	p.mu.Unlock()

	// Check local IPv4 first (fastest, no network needed)
	if p.localIPv4Probe != nil {
		reading := p.localIPv4Probe.Check(ctx)
		select {
		case output <- reading:
		default:
		}
	}

	// Check public IPv4
	if p.ipv4Probe != nil {
		reading := p.ipv4Probe.Check(ctx)
		select {
		case output <- reading:
		default:
		}
	}

	// Check public IPv6
	if p.ipv6Probe != nil {
		reading := p.ipv6Probe.Check(ctx)
		select {
		case output <- reading:
		default:
		}
	}
}

func (p *NetworkMonitorProbe) Check(ctx context.Context) SensorReading {
	// NetworkMonitor doesn't produce its own readings
	return SensorReading{
		Sensor:    p.name,
		Timestamp: time.Now(),
	}
}

// TriggerCheck forces an immediate IP check (called externally)
func (p *NetworkMonitorProbe) TriggerCheck(ctx context.Context, output chan<- SensorReading) {
	p.checkAndEmit(ctx, output)
}

// LocalIPProbe detects the local LAN IP address
type LocalIPProbe struct {
	name    string
	network string // "ip4" or "ip6"
	logger  *slog.Logger
}

// NewLocalIPv4Probe creates a probe that detects the local IPv4 LAN address
func NewLocalIPv4Probe(logger *slog.Logger) *LocalIPProbe {
	if logger == nil {
		logger = slog.Default()
	}
	return &LocalIPProbe{
		name:    "local_ipv4",
		network: "udp4",
		logger:  logger,
	}
}

// NewLocalIPv6Probe creates a probe that detects the local IPv6 LAN address
func NewLocalIPv6Probe(logger *slog.Logger) *LocalIPProbe {
	if logger == nil {
		logger = slog.Default()
	}
	return &LocalIPProbe{
		name:    "local_ipv6",
		network: "udp6",
		logger:  logger,
	}
}

func (p *LocalIPProbe) Name() string { return p.name }

func (p *LocalIPProbe) Start(ctx context.Context, output chan<- SensorReading) {
	// Local IP probes are checked on demand, not continuously polled
	p.logger.Debug("Local IP probe ready", "name", p.name)
}

func (p *LocalIPProbe) Check(ctx context.Context) SensorReading {
	start := time.Now()

	localIP := p.getLocalIP()

	if localIP == "" {
		return SensorReading{
			Sensor:    p.name,
			Timestamp: time.Now(),
			Latency:   time.Since(start),
			Error:     fmt.Errorf("could not determine local IP"),
		}
	}

	return SensorReading{
		Sensor:    p.name,
		Timestamp: time.Now(),
		IP:        net.ParseIP(localIP),
		Value:     localIP,
		Latency:   time.Since(start),
	}
}

// getLocalIP returns the local IP address used to reach the internet
func (p *LocalIPProbe) getLocalIP() string {
	// Use a well-known external address to determine which local interface
	// would be used. We don't actually send any data - just let the OS
	// choose the right source address.
	var target string
	if p.network == "udp6" {
		target = "[2001:4860:4860::8888]:80" // Google DNS IPv6
	} else {
		target = "8.8.8.8:80" // Google DNS IPv4
	}

	conn, err := net.Dial(p.network, target)
	if err != nil {
		p.logger.Debug("Failed to determine local IP", "error", err)
		return ""
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// EnvProbe reads environment variables
type EnvProbe struct {
	name    string
	varName string
}

// NewEnvProbe creates a probe that reads an environment variable
func NewEnvProbe(varName string) *EnvProbe {
	return &EnvProbe{
		name:    "env:" + varName,
		varName: varName,
	}
}

func (p *EnvProbe) Name() string { return p.name }

func (p *EnvProbe) Start(ctx context.Context, output chan<- SensorReading) {
	// Env probes are checked on demand, not polled
}

func (p *EnvProbe) Check(ctx context.Context) SensorReading {
	// Implementation would read os.Getenv(p.varName)
	// For now, return empty reading
	return SensorReading{
		Sensor:    p.name,
		Timestamp: time.Now(),
	}
}
