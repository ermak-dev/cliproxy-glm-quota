package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleMethodRegister(t *testing.T) {
	raw, err := handleMethod(methodPluginRegister, []byte(`{"schema_version":6}`))
	if err != nil {
		t.Fatalf("plugin.register returned error: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("envelope.OK = false; want true, error=%+v", env.Error)
	}
	var reg registrationResponse
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if !reg.Capabilities.QuotaProvider || !reg.Capabilities.AuthProvider {
		t.Fatalf("Capabilities = %+v; want quota_provider and auth_provider both true", reg.Capabilities)
	}
	if reg.Capabilities.Scheduler || reg.Capabilities.Executor || reg.Capabilities.ManagementAPI {
		t.Fatalf("Capabilities = %+v; want every other capability false", reg.Capabilities)
	}
}

func TestHandleMethodAuthParseRoundTrip(t *testing.T) {
	req := authParseRequest{
		Provider: pluginIdentifier,
		FileName: "glm-quota.json",
		RawJSON:  []byte(`{"type":"glm-quota","api_key":"sk-test","label":"Personal GLM key"}`),
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	raw, err := handleMethod(methodAuthParse, reqJSON)
	if err != nil {
		t.Fatalf("auth.parse returned error: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("envelope.OK = false; want true, error=%+v", env.Error)
	}
	var parsed authParseResponse
	if err := json.Unmarshal(env.Result, &parsed); err != nil {
		t.Fatalf("decode auth.parse response: %v", err)
	}
	if !parsed.Handled {
		t.Fatalf("Handled = false; want true")
	}
	if parsed.Auth.Provider != pluginIdentifier {
		t.Errorf("Auth.Provider = %q; want %q", parsed.Auth.Provider, pluginIdentifier)
	}
	if parsed.Auth.Label != "Personal GLM key" {
		t.Errorf("Auth.Label = %q; want the file's own label", parsed.Auth.Label)
	}
	if string(parsed.Auth.StorageJSON) != string(req.RawJSON) {
		t.Errorf("Auth.StorageJSON = %s; want the original file round-tripped unchanged", parsed.Auth.StorageJSON)
	}
}

func TestHandleMethodAuthParseRejectsMissingKey(t *testing.T) {
	req := authParseRequest{RawJSON: []byte(`{"type":"glm-quota"}`)}
	reqJSON, _ := json.Marshal(req)
	if _, err := handleMethod(methodAuthParse, reqJSON); err == nil {
		t.Fatal("auth.parse with no api_key should fail, not silently register an unusable credential")
	}
}

func TestHandleMethodQuotaDescribe(t *testing.T) {
	raw, err := handleMethod(methodQuotaDescribe, nil)
	if err != nil {
		t.Fatalf("quota.describe returned error: %v", err)
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	var desc quotaDescribeResponse
	if err := json.Unmarshal(env.Result, &desc); err != nil {
		t.Fatalf("decode quota.describe response: %v", err)
	}
	if desc.SupportsReset {
		t.Error("SupportsReset = true; z.ai's usage endpoint offers no reset action")
	}
	if len(desc.SupportedProviders) != 1 || desc.SupportedProviders[0] != pluginIdentifier {
		t.Errorf("SupportedProviders = %v; want exactly [%q]", desc.SupportedProviders, pluginIdentifier)
	}
}

func TestHandleMethodUnknownMethod(t *testing.T) {
	if _, err := handleMethod("quota.reset", nil); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("quota.reset error = %v; want a 'not supported' error so the host's logs are honest about why it failed", err)
	}
	if _, err := handleMethod("something.else", nil); err == nil || !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("unrecognized method error = %v; want 'unknown method'", err)
	}
}
