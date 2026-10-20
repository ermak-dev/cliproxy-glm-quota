package main

import "testing"

func TestConfigPathFromArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"short flag with value", []string{"/usr/local/bin/cli-proxy-api", "-config", "/srv/config.yaml"}, "/srv/config.yaml"},
		{"long flag with value", []string{"cli-proxy-api", "--config", "/srv/config.yaml"}, "/srv/config.yaml"},
		{"short flag with equals", []string{"cli-proxy-api", "-config=/srv/config.yaml"}, "/srv/config.yaml"},
		{"long flag with equals", []string{"cli-proxy-api", "--config=/srv/config.yaml"}, "/srv/config.yaml"},
		{"no config flag", []string{"cli-proxy-api", "-tui"}, ""},
		{"flag without a value", []string{"cli-proxy-api", "-config"}, ""},
		{"nothing after the terminator counts", []string{"cli-proxy-api", "--", "-config", "/ignored.yaml"}, ""},
		{"empty argv", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := configPathFromArgs(tc.args); got != tc.want {
				t.Errorf("configPathFromArgs(%q) = %q; want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestHostConfigPathPrefersTheProxyFlag(t *testing.T) {
	// The proxy under test runs with whatever argv this test binary has,
	// which carries no -config, so this covers the fallback ordering rather
	// than the flag branch (that one is covered above, on the pure parser).
	t.Setenv("WRITABLE_PATH", "/srv/writable")
	if got, want := hostConfigPath(), "/srv/writable/config.yaml"; got != want {
		t.Errorf("hostConfigPath() = %q; want %q from WRITABLE_PATH", got, want)
	}
}

func TestDiscoveryPathPrefersTheOperatorSetting(t *testing.T) {
	t.Setenv("WRITABLE_PATH", "/srv/writable")

	d := &discovery{written: map[string]string{}}
	d.configure("/operator/chosen.yaml")
	if got := d.path(); got != "/operator/chosen.yaml" {
		t.Errorf("path() = %q; want the operator's explicit setting", got)
	}

	// Clearing the setting must fall back to detection, not stay pinned to
	// whatever was configured before.
	d.configure("")
	if got, want := d.path(), "/srv/writable/config.yaml"; got != want {
		t.Errorf("path() after clearing = %q; want detected %q", got, want)
	}
}
