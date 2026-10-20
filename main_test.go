package main

import (
	"encoding/json"
	"net/http"
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
	if !reg.Capabilities.QuotaProvider || !reg.Capabilities.AuthProvider || !reg.Capabilities.ManagementAPI {
		t.Fatalf("Capabilities = %+v; want quota_provider, auth_provider and management_api all true", reg.Capabilities)
	}
	if reg.Capabilities.Scheduler || reg.Capabilities.Executor {
		t.Fatalf("Capabilities = %+v; want every non-declared capability false", reg.Capabilities)
	}
}

func TestHandleMethodManagementRegister(t *testing.T) {
	raw, err := handleMethod(methodManagementRegister, nil)
	if err != nil {
		t.Fatalf("management.register returned error: %v", err)
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	var reg managementRegistrationResponse
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatalf("decode management.register response: %v", err)
	}
	if len(reg.Resources) != 1 || reg.Resources[0].Path != addCredentialPath {
		t.Fatalf("Resources = %+v; want exactly one resource at %q", reg.Resources, addCredentialPath)
	}
	if len(reg.Routes) != 1 || reg.Routes[0].Method != "POST" || reg.Routes[0].Path != addCredentialManagementPath {
		t.Fatalf("Routes = %+v; want exactly one authenticated POST route at %q -- the save action, which a GET-only resource route can't carry", reg.Routes, addCredentialManagementPath)
	}
}

func TestHandleManagementHandleRendersForm(t *testing.T) {
	req := managementRequest{Method: "GET", Path: addCredentialPath}
	reqJSON, _ := json.Marshal(req)
	raw, err := handleMethod(methodManagementHandle, reqJSON)
	if err != nil {
		t.Fatalf("management.handle returned error: %v", err)
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	var resp managementResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("decode management.handle response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d; want 200", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), `name="api_key"`) {
		t.Fatalf("Body does not contain an api_key field: %s", resp.Body)
	}
	if !strings.Contains(string(resp.Body), `name="mgmt_key"`) {
		t.Fatalf("Body does not contain a management-key field: %s", resp.Body)
	}
	if !strings.Contains(string(resp.Body), "fetch(") {
		t.Fatalf("Body has no fetch() call -- a plain <form method=POST> can't attach the Authorization header the save route requires")
	}
}

func TestHandleAddCredentialSubmitRejectsEmptyKey(t *testing.T) {
	req := managementRequest{Method: "POST", Path: addCredentialPath, Body: []byte("api_key=&label=x")}
	reqJSON, _ := json.Marshal(req)
	raw, err := handleMethod(methodManagementHandle, reqJSON)
	if err != nil {
		t.Fatalf("management.handle returned error: %v", err)
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	var resp managementResponse
	_ = json.Unmarshal(env.Result, &resp)
	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400 for an empty api_key", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "required") {
		t.Fatalf("Body = %s; want an explanation that the key is required", resp.Body)
	}
}

func TestHandleAddCredentialSubmitCallsHostAuthSave(t *testing.T) {
	// callHost talks to the real host over cgo and is not mockable from a
	// plain Go test; this only confirms the plugin fails cleanly (rather
	// than panicking or reporting success) when no host is attached, which
	// is exactly what happens when main_test.go runs outside the proxy.
	req := managementRequest{Method: "POST", Path: addCredentialPath, Body: []byte("api_key=sk-test")}
	reqJSON, _ := json.Marshal(req)
	raw, err := handleMethod(methodManagementHandle, reqJSON)
	if err != nil {
		t.Fatalf("management.handle returned error: %v", err)
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	var resp managementResponse
	_ = json.Unmarshal(env.Result, &resp)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d; want 502 when host.auth.save has no real host to answer it", resp.StatusCode)
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

func TestHandleMethodAuthParseGivesDistinctFilesDistinctIDs(t *testing.T) {
	// The host recognizes "the same credential across reloads" by
	// AuthData.ID. A constant ID here would make a second key, placed
	// under a different file name, silently collide onto the first
	// instead of appearing as its own quota card -- the bug this test
	// guards against.
	parse := func(fileName string) authParseResponse {
		req := authParseRequest{FileName: fileName, RawJSON: []byte(`{"api_key":"sk-test"}`)}
		reqJSON, _ := json.Marshal(req)
		raw, err := handleMethod(methodAuthParse, reqJSON)
		if err != nil {
			t.Fatalf("auth.parse(%q) returned error: %v", fileName, err)
		}
		var env envelope
		_ = json.Unmarshal(raw, &env)
		var parsed authParseResponse
		_ = json.Unmarshal(env.Result, &parsed)
		return parsed
	}

	first := parse("glm-quota-personal.json")
	second := parse("glm-quota-work.json")

	if first.Auth.ID == second.Auth.ID {
		t.Fatalf("two differently-named credentials got the same ID %q; a second key would overwrite the first", first.Auth.ID)
	}
	if first.Auth.Provider != second.Auth.Provider {
		t.Fatalf("Provider differs between credentials (%q vs %q); both must share one provider key so one quota_provider registration covers every key", first.Auth.Provider, second.Auth.Provider)
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
