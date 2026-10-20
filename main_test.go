package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func registerResponse(t *testing.T, configYAML string) registrationResponse {
	t.Helper()
	reqJSON, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte(configYAML), SchemaVersion: schemaVersion})
	if err != nil {
		t.Fatalf("marshal lifecycle request: %v", err)
	}
	raw, err := handleMethod(methodPluginRegister, reqJSON)
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
	return reg
}

func TestHandleMethodRegister(t *testing.T) {
	reg := registerResponse(t, "cpa-config-path: /tmp/does-not-exist.yaml\n")

	if !reg.Capabilities.QuotaProvider || !reg.Capabilities.AuthProvider {
		t.Fatalf("Capabilities = %+v; want quota_provider and auth_provider true", reg.Capabilities)
	}
	if reg.Capabilities.ManagementAPI || reg.Capabilities.Scheduler || reg.Capabilities.Executor {
		t.Fatalf("Capabilities = %+v; want every other capability false -- this plugin owns no UI and routes no traffic", reg.Capabilities)
	}
	if len(reg.Metadata.ConfigFields) != 1 || reg.Metadata.ConfigFields[0].Name != "cpa-config-path" {
		t.Fatalf("ConfigFields = %+v; want exactly the cpa-config-path field so the panel renders an input for it", reg.Metadata.ConfigFields)
	}
	if reg.Metadata.Logo == "" {
		t.Error("Metadata.Logo is empty; the embedded logo should be sent as a data URI")
	}
}

func TestHandleMethodRegisterRejectsMalformedConfig(t *testing.T) {
	reqJSON, _ := json.Marshal(lifecycleRequest{ConfigYAML: []byte("cpa-config-path: [unclosed\n")})
	if _, err := handleMethod(methodPluginRegister, reqJSON); err == nil {
		t.Fatal("a malformed plugin config should fail registration loudly, not register with silent defaults")
	}
}

func TestParseCredentialFile(t *testing.T) {
	t.Run("pointer file", func(t *testing.T) {
		raw := []byte(`{"type":"glm-quota","source_provider":"Z.AI","source_key_index":2,"label":"Z.AI #3"}`)
		source, label, err := parseCredentialFile(raw)
		if err != nil {
			t.Fatalf("parseCredentialFile returned error: %v", err)
		}
		if source.Provider != "Z.AI" || source.KeyIndex != 2 {
			t.Errorf("source = %+v; want provider Z.AI index 2", source)
		}
		if label != "Z.AI #3" {
			t.Errorf("label = %q; want the file's own label", label)
		}
	})

	t.Run("label falls back to the provider name", func(t *testing.T) {
		_, label, err := parseCredentialFile([]byte(`{"type":"glm-quota","source_provider":"Z.AI"}`))
		if err != nil {
			t.Fatalf("parseCredentialFile returned error: %v", err)
		}
		if label != "Z.AI" {
			t.Errorf("label = %q; want %q", label, "Z.AI")
		}
	})

	t.Run("carries no key", func(t *testing.T) {
		// The whole point of the pointer file: a key must never be written
		// into auth-dir, so the struct has nowhere to put one.
		raw, err := json.Marshal(credentialFile{Type: pluginIdentifier, SourceProvider: "Z.AI"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(strings.ToLower(string(raw)), "api_key") {
			t.Fatalf("credential file %s contains an api_key field; the key must stay in the host config only", raw)
		}
	})

	for _, tc := range []struct {
		name, raw string
	}{
		{"empty", ``},
		{"not json", `nope`},
		{"another provider's file", `{"type":"claude","refresh_token":"x"}`},
		{"missing source_provider", `{"type":"glm-quota"}`},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			if _, _, err := parseCredentialFile([]byte(tc.raw)); err == nil {
				t.Fatalf("parseCredentialFile(%q) succeeded; want an error", tc.raw)
			}
		})
	}
}

func TestHandleAuthParseIgnoresForeignFiles(t *testing.T) {
	// auth-dir holds every provider's credentials. A file this plugin does
	// not own must come back Handled:false, not as an error -- an error
	// would make the host log a failure for every unrelated credential.
	req := authParseRequest{FileName: "claude-abc.json", RawJSON: []byte(`{"type":"claude"}`)}
	reqJSON, _ := json.Marshal(req)
	raw, err := handleMethod(methodAuthParse, reqJSON)
	if err != nil {
		t.Fatalf("auth.parse returned error for a foreign file: %v", err)
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	var parsed authParseResponse
	_ = json.Unmarshal(env.Result, &parsed)
	if parsed.Handled {
		t.Fatal("Handled = true for another provider's credential file")
	}
}

func TestHandleAuthParseGivesDistinctFilesDistinctIDs(t *testing.T) {
	parse := func(fileName, provider string, index int) authParseResponse {
		raw, _ := json.Marshal(credentialFile{Type: pluginIdentifier, SourceProvider: provider, SourceKeyIndex: index})
		reqJSON, _ := json.Marshal(authParseRequest{FileName: fileName, RawJSON: raw})
		got, err := handleMethod(methodAuthParse, reqJSON)
		if err != nil {
			t.Fatalf("auth.parse(%q) returned error: %v", fileName, err)
		}
		var env envelope
		_ = json.Unmarshal(got, &env)
		var parsed authParseResponse
		_ = json.Unmarshal(env.Result, &parsed)
		if !parsed.Handled {
			t.Fatalf("auth.parse(%q) did not handle its own file", fileName)
		}
		return parsed
	}

	first := parse("glm-quota-z-ai.json", "Z.AI", 0)
	second := parse("glm-quota-z-ai-2.json", "Z.AI", 1)

	if first.Auth.ID == second.Auth.ID {
		t.Fatalf("two keys of one provider got the same ID %q; the second would overwrite the first", first.Auth.ID)
	}
	if first.Auth.Provider != second.Auth.Provider {
		t.Fatalf("Provider differs (%q vs %q); both must share one provider key so a single quota_provider registration covers every key", first.Auth.Provider, second.Auth.Provider)
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
		t.Fatalf("quota.reset error = %v; want a 'not supported' error so the host's logs say why", err)
	}
	if _, err := handleMethod("management.handle", nil); err == nil || !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("management.handle error = %v; the capability was dropped, so it must now be unknown", err)
	}
	if _, err := handleMethod("something.else", nil); err == nil || !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("unrecognized method error = %v; want 'unknown method'", err)
	}
}
