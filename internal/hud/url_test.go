package hud

import "testing"

func TestBrowserHost(t *testing.T) {
	tests := []struct {
		name       string
		bindAddr   string
		listenHost string
		want       string
	}{
		{name: "wildcard ipv4", bindAddr: "0.0.0.0", listenHost: "0.0.0.0", want: "127.0.0.1"},
		{name: "wildcard ipv6", bindAddr: "::", listenHost: "::", want: "127.0.0.1"},
		{name: "explicit localhost", bindAddr: "localhost", listenHost: "127.0.0.1", want: "localhost"},
		{name: "explicit ipv6 loopback", bindAddr: "::1", listenHost: "::1", want: "::1"},
		{name: "explicit lan host", bindAddr: "192.168.1.50", listenHost: "192.168.1.50", want: "192.168.1.50"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := browserHost(tt.bindAddr, tt.listenHost); got != tt.want {
				t.Fatalf("browserHost(%q, %q) = %q, want %q", tt.bindAddr, tt.listenHost, got, tt.want)
			}
		})
	}
}
