package servercli

import (
	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
)

func newClientConfigForServer() *cliutil.ClientConfig {
	return &cliutil.ClientConfig{
		ServerURL:          cliutil.NormalizeServerURL(g.server),
		Token:              g.token,
		Username:           g.username,
		InsecureSkipVerify: g.insecure,
	}
}

func loginRemote(cfg *cliutil.ClientConfig) (*cliutil.LoginResult, error) {
	return cliutil.Login(cfg, g.username, g.password)
}

func newAPIClient(cfg *cliutil.ClientConfig) *cliutil.Client {
	return cliutil.NewClient(cfg)
}
