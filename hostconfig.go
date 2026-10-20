package main

import (
	"os"
	"path/filepath"
	"strings"
)

// hostConfigPath finds CLIProxyAPI's own config.yaml without being told.
//
// This plugin is a shared library loaded into the proxy's process, so the
// proxy's argv is visible to it in /proc/self/cmdline. That is the same
// information the host itself uses for plugin bootstrap
// (cmd/server/main.go's pluginBootstrapConfigPath), and it is authoritative:
// an explicit -config wins, and only when the host was started without one
// does this fall back to the default it would have used.
//
// The fallbacks mirror the host's own order rather than inventing a new one:
//  1. an explicit -config / --config flag (both the " -config x" and
//     "-config=x" spellings);
//  2. the WRITABLE_PATH directory, where the host looks for its config when
//     started headless (util.WritablePath);
//  3. the process working directory's config.yaml.
//
// An operator-set cpa-config-path overrides all of this; see configure().
func hostConfigPath() string {
	if path := configPathFromArgs(readProcessArgs()); path != "" {
		return path
	}
	if dir := strings.TrimSpace(os.Getenv("WRITABLE_PATH")); dir != "" {
		return filepath.Join(dir, "config.yaml")
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, "config.yaml")
	}
	return "config.yaml"
}

// readProcessArgs returns the current argv. Reading /proc/self/cmdline keeps
// this package testable and avoids depending on os.Args, which a shared
// library cannot rely on being populated the way the host's main saw it.
func readProcessArgs() []string {
	raw, err := os.ReadFile("/proc/self/cmdline")
	if err != nil {
		return nil
	}
	parts := strings.Split(string(raw), "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// configPathFromArgs extracts the -config/--config value, matching the
// spellings the host's own parser accepts.
func configPathFromArgs(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return ""
		case arg == "-config" || arg == "--config":
			if i+1 < len(args) {
				return strings.TrimSpace(args[i+1])
			}
			return ""
		case strings.HasPrefix(arg, "-config="):
			return strings.TrimSpace(strings.TrimPrefix(arg, "-config="))
		case strings.HasPrefix(arg, "--config="):
			return strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
		}
	}
	return ""
}
