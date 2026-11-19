package network

import (
	"testing"
)

func TestListAdaptersRanksPhysicalOverVirtual(t *testing.T) {
	adapters := ListAdapters()
	if len(adapters) == 0 {
		t.Skip("no active non-loopback network adapters")
	}
	t.Logf("Detected adapters:")
	for _, a := range adapters {
		t.Logf("  name=%q ip=%s kind=%s virtual=%v priority=%d rec=%v",
			a.Name, a.IP, a.Kind, a.IsVirtual, a.Priority, a.Recommended)
	}

	active, _ := PickActiveAdapter("")
	if active.IsVirtual {
		// Only fail if a physical adapter actually existed
		for _, a := range adapters {
			if !a.IsVirtual {
				t.Fatalf("picked virtual adapter %q (%s) even though physical adapter %q (%s) exists",
					active.Name, active.IP, a.Name, a.IP)
			}
		}
	}
}

func TestSanitizeMDNSHost(t *testing.T) {
	cases := map[string]string{
		"DESKTOP-ABC123": "desktop-abc123",
		"我的电脑 PC!":       "pc",
		"   ":            "airdrop-win",
		"My_Work--PC":    "my-work-pc",
	}
	for in, want := range cases {
		if got := SanitizeMDNSHost(in); got != want {
			t.Errorf("SanitizeMDNSHost(%q) = %q, want %q", in, got, want)
		}
	}
}
