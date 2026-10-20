package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxCPAConfigBytes bounds the config read. CLIProxyAPI's own config is a few
// KB; anything near this is a sign the path points at the wrong file.
const maxCPAConfigBytes = 4 << 20

// zaiBaseURLHosts are the upstream hosts that serve a GLM Coding Plan. An
// openai-compatibility entry is treated as a GLM plan when its base-url
// names one of these -- the entry's own `name` is operator-chosen ("Z.AI",
// "glm", anything) and cannot be matched on.
var zaiBaseURLHosts = []string{"api.z.ai", "open.bigmodel.cn"}

// cpaConfig is the sliver of CLIProxyAPI's config.yaml this plugin reads.
// Everything else in that file is ignored: yaml.v3 leaves unknown fields
// alone by default, so a host config growing new sections cannot break this.
type cpaConfig struct {
	APIKeys struct {
		OpenAICompat []compatProvider `yaml:"openai-compatibility"`
	} `yaml:"api-keys"`
	// Some CLIProxyAPI layouts put the list at the top level instead of
	// under api-keys; both spellings appear in the project's own
	// config.example.yaml, so both are read.
	OpenAICompat []compatProvider `yaml:"openai-compatibility"`
}

type compatProvider struct {
	Name     string      `yaml:"name"`
	BaseURL  string      `yaml:"base-url"`
	Disabled bool        `yaml:"disabled"`
	Keys     []compatKey `yaml:"keys"`
}

type compatKey struct {
	APIKey string `yaml:"api-key"`
}

// glmSource identifies one GLM key by where it lives in the host config,
// never by its value: this is what gets persisted into the auth-dir file, so
// the key itself is read fresh from config.yaml on every quota poll and is
// never copied to a second place on disk.
type glmSource struct {
	// Provider is the openai-compatibility entry's `name`.
	Provider string
	// KeyIndex is the position in that entry's `keys` list, so an entry
	// holding several keys yields one credential (and one quota card) per
	// key rather than silently reporting only the first.
	KeyIndex int
}

func readCPAConfig(path string) (cpaConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return cpaConfig{}, fmt.Errorf("cpa-config-path is not set")
	}
	info, errStat := os.Stat(path)
	if errStat != nil {
		return cpaConfig{}, fmt.Errorf("stat %s: %w", path, errStat)
	}
	if info.Size() > maxCPAConfigBytes {
		return cpaConfig{}, fmt.Errorf("%s is %d bytes, larger than the %d-byte bound", path, info.Size(), maxCPAConfigBytes)
	}
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		return cpaConfig{}, fmt.Errorf("read %s: %w", path, errRead)
	}
	var cfg cpaConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cpaConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// compatProviders merges the two accepted spellings into one list.
func (c cpaConfig) compatProviders() []compatProvider {
	out := make([]compatProvider, 0, len(c.APIKeys.OpenAICompat)+len(c.OpenAICompat))
	out = append(out, c.APIKeys.OpenAICompat...)
	out = append(out, c.OpenAICompat...)
	return out
}

func isGLMBaseURL(baseURL string) bool {
	lowered := strings.ToLower(strings.TrimSpace(baseURL))
	if lowered == "" {
		return false
	}
	for _, host := range zaiBaseURLHosts {
		if strings.Contains(lowered, host) {
			return true
		}
	}
	return false
}

// discoverGLMSources lists every GLM key configured in the host config, in a
// stable order. A disabled entry is skipped: the operator has already said
// it should not be used, and a quota card for it would be noise. An entry
// with an empty key is skipped too -- it cannot be polled, and inventing a
// card that always errors is worse than having none.
func discoverGLMSources(cfg cpaConfig) []glmSource {
	var out []glmSource
	for _, provider := range cfg.compatProviders() {
		if provider.Disabled || !isGLMBaseURL(provider.BaseURL) {
			continue
		}
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			continue
		}
		for index, key := range provider.Keys {
			if strings.TrimSpace(key.APIKey) == "" {
				continue
			}
			out = append(out, glmSource{Provider: name, KeyIndex: index})
		}
	}
	return out
}

// resolveGLMKey returns the key a source currently points at. It is called on
// every quota poll rather than cached, so rotating a key in config.yaml takes
// effect without touching this plugin or its credential files.
func resolveGLMKey(cfg cpaConfig, source glmSource) (string, error) {
	for _, provider := range cfg.compatProviders() {
		if !strings.EqualFold(strings.TrimSpace(provider.Name), strings.TrimSpace(source.Provider)) {
			continue
		}
		if source.KeyIndex < 0 || source.KeyIndex >= len(provider.Keys) {
			return "", fmt.Errorf("provider %q has %d keys, no key at index %d", source.Provider, len(provider.Keys), source.KeyIndex)
		}
		key := strings.TrimSpace(provider.Keys[source.KeyIndex].APIKey)
		if key == "" {
			return "", fmt.Errorf("provider %q key %d is empty", source.Provider, source.KeyIndex)
		}
		return key, nil
	}
	return "", fmt.Errorf("provider %q is no longer in the host config", source.Provider)
}

// credentialFileName is the auth-dir file for a source. The provider name is
// slugified because it becomes both a file name and, through
// credentialIDFromFileName, the credential's stable ID.
func credentialFileName(source glmSource) string {
	slug := slugify(source.Provider)
	if slug == "" {
		slug = "provider"
	}
	if source.KeyIndex == 0 {
		return fmt.Sprintf("%s-%s.json", pluginIdentifier, slug)
	}
	return fmt.Sprintf("%s-%s-%d.json", pluginIdentifier, slug, source.KeyIndex+1)
}

// slugify turns an operator-entered provider name into a safe file-name
// fragment: lowercase ASCII letters, digits and hyphens only, with no
// leading, trailing or repeated hyphens.
func slugify(s string) string {
	var b strings.Builder
	lastHyphen := true // true keeps a leading hyphen from ever being written
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
