//go:build windows

package main

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

type windowsProxyConfig struct {
	enabled  bool
	server   string
	override string
}

func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = windowsSystemProxy

	return &http.Client{
		Transport: transport,
		Timeout:   0, // no timeout: downloads can be very long
	}
}

func windowsSystemProxy(req *http.Request) (*url.URL, error) {
	if proxyURL, err := http.ProxyFromEnvironment(req); proxyURL != nil || err != nil {
		return proxyURL, err
	}

	cfg, err := readWindowsProxyConfig()
	if err != nil || !cfg.enabled || strings.TrimSpace(cfg.server) == "" {
		return nil, nil
	}
	if shouldBypassWindowsProxy(req.URL.Hostname(), cfg.override) {
		return nil, nil
	}
	return proxyURLForScheme(cfg.server, req.URL.Scheme)
}

func readWindowsProxyConfig() (windowsProxyConfig, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return windowsProxyConfig{}, err
	}
	defer key.Close()

	proxyEnable, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil {
		proxyEnable = 0
	}
	proxyServer, _, err := key.GetStringValue("ProxyServer")
	if err != nil {
		proxyServer = ""
	}
	proxyOverride, _, err := key.GetStringValue("ProxyOverride")
	if err != nil {
		proxyOverride = ""
	}

	return windowsProxyConfig{
		enabled:  proxyEnable != 0,
		server:   proxyServer,
		override: proxyOverride,
	}, nil
}

func proxyURLForScheme(proxyServer, requestScheme string) (*url.URL, error) {
	proxyServer = strings.TrimSpace(proxyServer)
	if proxyServer == "" {
		return nil, nil
	}

	if strings.Contains(proxyServer, "=") {
		entries := strings.Split(proxyServer, ";")
		fallback := ""
		fallbackScheme := "http"
		for _, entry := range entries {
			key, value, ok := strings.Cut(strings.TrimSpace(entry), "=")
			if !ok {
				continue
			}
			key = strings.ToLower(strings.TrimSpace(key))
			value = strings.TrimSpace(value)
			if key == requestScheme {
				return parseProxyURL(value, "http")
			}
			if key == "http" && fallback == "" {
				fallback = value
				fallbackScheme = "http"
			}
			if key == "socks" && fallback == "" {
				fallback = value
				fallbackScheme = "socks5"
			}
		}
		if fallback != "" {
			return parseProxyURL(fallback, fallbackScheme)
		}
		return nil, nil
	}

	return parseProxyURL(proxyServer, "http")
}

func parseProxyURL(raw, defaultScheme string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !strings.Contains(raw, "://") {
		raw = defaultScheme + "://" + raw
	}
	return url.Parse(raw)
}

func shouldBypassWindowsProxy(hostname, proxyOverride string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return false
	}

	for _, rule := range strings.Split(proxyOverride, ";") {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "" {
			continue
		}
		if rule == "<local>" && !strings.Contains(hostname, ".") {
			return true
		}
		if matched, _ := path.Match(rule, hostname); matched {
			return true
		}
		if rule == hostname {
			return true
		}
	}
	return false
}
