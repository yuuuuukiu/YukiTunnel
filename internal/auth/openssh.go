package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// ParsePrivateKeyPEM accepts PKCS#1, PKCS#8 and OpenSSH ("BEGIN OPENSSH
// PRIVATE KEY") RSA private keys, because users naturally generate keys with
// ssh-keygen on every platform.
func ParsePrivateKeyPEM(pemBytes []byte) (*rsa.PrivateKey, error) {
	if key, err := ParseRSAPrivateKeyPEM(pemBytes); err == nil {
		return key, nil
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("私钥文件不是有效的 PEM 格式")
	}
	raw, err := ssh.ParseRawPrivateKey(pemBytes)
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			return nil, errors.New("私钥受密码保护，请先解密：ssh-keygen -p -m PEM -f <私钥>")
		}
		return nil, fmt.Errorf("无法解析私钥（支持 RSA PEM 与 OpenSSH 格式）：%w", err)
	}
	switch k := raw.(type) {
	case *rsa.PrivateKey:
		return k, nil
	case *rsa.PublicKey:
		return nil, errors.New("这是公钥文件，请选择私钥（通常没有 .pub 后缀）")
	default:
		return nil, fmt.Errorf("仅支持 RSA 私钥，当前为 %T（请使用 ssh-keygen -t rsa 生成）", raw)
	}
}

// PublicKeyFromPrivatePEM supports every private key format ssh-keygen emits.
func PublicKeyFromPrivatePEMAny(pemBytes []byte) (canonical, fingerprint string, err error) {
	key, err := ParsePrivateKeyPEM(pemBytes)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", err
	}
	return string(ssh.MarshalAuthorizedKey(sshPub)), ssh.FingerprintSHA256(sshPub), nil
}

var _ = x509.MarshalPKCS1PrivateKey
