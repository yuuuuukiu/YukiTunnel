package cloudprofile

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Aliyun: RPC style, HMAC-SHA1 over a canonicalised query string.
//
// StringToSign = "POST" + "&" + percentEncode("/") + "&" + percentEncode(canonicalQuery)
// Signature   = Base64(HMAC-SHA1(AccessKeySecret + "&", StringToSign))
// ---------------------------------------------------------------------------

// AliyunSigner implements aliyun-hmac-sha1.
type AliyunSigner struct {
	// Now is injectable for deterministic tests.
	Now func() time.Time
}

// NewAliyunSigner builds the Aliyun signer.
func NewAliyunSigner() *AliyunSigner { return &AliyunSigner{Now: time.Now} }

// Name returns the engine id used by profiles.
func (s *AliyunSigner) Name() string { return "aliyun-hmac-sha1" }

// Sign adds the common parameters and the Signature field.
func (s *AliyunSigner) Sign(ctx context.Context, req *Request, creds Credentials) error {
	ak := creds["access_key_id"]
	sk := creds["access_key_secret"]
	if ak == "" || sk == "" {
		return fmt.Errorf("missing access_key_id / access_key_secret")
	}
	if req.FormBody == nil {
		req.FormBody = map[string]string{}
	}
	req.FormBody["AccessKeyId"] = ak
	req.FormBody["Format"] = "JSON"
	req.FormBody["SignatureMethod"] = "HMAC-SHA1"
	req.FormBody["SignatureVersion"] = "1.0"
	req.FormBody["SignatureNonce"] = nonce()
	req.FormBody["Timestamp"] = s.Now().UTC().Format("2006-01-02T15:04:05Z")
	if req.FormBody["Version"] == "" {
		req.FormBody["Version"] = "2014-05-26"
	}
	canonical := canonicalQuery(req.FormBody)
	stringToSign := "POST&" + percentEncode("/") + "&" + percentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(sk+"&"))
	mac.Write([]byte(stringToSign))
	req.FormBody["Signature"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return nil
}

// percentEncode implements RFC 3986 encoding as Aliyun requires.
func percentEncode(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	e = strings.ReplaceAll(e, "%7E", "~")
	return e
}

func canonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, percentEncode(k)+"="+percentEncode(params[k]))
	}
	return strings.Join(parts, "&")
}

func nonce() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix()%100000)
}

// ---------------------------------------------------------------------------
// Tencent Cloud: TC3-HMAC-SHA256.
//
// CanonicalRequest = HTTPMethod \n CanonicalURI \n CanonicalQueryString \n
//                    CanonicalHeaders \n SignedHeaders \n HashedRequestPayload
// StringToSign     = "TC3-HMAC-SHA256" \n timestamp \n date/service/tc3_request \n hash(CanonicalRequest)
// SecretSigning    = HMAC(HMAC(HMAC("TC3"+SecretKey, date), service), "tc3_request")
// ---------------------------------------------------------------------------

// TencentSigner implements tencent-hmac-sha256.
type TencentSigner struct {
	Now func() time.Time
}

// NewTencentSigner builds the Tencent signer.
func NewTencentSigner() *TencentSigner { return &TencentSigner{Now: time.Now} }

// Name returns the engine id.
func (s *TencentSigner) Name() string { return "tencent-hmac-sha256" }

// Sign sets X-TC-* headers and Authorization.
func (s *TencentSigner) Sign(ctx context.Context, req *Request, creds Credentials) error {
	secretID := creds["secret_id"]
	secretKey := creds["secret_key"]
	if secretID == "" || secretKey == "" {
		return fmt.Errorf("missing secret_id / secret_key")
	}
	region := creds["region"]
	service := "vpc"
	now := s.Now().UTC()
	ts := now.Unix()
	date := now.Format("2006-01-02")

	payload := req.Body
	if req.FormBody != nil {
		form := url.Values{}
		for k, v := range req.FormBody {
			form.Set(k, v)
		}
		payload = []byte(form.Encode())
	}
	hashedPayload := sha256Hex(payload)

	u, err := url.Parse(req.URL)
	if err != nil {
		return err
	}
	canonicalURI := "/"
	if u.Path != "" {
		canonicalURI = u.Path
	}
	canonicalQuery := u.RawQuery
	host := u.Host
	contentType := req.Headers["Content-Type"]
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
		req.Headers["Content-Type"] = contentType
	}
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-tc-action:%s\n",
		strings.ToLower(contentType), strings.ToLower(host), strings.ToLower(req.Operation))
	signedHeaders := "content-type;host;x-tc-action"
	canonicalRequest := strings.Join([]string{
		strings.ToUpper(req.Method), canonicalURI, canonicalQuery,
		canonicalHeaders, signedHeaders, hashedPayload,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/tc3_request", date, service)
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256", fmt.Sprint(ts), credentialScope, sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	req.Headers["Authorization"] = fmt.Sprintf(
		"TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		secretID, credentialScope, signedHeaders, signature)
	req.Headers["X-TC-Action"] = req.Operation
	req.Headers["X-TC-Timestamp"] = fmt.Sprint(ts)
	if region != "" {
		req.Headers["X-TC-Region"] = region
	}
	if req.Headers["X-TC-Version"] == "" {
		req.Headers["X-TC-Version"] = "2017-03-12"
	}
	req.Body = payload
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// ---------------------------------------------------------------------------
// AWS: Signature Version 4 for the EC2 query API.
// ---------------------------------------------------------------------------

// AWSSigner implements aws-sigv4.
type AWSSigner struct {
	Now func() time.Time
}

// NewAWSSigV4Signer builds the AWS signer.
func NewAWSSigV4Signer() *AWSSigner { return &AWSSigner{Now: time.Now} }

// Name returns the engine id.
func (s *AWSSigner) Name() string { return "aws-sigv4" }

// Sign implements SigV4 with the "ec2" service and the query protocol.
func (s *AWSSigner) Sign(ctx context.Context, req *Request, creds Credentials) error {
	ak := creds["access_key_id"]
	sk := creds["secret_access_key"]
	if ak == "" || sk == "" {
		return fmt.Errorf("missing access_key_id / secret_access_key")
	}
	region := creds["region"]
	if region == "" {
		region = "us-east-1"
	}
	sessionToken := creds["session_token"]

	if req.FormBody == nil {
		req.FormBody = map[string]string{}
	}
	req.FormBody["Action"] = req.Operation
	if req.FormBody["Version"] == "" {
		req.FormBody["Version"] = "2016-11-15"
	}

	now := s.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	u, err := url.Parse(req.URL)
	if err != nil {
		return err
	}
	form := url.Values{}
	for k, v := range req.FormBody {
		form.Set(k, v)
	}
	payload := form.Encode()
	host := u.Host
	req.Headers["Host"] = host
	req.Headers["X-Amz-Date"] = amzDate
	if sessionToken != "" {
		req.Headers["X-Amz-Security-Token"] = sessionToken
	}

	headerNames := []string{"host", "x-amz-date"}
	if sessionToken != "" {
		headerNames = append(headerNames, "x-amz-security-token")
	}
	sort.Strings(headerNames)
	var canonicalHeaders strings.Builder
	for _, h := range headerNames {
		canonicalHeaders.WriteString(h + ":" + strings.TrimSpace(req.Headers[canonicalHeaderKey(h)]) + "\n")
	}
	signedHeaders := strings.Join(headerNames, ";")
	canonicalRequest := strings.Join([]string{
		strings.ToUpper(req.Method), "/", "", canonicalHeaders.String(), signedHeaders, sha256Hex([]byte(payload)),
	}, "\n")

	scope := fmt.Sprintf("%s/%s/ec2/aws4_request", dateStamp, region)
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, scope, sha256Hex([]byte(canonicalRequest))}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+sk), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, "ec2")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Headers["Authorization"] = fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		ak, scope, signedHeaders, signature)
	req.FormBody = map[string]string{}
	for k := range form {
		req.FormBody[k] = form.Get(k)
	}
	return nil
}

// canonicalHeaderKey maps a lowercase header name to its canonical form.
func canonicalHeaderKey(h string) string {
	parts := strings.Split(h, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "-")
}

// ---------------------------------------------------------------------------
// Huawei Cloud: AK/SK -> IAM token (X-Auth-Token) or, for the v2 VPC API,
// X-Project-Id + X-Auth-Token. The token is obtained separately and passed in
// as a credential, so the signer itself is stateless.
// ---------------------------------------------------------------------------

// HuaweiSigner implements huawei-aksk.
type HuaweiSigner struct{}

// NewHuaweiSigner builds the Huawei signer.
func NewHuaweiSigner() *HuaweiSigner { return &HuaweiSigner{} }

// Name returns the engine id.
func (s *HuaweiSigner) Name() string { return "huawei-aksk" }

// Sign attaches the IAM token and project header.
func (s *HuaweiSigner) Sign(ctx context.Context, req *Request, creds Credentials) error {
	token := creds["iam_token"]
	if token == "" {
		// Fall back to deriving the token from AK/SK via the IAM API.
		return fmt.Errorf("missing iam_token：请先通过 AK/SK 换取 IAM Token（yuki-server profile huawei-token）")
	}
	req.Headers["X-Auth-Token"] = token
	if project := creds["project_id"]; project != "" {
		req.Headers["X-Project-Id"] = project
	}
	if req.Headers["Content-Type"] == "" {
		req.Headers["Content-Type"] = "application/json;charset=utf8"
	}
	return nil
}

// ---------------------------------------------------------------------------
// Azure and GCP use OAuth2 bearer tokens (no request-level signing). The token
// is obtained out of band and passed as a credential; the signer only attaches
// it, keeping the engine free of cloud SDK dependencies.
// ---------------------------------------------------------------------------

// BearerSigner implements azure-oauth2 and gcp-oauth2.
type BearerSigner struct {
	name string
}

// NewAzureSigner builds the Azure bearer signer.
func NewAzureSigner() *BearerSigner { return &BearerSigner{name: "azure-oauth2"} }

// NewGCPSigner builds the GCP bearer signer.
func NewGCPSigner() *BearerSigner { return &BearerSigner{name: "gcp-oauth2"} }

// Name returns the engine id.
func (s *BearerSigner) Name() string { return s.name }

// Sign attaches the bearer token.
func (s *BearerSigner) Sign(ctx context.Context, req *Request, creds Credentials) error {
	token := creds["access_token"]
	if token == "" {
		return fmt.Errorf("missing access_token：请先获取 OAuth2 访问令牌")
	}
	req.Headers["Authorization"] = "Bearer " + token
	if req.Headers["Content-Type"] == "" {
		req.Headers["Content-Type"] = "application/json"
	}
	return nil
}
