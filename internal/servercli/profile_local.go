package servercli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
	"gopkg.in/yaml.v3"
)

// saveProfile stores a verified profile document.
func (l *LocalBackend) saveProfile(ctx context.Context, rec *cloudProfileRecord) error {
	return l.st.UpsertCloudProfile(ctx, &store.CloudProfile{
		ID: rec.ID, Vendor: rec.Vendor, Name: rec.Name, Version: rec.Version,
		Source: rec.Source, SHA256: rec.SHA256, Document: rec.Document, Enabled: true,
	})
}

// saveCredential encrypts and stores cloud credentials using the same scheme as
// the server (AES-256-GCM with a key file next to the database).
func (l *LocalBackend) saveCredential(ctx context.Context, profileID, label string, fields map[string]string) error {
	if label == "" {
		label = "default"
	}
	p, err := l.st.GetCloudProfile(ctx, profileID)
	if err != nil {
		return fmt.Errorf("profile %q 未安装", profileID)
	}
	var doc cloudprofile.Profile
	if err := yaml.Unmarshal([]byte(p.Document), &doc); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, f := range doc.Spec.Auth.CredentialFields {
		allowed[f] = true
	}
	for k := range fields {
		if !allowed[k] {
			return fmt.Errorf("字段 %q 不在 %s 声明的 credential_fields 中（允许：%s）",
				k, profileID, strings.Join(doc.Spec.Auth.CredentialFields, ", "))
		}
	}
	sealed, err := l.seal(fields)
	if err != nil {
		return err
	}
	return l.st.UpsertProfileCredential(ctx, &store.ProfileCredential{
		ID: ids.Hex(12), ProfileID: profileID, Label: label, FieldsJSON: sealed,
	})
}

// runProfile executes a profile locally.
func (l *LocalBackend) runProfile(ctx context.Context, profileID, action string, params map[string]any,
	credLabel, nodeID string, dryRun, ack bool) (*cloudprofile.ExecutionResult, error) {
	if !dryRun && !ack {
		return nil, fmt.Errorf("请加 --ack 确认已知晓 profile 声明的云权限")
	}
	if credLabel == "" {
		credLabel = "default"
	}
	rec, err := l.st.GetCloudProfile(ctx, profileID)
	if err != nil {
		return nil, fmt.Errorf("profile %q 未安装", profileID)
	}
	if !rec.Enabled {
		return nil, fmt.Errorf("profile %q 已禁用", profileID)
	}
	var doc cloudprofile.Profile
	if err := yaml.Unmarshal([]byte(rec.Document), &doc); err != nil {
		return nil, err
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	doc.SHA256 = rec.SHA256

	cred, err := l.st.GetProfileCredential(ctx, profileID, credLabel)
	if err != nil {
		return nil, fmt.Errorf("未找到凭证 %q", credLabel)
	}
	creds, err := l.open(cred.FieldsJSON)
	if err != nil {
		return nil, err
	}
	if nodeID != "" {
		if node, err := l.st.GetNode(ctx, nodeID); err == nil && node.Region != "" {
			if _, ok := params["region"]; !ok {
				params["region"] = node.Region
			}
		}
	}
	eng, err := cloudprofile.NewEngine(l.proxyURL(ctx))
	if err != nil {
		return nil, err
	}
	res, runErr := eng.Run(ctx, &doc, cloudprofile.RunOptions{
		Action: action, Params: params, Creds: creds, DryRun: dryRun,
	})
	run := &store.ProfileRun{
		ID: ids.Hex(12), ProfileID: profileID, ProfileSHA: rec.SHA256, Action: action,
		NodeID: nodeID, Protocol: fmt.Sprint(params["protocol"]),
		PortMin: intFromAny(params["port_min"]), PortMax: intFromAny(params["port_max"]),
		ActorName: "cli",
	}
	if res != nil {
		run.DurationMS = res.Duration.Milliseconds()
		run.Message = res.Message
		run.Status = "success"
		if res.Skipped || res.Status == 0 {
			run.Status = "skipped"
		}
	}
	if runErr != nil {
		run.Status = "failed"
		run.Message = runErr.Error()
	}
	_ = l.st.AddProfileRun(ctx, run)
	if runErr != nil {
		return nil, runErr
	}
	return res, nil
}

func (l *LocalBackend) proxyURL(ctx context.Context) string {
	v, _ := l.st.GetSetting(ctx, "profile_proxy", "")
	return v
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case string:
		var i int
		_, _ = fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}

var _ = json.Marshal
