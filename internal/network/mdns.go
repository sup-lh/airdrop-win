package network

import (
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"
)

var nonHostChar = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// SanitizeMDNSHost produces a clean RFC-compliant mDNS hostname label (without .local).
func SanitizeMDNSHost(name string) string {
	s := strings.TrimSpace(name)
	s = strings.ToLower(s)
	s = nonHostChar.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "airdrop-win"
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

type MDNSAdvertiser struct {
	mu       sync.Mutex
	server   *zeroconf.Server
	hostname string
}

func NewMDNSAdvertiser() *MDNSAdvertiser {
	return &MDNSAdvertiser{}
}

// Start publishes _airdropwin._tcp and <host>.local on physical LAN interfaces only.
func (m *MDNSAdvertiser) Start(deviceName string, ip string, port int) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.server != nil {
		m.server.Shutdown()
		m.server = nil
	}

	hostLabel := SanitizeMDNSHost(deviceName)
	m.hostname = hostLabel + ".local"

	ifaces := PhysicalNetInterfaces()
	var ips []string
	if ip != "" && ip != "127.0.0.1" {
		ips = []string{ip}
	}
	if len(ips) == 0 {
		return ""
	}

	// Filter to matching interface if possible
	var targetIfaces []net.Interface
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.String() == ip {
				targetIfaces = append(targetIfaces, iface)
				break
			}
		}
	}
	if len(targetIfaces) == 0 {
		targetIfaces = ifaces
	}

	txt := []string{
		"app=AirDropWin",
		fmt.Sprintf("device=%s", deviceName),
	}

	srv, err := zeroconf.RegisterProxy(
		deviceName,
		"_http._tcp",
		"local.",
		port,
		hostLabel,
		ips,
		txt,
		targetIfaces,
	)
	if err != nil {
		log.Printf("[mDNS] failed to start mDNS responder (non-fatal): %v", err)
		return ""
	}

	m.server = srv
	log.Printf("[mDNS] advertising http://%s:%d -> %s", m.hostname, port, ip)
	return m.hostname
}

func (m *MDNSAdvertiser) Hostname() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hostname
}

func (m *MDNSAdvertiser) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		m.server.Shutdown()
		m.server = nil
	}
}
