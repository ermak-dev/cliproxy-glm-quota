package main

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleCPAConfig = `"config-version": 8
"api-keys":
    "openai-compatibility":
        - "name": "Z.AI"
          "base-url": "https://api.z.ai/api/coding/paas/v4"
          "keys":
              - "api-key": "zai-key-one"
              - "api-key": "zai-key-two"
        - "name": "DeepSeek"
          "base-url": "https://api.deepseek.com"
          "keys":
              - "api-key": "deepseek-key"
        - "name": "Z.AI-disabled"
          "base-url": "https://api.z.ai/api/coding/paas/v4"
          "disabled": true
          "keys":
              - "api-key": "disabled-key"
        - "name": "BigModel"
          "base-url": "https://open.bigmodel.cn/api/paas/v4"
          "keys":
              - "api-key": ""
`

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestDiscoverGLMSources(t *testing.T) {
	cfg, err := readCPAConfig(writeTempConfig(t, sampleCPAConfig))
	if err != nil {
		t.Fatalf("readCPAConfig returned error: %v", err)
	}
	sources := discoverGLMSources(cfg)

	// Two Z.AI keys. The disabled entry, the empty-key BigModel entry and
	// the non-GLM DeepSeek entry are all skipped; DeepSeek's own key is
	// irrelevant because its base-url is not a GLM host.
	if len(sources) != 2 {
		t.Fatalf("discoverGLMSources = %+v; want 2 sources (both Z.AI keys)", sources)
	}
	want := []glmSource{{Provider: "Z.AI", KeyIndex: 0}, {Provider: "Z.AI", KeyIndex: 1}}
	for i, w := range want {
		if sources[i] != w {
			t.Errorf("sources[%d] = %+v; want %+v", i, sources[i], w)
		}
	}
}

func TestIsGLMBaseURL(t *testing.T) {
	for _, tc := range []struct {
		url string
		glm bool
		why string
	}{
		{"https://api.z.ai/api/coding/paas/v4", true, "canonical z.ai coding plan"},
		{"https://api.z.ai/api/anthropic", true, "z.ai anthropic-compatible surface"},
		{"https://open.bigmodel.cn/api/paas/v4", true, "BigModel CN platform"},
		{"https://API.Z.AI/v1", true, "host matching is case-insensitive"},
		{"https://api.deepseek.com", false, "another provider"},
		{"", false, "empty base-url"},
		{"https://api.z.ai.evil.example/v1", true, "deliberately lenient: contains-match keeps a lookalike in"},
	} {
		if got := isGLMBaseURL(tc.url); got != tc.glm {
			t.Errorf("isGLMBaseURL(%q) = %v; want %v (%s)", tc.url, got, tc.glm, tc.why)
		}
	}
}

func TestResolveGLMKey(t *testing.T) {
	cfg, err := readCPAConfig(writeTempConfig(t, sampleCPAConfig))
	if err != nil {
		t.Fatalf("readCPAConfig returned error: %v", err)
	}

	for _, tc := range []struct {
		source glmSource
		want   string
	}{
		{glmSource{Provider: "Z.AI", KeyIndex: 0}, "zai-key-one"},
		{glmSource{Provider: "Z.AI", KeyIndex: 1}, "zai-key-two"},
		{glmSource{Provider: "BigModel", KeyIndex: 0}, ""},
	} {
		got, errKey := resolveGLMKey(cfg, tc.source)
		if tc.want == "" {
			if errKey == nil {
				t.Errorf("resolveGLMKey(%+v) = %q, nil; want an error for an empty key", tc.source, got)
			}
			continue
		}
		if errKey != nil {
			t.Errorf("resolveGLMKey(%+v) returned error: %v", tc.source, errKey)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveGLMKey(%+v) = %q; want %q", tc.source, got, tc.want)
		}
	}

	if _, err := resolveGLMKey(cfg, glmSource{Provider: "Z.AI", KeyIndex: 9}); err == nil {
		t.Error("resolveGLMKey accepted an out-of-range key index")
	}
	if _, err := resolveGLMKey(cfg, glmSource{Provider: "Removed"}); err == nil {
		t.Error("resolveGLMKey accepted a provider that is no longer configured")
	}
}

func TestReadCPAConfigBounds(t *testing.T) {
	if _, err := readCPAConfig(""); err == nil {
		t.Error("readCPAConfig accepted an empty path")
	}
	if _, err := readCPAConfig(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("readCPAConfig accepted a missing file")
	}
	if _, err := readCPAConfig(writeTempConfig(t, "not: [valid\n")); err == nil {
		t.Error("readCPAConfig accepted malformed YAML")
	}
}

func TestCredentialFileName(t *testing.T) {
	for _, tc := range []struct {
		source glmSource
		want   string
	}{
		{glmSource{Provider: "Z.AI", KeyIndex: 0}, "glm-quota-z-ai.json"},
		{glmSource{Provider: "Z.AI", KeyIndex: 1}, "glm-quota-z-ai-2.json"},
		{glmSource{Provider: "  ", KeyIndex: 0}, "glm-quota-provider.json"},
	} {
		if got := credentialFileName(tc.source); got != tc.want {
			t.Errorf("credentialFileName(%+v) = %q; want %q", tc.source, got, tc.want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Z.AI", "z-ai"},
		{"Team 2", "team-2"},
		{"  spaced out  ", "spaced-out"},
		{"", ""},
		{"---", ""},
		{"already-a-slug", "already-a-slug"},
	}
	for _, tc := range cases {
		if got := slugify(tc.in); got != tc.want {
			t.Errorf("slugify(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
