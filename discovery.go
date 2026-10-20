package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// credentialFile is what this plugin writes into auth-dir: a POINTER into the
// host config, never the key itself. The key stays in exactly one place
// (CLIProxyAPI's own config.yaml) and is read fresh on every quota poll, so
// rotating it there needs no action here and no second copy of the secret
// ever lands on disk.
type credentialFile struct {
	Type           string `json:"type"`
	SourceProvider string `json:"source_provider"`
	SourceKeyIndex int    `json:"source_key_index"`
	Label          string `json:"label,omitempty"`
}

const (
	// discoveryFirstRunDelay lets the host finish wiring its auth manager
	// before the first host.auth.save; a save issued mid-startup would be
	// refused with "core auth manager unavailable".
	discoveryFirstRunDelay = 5 * time.Second
	// discoveryInterval re-reads the host config so a key added or removed
	// through the panel shows up without restarting the proxy.
	discoveryInterval = 2 * time.Minute
)

// discovery owns the background sync between the host config's GLM keys and
// this plugin's auth-dir credentials.
type discovery struct {
	mu sync.Mutex
	// configPath is the operator-supplied cpa-config-path.
	configPath string
	// written remembers the exact JSON last saved per file name, so a tick
	// that finds nothing changed issues no host.auth.save at all -- rewriting
	// identical files every couple of minutes would churn the auth-dir and
	// wake the host's file watcher for nothing.
	written map[string]string
	started bool
}

var discoveryState = &discovery{written: map[string]string{}}

// configure records the config path and starts the background loop once.
func (d *discovery) configure(configPath string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.configPath != configPath {
		// A changed path invalidates what we believe is on disk.
		d.written = map[string]string{}
	}
	d.configPath = configPath
	if d.started {
		return
	}
	d.started = true
	go d.loop()
}

func (d *discovery) path() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.configPath
}

func (d *discovery) loop() {
	time.Sleep(discoveryFirstRunDelay)
	for {
		if err := d.syncOnce(); err != nil {
			hostLog("warn", "glm-quota: credential discovery failed", map[string]any{"error": err.Error()})
		}
		time.Sleep(discoveryInterval)
	}
}

// syncOnce reads the host config and makes sure every GLM key it finds has a
// matching credential. It never deletes: the host exposes no delete callback,
// so a key removed from the config leaves its credential behind for the
// operator to remove from the panel. That residue is deliberate and called
// out in the README rather than papered over.
func (d *discovery) syncOnce() error {
	configPath := d.path()
	if configPath == "" {
		return fmt.Errorf("cpa-config-path is not set, so no GLM keys can be discovered")
	}
	cfg, errRead := readCPAConfig(configPath)
	if errRead != nil {
		return errRead
	}
	sources := discoverGLMSources(cfg)
	if len(sources) == 0 {
		hostLog("debug", "glm-quota: no GLM keys in the host config", map[string]any{"config_path": configPath})
		return nil
	}
	for _, source := range sources {
		if err := d.ensureCredential(source); err != nil {
			hostLog("warn", "glm-quota: could not register a credential", map[string]any{
				"provider": source.Provider,
				"error":    err.Error(),
			})
		}
	}
	return nil
}

func (d *discovery) ensureCredential(source glmSource) error {
	label := source.Provider
	if source.KeyIndex > 0 {
		label = fmt.Sprintf("%s #%d", source.Provider, source.KeyIndex+1)
	}
	content, errMarshal := json.Marshal(credentialFile{
		Type:           pluginIdentifier,
		SourceProvider: source.Provider,
		SourceKeyIndex: source.KeyIndex,
		Label:          label,
	})
	if errMarshal != nil {
		return fmt.Errorf("marshal credential: %w", errMarshal)
	}
	fileName := credentialFileName(source)

	d.mu.Lock()
	unchanged := d.written[fileName] == string(content)
	d.mu.Unlock()
	if unchanged {
		return nil
	}

	saveReq, errMarshal := json.Marshal(hostAuthSaveRequest{Name: fileName, JSON: content})
	if errMarshal != nil {
		return fmt.Errorf("marshal host.auth.save request: %w", errMarshal)
	}
	if _, errSave := callHost(methodHostAuthSave, saveReq); errSave != nil {
		return errSave
	}

	d.mu.Lock()
	d.written[fileName] = string(content)
	d.mu.Unlock()
	hostLog("info", "glm-quota: registered a quota credential", map[string]any{
		"file":     fileName,
		"provider": source.Provider,
	})
	return nil
}

// hostLog forwards a line to the proxy's own logger, which is where the
// panel's log page reads from. Failing to log must never escalate, so the
// error is dropped on purpose -- there is nowhere left to report it.
func hostLog(level, message string, fields map[string]any) {
	payload, err := json.Marshal(map[string]any{
		"level":   level,
		"message": message,
		"fields":  fields,
	})
	if err != nil {
		return
	}
	_, _ = callHost(methodHostLog, payload)
}
