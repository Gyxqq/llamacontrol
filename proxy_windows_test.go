//go:build windows

package main

import "testing"

func TestProxyURLForScheme(t *testing.T) {
	tests := []struct {
		name          string
		proxyServer   string
		requestScheme string
		want          string
	}{
		{
			name:          "plain host port",
			proxyServer:   "127.0.0.1:7890",
			requestScheme: "https",
			want:          "http://127.0.0.1:7890",
		},
		{
			name:          "scheme specific https",
			proxyServer:   "http=127.0.0.1:8080;https=127.0.0.1:7890",
			requestScheme: "https",
			want:          "http://127.0.0.1:7890",
		},
		{
			name:          "fallback to http entry",
			proxyServer:   "http=127.0.0.1:8080",
			requestScheme: "https",
			want:          "http://127.0.0.1:8080",
		},
		{
			name:          "keeps explicit scheme",
			proxyServer:   "socks5://127.0.0.1:1080",
			requestScheme: "https",
			want:          "socks5://127.0.0.1:1080",
		},
		{
			name:          "fallback to socks entry",
			proxyServer:   "socks=127.0.0.1:1080",
			requestScheme: "https",
			want:          "socks5://127.0.0.1:1080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := proxyURLForScheme(tt.proxyServer, tt.requestScheme)
			if err != nil {
				t.Fatalf("proxyURLForScheme returned error: %v", err)
			}
			if got == nil {
				t.Fatal("proxyURLForScheme returned nil")
			}
			if got.String() != tt.want {
				t.Fatalf("proxyURLForScheme() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestShouldBypassWindowsProxy(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		override string
		want     bool
	}{
		{name: "local host", hostname: "localhost", override: "<local>", want: true},
		{name: "wildcard", hostname: "api.example.com", override: "*.example.com", want: true},
		{name: "exact", hostname: "huggingface.co", override: "huggingface.co", want: true},
		{name: "not matched", hostname: "huggingface.co", override: "*.example.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldBypassWindowsProxy(tt.hostname, tt.override)
			if got != tt.want {
				t.Fatalf("shouldBypassWindowsProxy() = %v, want %v", got, tt.want)
			}
		})
	}
}
