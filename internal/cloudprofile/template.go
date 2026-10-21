package cloudprofile

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

// TemplateFuncs are the only functions a profile may call. Keeping the set
// small and side-effect free is deliberate: a signed profile should never be
// able to run arbitrary logic.
var TemplateFuncs = template.FuncMap{
	"port_range":  portRange,
	"proto":       proto,
	"default":     def,
	"json":        toJSON,
	"upper":       strings.ToUpper,
	"lower":       strings.ToLower,
	"title":       title,
	"join":        strings.Join,
	"quote":       strconv.Quote,
	"trim":        strings.TrimSpace,
	"replace":     strings.ReplaceAll,
	"cidr_suffix": cidrSuffix,
}

// portRange renders a port (or range) in the provider's dialect.
//
//	slash   Aliyun:  80/80, 8000/8010
//	dash    Tencent/Azure/GCP: 80, 8000-8010
//	split   AWS/Huawei: returns "<min>-<max>" which the caller splits
//	string  single quoted string
func portRange(format string, min, max any) (string, error) {
	lo, err := toInt(min)
	if err != nil {
		return "", fmt.Errorf("port_range: %w", err)
	}
	hi, err := toInt(max)
	if err != nil {
		return "", fmt.Errorf("port_range: %w", err)
	}
	if hi < lo {
		lo, hi = hi, lo
	}
	switch format {
	case "slash":
		return fmt.Sprintf("%d/%d", lo, hi), nil
	case "dash", "split":
		if lo == hi {
			return strconv.Itoa(lo), nil
		}
		return fmt.Sprintf("%d-%d", lo, hi), nil
	case "string", "string_array":
		if lo == hi {
			return strconv.Itoa(lo), nil
		}
		return fmt.Sprintf("%d-%d", lo, hi), nil
	default:
		return "", fmt.Errorf("port_range: unknown format %q", format)
	}
}

// proto renders a protocol in the provider's case convention.
func proto(casing, protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp":
		protocol = "tcp"
	case "udp":
		protocol = "udp"
	case "icmp":
		protocol = "icmp"
	case "all", "any", "":
		protocol = "all"
	default:
		protocol = strings.ToLower(protocol)
	}
	switch casing {
	case "upper":
		return strings.ToUpper(protocol)
	case "title":
		if protocol == "all" {
			return "*"
		}
		return strings.ToUpper(protocol[:1]) + protocol[1:]
	case "lower":
		if protocol == "all" {
			return "-1"
		}
		return protocol
	default:
		return protocol
	}
}

func def(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" {
		return fallback
	}
	return s
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

// cidrSuffix converts "0.0.0.0/0" -> "/0" so profiles can pass a bare IP.
func cidrSuffix(cidr string) string {
	if i := strings.IndexByte(cidr, '/'); i >= 0 {
		return cidr[i:]
	}
	return ""
}

func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(n))
	case nil:
		return 0, fmt.Errorf("missing value")
	default:
		return 0, fmt.Errorf("cannot convert %T to int", v)
	}
}

// Render evaluates a template string with the given data.
func Render(text string, data map[string]any) (string, error) {
	tpl, err := template.New("profile").Option("missingkey=error").Funcs(TemplateFuncs).Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", text, err)
	}
	var sb strings.Builder
	if err := tpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render template %q: %w", text, err)
	}
	return sb.String(), nil
}

// RenderParams walks a params map and renders every string value, including
// nested maps and lists. Keys are rendered too (AWS uses "IpPermissions.1.IpProtocol").
func RenderParams(params map[string]any, data map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(params))
	for k, v := range params {
		rk, err := Render(k, data)
		if err != nil {
			return nil, err
		}
		rv, err := renderValue(v, data)
		if err != nil {
			return nil, err
		}
		out[rk] = rv
	}
	return out, nil
}

func renderValue(v any, data map[string]any) (any, error) {
	switch val := v.(type) {
	case string:
		return Render(val, data)
	case map[string]any:
		return RenderParams(val, data)
	case []any:
		out := make([]any, 0, len(val))
		for _, item := range val {
			rv, err := renderValue(item, data)
			if err != nil {
				return nil, err
			}
			out = append(out, rv)
		}
		return out, nil
	default:
		return v, nil
	}
}
