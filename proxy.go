//go:build !windows

package main

import (
	"net/http"
)

func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment

	return &http.Client{
		Transport: transport,
		Timeout:   0, // no timeout: downloads can be very long
	}
}
