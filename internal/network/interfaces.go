package network

import (
	"net"
	"sort"
	"strings"
)

type AdapterInfo struct {
	Name       string `json:"name"`
	IP         string `json:"ip"`
	Kind       string `json:"kind"` // "wifi", "hotspot", "ethernet", "virtual", "other"
	IsVirtual  bool   `json:"isVirtual"`
	Priority   int    `json:"priority"`
	Recommended bool  `json:"recommended"`
}

var virtualKeywords = []string{
	"taishan", "meta", "clash", "mihomo", "wintun", "sing-box", "singbox",
	"tun", "tap", "vpn", "wireguard", "tailscale", "zerotier", "nordlynx",
	"vethernet", "hyper-v", "default switch", "vmware", "virtualbox", "vbox",
	"wsl", "docker", "loopback", "bluetooth", "蓝牙", "isatap", "teredo",
	"pseudo", "npcap", "hamachi", "radmin", "cpolar", "frp", "utun",
}

var wifiKeywords = []string{
	"wlan", "wi-fi", "wifi", "wireless", "无线", "802.11",
}

var hotspotKeywords = []string{
	"local area connection*", "本地连接*", "wi-fi direct", "hotspot", "热点",
}

var ethernetKeywords = []string{
	"ethernet", "以太网", "lan", "realtek", "intel", "killer", "broadcom", "gb",
}

func isFakeOrLinkLocal(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return true
	}
	// 127.0.0.0/8 loopback
	if ip4[0] == 127 {
		return true
	}
	// 169.254.0.0/16 APIPA link-local
	if ip4[0] == 169 && ip4[1] == 254 {
		return true
	}
	// 198.18.0.0/15 benchmark / Clash/Mihomo/Meta Fake-IP TUN range!
	if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) {
		return true
	}
	// 100.64.0.0/10 CGNAT (Tailscale / VPN)
	if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return true
	}
	return false
}

func isPrivateIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	if ip4[0] == 10 {
		return true
	}
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return true
	}
	if ip4[0] == 192 && ip4[1] == 168 {
		return true
	}
	return false
}

func containsAny(s string, keys []string) bool {
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// ListAdapters enumerates active IPv4 adapters and ranks physical LAN adapters
// ahead of VPN TUN / Meta Tunnel / Hyper-V virtual switches.
func ListAdapters() []AdapterInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var result []AdapterInfo
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		lowerName := strings.ToLower(iface.Name)
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() {
				continue
			}

			info := AdapterInfo{
				Name: iface.Name,
				IP:   ip4.String(),
				Kind: "other",
			}

			// Detect virtual/VPN/TUN interfaces
			if containsAny(lowerName, virtualKeywords) || isFakeOrLinkLocal(ip4) {
				info.IsVirtual = true
				info.Kind = "virtual"
				info.Priority = 10
			} else if containsAny(lowerName, hotspotKeywords) || ip4.String() == "192.168.137.1" || (ip4[0] == 172 && ip4[1] == 20 && ip4[2] == 10) {
				// Windows Mobile Hotspot (192.168.137.1) or iPhone Personal Hotspot (172.20.10.x)
				info.Kind = "hotspot"
				info.Priority = 95
			} else if containsAny(lowerName, wifiKeywords) {
				info.Kind = "wifi"
				info.Priority = 100
			} else if containsAny(lowerName, ethernetKeywords) {
				info.Kind = "ethernet"
				info.Priority = 85
			} else {
				info.Kind = "other"
				info.Priority = 60
			}

			// Boost standard RFC1918 LAN ranges (especially 192.168.x.x)
			if !info.IsVirtual && isPrivateIPv4(ip4) {
				info.Priority += 5
				if ip4[0] == 192 && ip4[1] == 168 {
					info.Priority += 3
				}
			}

			// Deprioritize Hyper-V / WSL 172.16-31 subnets if name wasn't matched
			if !info.IsVirtual && ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 && !(ip4[1] == 20 && ip4[2] == 10) {
				info.Priority -= 20
			}

			result = append(result, info)
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority > result[j].Priority
		}
		return result[i].Name < result[j].Name
	})

	if len(result) > 0 && !result[0].IsVirtual {
		result[0].Recommended = true
	}

	return result
}

// PickActiveAdapter returns the selected adapter if valid, or the highest-priority physical adapter.
func PickActiveAdapter(selectedName string) (AdapterInfo, []AdapterInfo) {
	adapters := ListAdapters()
	if len(adapters) == 0 {
		return AdapterInfo{Name: "localhost", IP: "127.0.0.1", Kind: "other"}, adapters
	}
	if selectedName != "" {
		for _, a := range adapters {
			if a.Name == selectedName {
				return a, adapters
			}
		}
	}
	return adapters[0], adapters
}

// PhysicalNetInterfaces returns net.Interface structs for non-virtual adapters
// so mDNS only binds to real LAN/Wi-Fi interfaces (avoiding Meta Tunnel / Hyper-V).
func PhysicalNetInterfaces() []net.Interface {
	adapters := ListAdapters()
	allowed := make(map[string]bool)
	for _, a := range adapters {
		if !a.IsVirtual {
			allowed[a.Name] = true
		}
	}
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, iface := range all {
		if allowed[iface.Name] {
			out = append(out, iface)
		}
	}
	return out
}
