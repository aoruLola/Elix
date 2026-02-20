package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustedProxyBehavior(t *testing.T) {
	trusted, invalid := parseTrustedProxyCIDRs([]string{"198.51.100.0/24"})
	if len(invalid) != 0 {
		t.Fatalf("unexpected invalid cidrs: %v", invalid)
	}

	tests := []struct {
		name            string
		remoteAddr      string
		xff             string
		trustedProxyNet bool
		want            string
	}{
		{
			name:            "ignore xff without trusted proxies",
			remoteAddr:      "198.51.100.9:12345",
			xff:             "203.0.113.5",
			trustedProxyNet: false,
			want:            "198.51.100.9",
		},
		{
			name:            "use xff from trusted proxy",
			remoteAddr:      "198.51.100.9:12345",
			xff:             "203.0.113.5, 198.51.100.1",
			trustedProxyNet: true,
			want:            "203.0.113.5",
		},
		{
			name:            "ignore xff from untrusted proxy",
			remoteAddr:      "192.0.2.30:3333",
			xff:             "203.0.113.5",
			trustedProxyNet: true,
			want:            "192.0.2.30",
		},
		{
			name:            "fallback to remote ip on invalid xff",
			remoteAddr:      "198.51.100.9:12345",
			xff:             "not-an-ip",
			trustedProxyNet: true,
			want:            "198.51.100.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			if tt.trustedProxyNet {
				s.trustedProxyNets = trusted
			}
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/healthz", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}

			got := s.clientIP(req)
			if got != tt.want {
				t.Fatalf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTrustedProxyCIDRs(t *testing.T) {
	nets, invalid := parseTrustedProxyCIDRs([]string{
		"127.0.0.1/32",
		"bad-cidr",
		"2001:db8::/32",
		"",
	})
	if len(nets) != 2 {
		t.Fatalf("expected 2 valid cidrs, got %d", len(nets))
	}
	if len(invalid) != 1 || invalid[0] != "bad-cidr" {
		t.Fatalf("unexpected invalid cidrs: %v", invalid)
	}
}
