package server

import (
	"crypto/tls"
	"net/http"
	"time"
)

// insecureHTTPClient is only used for localhost health probes against our own
// freshly generated self-signed certificate.
func insecureHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // localhost self-probe
		},
	}
}
