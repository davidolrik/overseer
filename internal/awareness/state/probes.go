package state

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
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
	name         string
	targets      []TCPTarget
	timeout      time.Duration
	interval     time.Duration
	logger       *slog.Logger
	sleepMonitor *SleepMonitor
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
func NewTCPProbe(logger *slog.Logger, sleepMonitor *SleepMonitor) *TCPProbe {
	if logger == nil {
		logger = slog.Default()
	}
	return &TCPProbe{
		name:         "tcp",
		targets:      DefaultTCPTargets(),
		timeout:      5 * time.Second,
		interval:     10 * time.Second,
		logger:       logger,
		sleepMonitor: sleepMonitor,
	}
}

func (p *TCPProbe) Name() string { return p.name }

func (p *TCPProbe) Start(ctx context.Context, output chan<- SensorReading) {
	go func() {
		// Do an initial check immediately (skip if suppressed)
		if p.sleepMonitor == nil || !p.sleepMonitor.IsSuppressed() {
			reading := p.Check(ctx)
			select {
			case output <- reading:
			case <-ctx.Done():
				return
			}
		}

		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Skip if suppressed (sleeping or in grace period)
				if p.sleepMonitor != nil && p.sleepMonitor.IsSuppressed() {
					continue
				}
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

// IPProbe checks public IP address via HTTP services
type IPProbe struct {
	name      string
	httpURLs  []string      // HTTP "what's my IP" service URLs
	resolvers []IPResolver  // DNS resolvers (fallback when HTTP fails)
	timeout   time.Duration
	network   string // "udp4" or "udp6"
	offlineIP string // IP to return when offline
	logger    *slog.Logger

	// Prefix tracking - when > 0, only track the network prefix (e.g., /64 for IPv6)
	// This avoids noise from privacy extension address rotation
	prefixBits int

	// Hysteresis for DNS fallback (DNS is less reliable than HTTP)
	mu             sync.Mutex
	lastStableIP   string
	pendingIP      string
	pendingCount   int
	stabilityCount int // e.g., 2
}

// IPResolver defines a DNS resolver for public IP detection
type IPResolver struct {
	ResolverAddr string // DNS server address (direct IP, e.g., "208.67.222.222:53")
	Hostname     string // Hostname to query
	QueryType    string // "A", "TXT", or "AAAA"
}

// DefaultIPv4HTTPURLs returns the default HTTP services for IPv4 detection
// These services return the client's public IP as plain text
func DefaultIPv4HTTPURLs() []string {
	return []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
		"https://ipinfo.io/ip",
		"https://checkip.amazonaws.com",
	}
}

// DefaultIPv6HTTPURLs returns the default HTTP services for IPv6 detection
func DefaultIPv6HTTPURLs() []string {
	return []string{
		"https://api6.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
		"https://v6.ident.me",
		"https://v6.ipinfo.io/ip",
	}
}

// DefaultIPv4Resolvers returns DNS resolvers for IPv4 detection (fallback)
func DefaultIPv4Resolvers() []IPResolver {
	return []IPResolver{
		{ResolverAddr: "208.67.222.222:53", Hostname: "myip.opendns.com.", QueryType: "A"},
		{ResolverAddr: "208.67.220.220:53", Hostname: "myip.opendns.com.", QueryType: "A"},
		{ResolverAddr: "216.239.32.10:53", Hostname: "o-o.myaddr.l.google.com.", QueryType: "TXT"},
		{ResolverAddr: "193.108.88.1:53", Hostname: "whoami.akamai.net.", QueryType: "A"},
	}
}

// DefaultIPv6Resolvers returns DNS resolvers for IPv6 detection (fallback)
func DefaultIPv6Resolvers() []IPResolver {
	return []IPResolver{
		{ResolverAddr: "[2620:119:35::35]:53", Hostname: "myip.opendns.com.", QueryType: "AAAA"},
		{ResolverAddr: "[2620:119:53::53]:53", Hostname: "myip.opendns.com.", QueryType: "AAAA"},
		{ResolverAddr: "[2001:4860:4802:32::a]:53", Hostname: "o-o.myaddr.l.google.com.", QueryType: "TXT"},
	}
}

// NewIPv4Probe creates a new IPv4 address probe
func NewIPv4Probe(logger *slog.Logger) *IPProbe {
	if logger == nil {
		logger = slog.Default()
	}
	return &IPProbe{
		name:           "public_ipv4",
		httpURLs:       DefaultIPv4HTTPURLs(),
		resolvers:      DefaultIPv4Resolvers(),
		timeout:        10 * time.Second,
		network:        "udp4",
		offlineIP:      "169.254.0.0",
		logger:         logger,
		stabilityCount: 2,
	}
}

// NewIPv6Probe creates a new IPv6 address probe
func NewIPv6Probe(logger *slog.Logger) *IPProbe {
	if logger == nil {
		logger = slog.Default()
	}
	return &IPProbe{
		name:           "public_ipv6",
		httpURLs:       DefaultIPv6HTTPURLs(),
		resolvers:      DefaultIPv6Resolvers(),
		timeout:        10 * time.Second,
		network:        "udp6",
		offlineIP:      "fe80::",
		logger:         logger,
		prefixBits:     64, // Only track /64 prefix to avoid privacy extension noise
		stabilityCount: 4,  // Higher than IPv4 to filter brief network hiccups
	}
}

func (p *IPProbe) Name() string { return p.name }

func (p *IPProbe) Start(ctx context.Context, output chan<- SensorReading) {
	// IP probes don't poll continuously - they're checked on demand
	// or when triggered by network changes
	p.logger.Debug("IP probe ready", "name", p.name)
}

// normalizeToPrefix extracts the network prefix from an IP address
// For example, with prefixBits=64: "2a05:f6c3:dd4d:0:1234:5678:9abc:def0" -> "2a05:f6c3:dd4d::"
func (p *IPProbe) normalizeToPrefix(ipStr string) string {
	if p.prefixBits <= 0 {
		return ipStr
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ipStr
	}

	// Create a mask for the prefix
	var mask net.IPMask
	if ip.To4() != nil {
		mask = net.CIDRMask(p.prefixBits, 32)
	} else {
		mask = net.CIDRMask(p.prefixBits, 128)
	}

	// Apply the mask to get just the network portion
	network := ip.Mask(mask)
	if network == nil {
		return ipStr
	}

	return network.String()
}

// checkHTTP queries HTTP "what's my IP" services and returns consensus IP.
// Queries all services in parallel and returns the IP that 2+ services agree on.
func (p *IPProbe) checkHTTP(ctx context.Context) string {
	if len(p.httpURLs) == 0 {
		return ""
	}

	// Create HTTP client with appropriate transport for IPv4/IPv6
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Force IPv4 or IPv6 based on probe type
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			if p.network == "udp6" {
				return dialer.DialContext(ctx, "tcp6", addr)
			}
			return dialer.DialContext(ctx, "tcp4", addr)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	// Query all services in parallel
	type result struct {
		ip  string
		url string
	}
	results := make(chan result, len(p.httpURLs))

	for _, url := range p.httpURLs {
		go func(url string) {
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				results <- result{url: url} // empty ip signals failure
				return
			}

			resp, err := client.Do(req)
			if err != nil {
				p.logger.Debug("HTTP IP check failed", "url", url, "error", err)
				results <- result{url: url}
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				p.logger.Debug("HTTP IP check non-200", "url", url, "status", resp.StatusCode)
				results <- result{url: url}
				return
			}

			// Read response (limit to 64 bytes - an IP address is much smaller)
			body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
			if err != nil {
				results <- result{url: url}
				return
			}

			ipStr := strings.TrimSpace(string(body))

			// Validate it's a proper IP address
			ip := net.ParseIP(ipStr)
			if ip == nil {
				p.logger.Debug("HTTP IP check invalid response", "url", url, "response", ipStr)
				results <- result{url: url}
				return
			}

			// For IPv4 probe, ensure we got an IPv4 address
			if p.network == "udp4" && ip.To4() == nil {
				p.logger.Debug("HTTP IP check returned IPv6 for IPv4 probe", "url", url, "ip", ipStr)
				results <- result{url: url}
				return
			}

			// For IPv6 probe, ensure we got an IPv6 address
			if p.network == "udp6" && ip.To4() != nil {
				p.logger.Debug("HTTP IP check returned IPv4 for IPv6 probe", "url", url, "ip", ipStr)
				results <- result{url: url}
				return
			}

			results <- result{ip: ipStr, url: url}
		}(url)
	}

	// Collect results until we have consensus (2+ agree)
	votes := make(map[string]int)
	timeout := time.After(6 * time.Second)
	received := 0
collect:
	for received < len(p.httpURLs) {
		select {
		case r := <-results:
			received++
			if r.ip != "" {
				votes[r.ip]++
				// Return early once we have consensus
				if votes[r.ip] >= 2 {
					p.logger.Debug("HTTP IP consensus reached", "ip", r.ip, "votes", votes[r.ip])
					return r.ip
				}
			}
		case <-ctx.Done():
			return ""
		case <-timeout:
			break collect
		}
	}

	// No consensus reached
	if len(votes) > 0 {
		p.logger.Debug("HTTP IP detection failed to reach consensus", "votes", votes)
	}

	return ""
}

// checkDNS queries DNS resolvers and returns consensus IP.
// Uses direct IP addresses for DNS servers, so works even when system DNS is broken.
func (p *IPProbe) checkDNS(ctx context.Context) string {
	if len(p.resolvers) == 0 {
		return ""
	}

	type result struct {
		ip       string
		resolver string
	}
	results := make(chan result, len(p.resolvers))

	for _, resolver := range p.resolvers {
		go func(r IPResolver) {
			ip := p.queryResolver(ctx, r)
			results <- result{ip: ip, resolver: r.ResolverAddr}
		}(resolver)
	}

	// Collect results and find consensus
	votes := make(map[string]int)
	timeout := time.After(6 * time.Second)
	received := 0

collect:
	for received < len(p.resolvers) {
		select {
		case r := <-results:
			received++
			if r.ip != "" {
				votes[r.ip]++
				// Return early once we have consensus (2+ agree)
				if votes[r.ip] >= 2 {
					p.logger.Debug("DNS IP consensus reached", "ip", r.ip, "votes", votes[r.ip])
					return r.ip
				}
			}
		case <-ctx.Done():
			return ""
		case <-timeout:
			break collect
		}
	}

	// No consensus - return the IP with most votes if any
	var bestIP string
	var bestVotes int
	for ip, v := range votes {
		if v > bestVotes {
			bestIP = ip
			bestVotes = v
		}
	}

	if bestVotes > 0 {
		p.logger.Debug("DNS IP detection no consensus, using best", "ip", bestIP, "votes", bestVotes)
		return bestIP
	}

	return ""
}

// queryResolver queries a single DNS resolver for the public IP
func (p *IPProbe) queryResolver(ctx context.Context, r IPResolver) string {
	// Create a custom resolver using the direct IP address
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			// Use the resolver's direct IP address
			return d.DialContext(ctx, network, r.ResolverAddr)
		},
	}

	switch r.QueryType {
	case "A":
		ips, err := resolver.LookupIP(ctx, "ip4", r.Hostname)
		if err != nil || len(ips) == 0 {
			p.logger.Debug("DNS A lookup failed", "resolver", r.ResolverAddr, "hostname", r.Hostname, "error", err)
			return ""
		}
		return ips[0].String()

	case "AAAA":
		ips, err := resolver.LookupIP(ctx, "ip6", r.Hostname)
		if err != nil || len(ips) == 0 {
			p.logger.Debug("DNS AAAA lookup failed", "resolver", r.ResolverAddr, "hostname", r.Hostname, "error", err)
			return ""
		}
		return ips[0].String()

	case "TXT":
		txts, err := resolver.LookupTXT(ctx, r.Hostname)
		if err != nil || len(txts) == 0 {
			p.logger.Debug("DNS TXT lookup failed", "resolver", r.ResolverAddr, "hostname", r.Hostname, "error", err)
			return ""
		}
		// TXT records contain the IP as the value
		ipStr := strings.TrimSpace(txts[0])
		// Remove any surrounding quotes
		ipStr = strings.Trim(ipStr, "\"")
		ip := net.ParseIP(ipStr)
		if ip == nil {
			p.logger.Debug("DNS TXT returned invalid IP", "resolver", r.ResolverAddr, "response", ipStr)
			return ""
		}
		return ip.String()

	default:
		return ""
	}
}

// applyHysteresis applies hysteresis to DNS results to prevent flapping.
// Requires N consecutive identical readings before accepting a change.
func (p *IPProbe) applyHysteresis(newIP string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	// If no stable IP yet, accept immediately
	if p.lastStableIP == "" {
		p.lastStableIP = newIP
		p.pendingIP = ""
		p.pendingCount = 0
		return newIP
	}

	// If same as stable IP, reset pending state
	if newIP == p.lastStableIP {
		p.pendingIP = ""
		p.pendingCount = 0
		return p.lastStableIP
	}

	// Different IP - apply hysteresis
	if newIP == p.pendingIP {
		p.pendingCount++
		if p.pendingCount >= p.stabilityCount {
			// Stable enough - accept the change
			p.logger.Debug("DNS IP change accepted after hysteresis",
				"old", p.lastStableIP, "new", newIP, "count", p.pendingCount)
			p.lastStableIP = newIP
			p.pendingIP = ""
			p.pendingCount = 0
			return newIP
		}
		// Not stable enough yet
		p.logger.Debug("DNS IP pending hysteresis",
			"stable", p.lastStableIP, "pending", p.pendingIP, "count", p.pendingCount)
		return p.lastStableIP
	}

	// New pending IP - start counting
	p.pendingIP = newIP
	p.pendingCount = 1
	return p.lastStableIP
}

func (p *IPProbe) Check(ctx context.Context) SensorReading {
	start := time.Now()

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	detectedIP := p.checkHTTP(ctx)

	if detectedIP == "" {
		// Fall back to DNS if HTTP failed (e.g., DNS resolution broken)
		p.logger.Debug("HTTP IP detection failed, trying DNS fallback", "sensor", p.name)
		dnsIP := p.checkDNS(ctx)
		// Apply hysteresis only to DNS results (less reliable)
		detectedIP = p.applyHysteresis(dnsIP)
	}

	// Normalize to prefix if configured (e.g., /64 for IPv6)
	if detectedIP != "" && p.prefixBits > 0 {
		detectedIP = p.normalizeToPrefix(detectedIP)
	}

	if detectedIP == "" {
		// No consensus - return offline IP
		offlineIP := p.offlineIP
		if p.prefixBits > 0 {
			offlineIP = p.normalizeToPrefix(offlineIP)
		}
		return SensorReading{
			Sensor:    p.name,
			Timestamp: time.Now(),
			IP:        net.ParseIP(offlineIP),
			Value:     offlineIP,
			Latency:   time.Since(start),
		}
	}

	return SensorReading{
		Sensor:    p.name,
		Timestamp: time.Now(),
		IP:        net.ParseIP(detectedIP),
		Value:     detectedIP,
		Latency:   time.Since(start),
	}
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

	// Sleep/wake awareness
	sleepMonitor *SleepMonitor

	mu            sync.Mutex
	lastCheckTime time.Time
	minInterval   time.Duration // Minimum time between checks (debounce)
}

// NewNetworkMonitorProbe creates a new network monitor probe
func NewNetworkMonitorProbe(ipv4 *IPProbe, ipv6 *IPProbe, localIPv4 *LocalIPProbe, sleepMonitor *SleepMonitor, logger *slog.Logger) *NetworkMonitorProbe {
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
		sleepMonitor:   sleepMonitor,
		minInterval:    2 * time.Second,
	}
}

func (p *NetworkMonitorProbe) Name() string { return p.name }

func (p *NetworkMonitorProbe) Start(ctx context.Context, output chan<- SensorReading) {
	go func() {
		// Do an initial check immediately so IP sensors are populated early
		if p.sleepMonitor == nil || !p.sleepMonitor.IsSuppressed() {
			p.checkAndEmit(ctx, output)
		}

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
	// Skip checks during sleep and wake grace period
	if p.sleepMonitor != nil && p.sleepMonitor.IsSuppressed() {
		return
	}

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
	value := os.Getenv(p.varName)
	return SensorReading{
		Sensor:    p.name,
		Value:     value,
		Timestamp: time.Now(),
	}
}
