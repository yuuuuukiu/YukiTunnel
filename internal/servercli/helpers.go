package servercli

import (
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/tlsutil"
)

func insecureClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // localhost self-signed probe
		},
	}
}

func fingerprintPEM(b []byte) (string, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return "", errInvalidPEM{}
	}
	return tlsutil.Fingerprint(b)
}

type errInvalidPEM struct{}

func (errInvalidPEM) Error() string { return "证书文件不是有效的 PEM" }
