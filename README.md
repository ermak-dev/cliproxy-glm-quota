# glm-quota

Native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin
that surfaces Z.ai GLM Coding Plan quota — the same numbers as
https://z.ai/manage-apikey/coding-plan/personal/usage — as a quota card in
CLIProxyAPI's built-in Management Center (the `/quota` tab).

It is deliberately small: two capabilities, no scheduler, no executor, no
dependency on how the GLM inference credential itself is configured
(`claude-api-key` vs `openai-compatibility`, whatever name the operator gave
it in the panel). It does not route any inference traffic and does not touch
any existing credential.

- **AuthProvider** — parses a tiny auth-dir JSON file holding a GLM Coding
  Plan API key, registered as a credential of type `glm-quota`.
- **QuotaProvider** — answers the host's `quota.fetch` for that credential by
  calling `https://api.z.ai/api/monitor/usage/quota/limit` directly (fixed,
  not configurable — a plugin that lets its upstream URL be configured is an
  open SSRF relay).

## Why a separate credential

CLIProxyAPI's quota tab attaches a card to a credential by matching the
credential's `provider` key against this plugin's declared
`supported_providers`. The existing GLM inference credential's provider key
depends on how it was added in the panel and isn't something this plugin can
discover or assume. Rather than guess it, this plugin owns its own provider
key (`glm-quota`) and its own tiny credential, carrying nothing but the same
API key, used only for the quota probe. The real inference credential is
untouched.

## Install

### Build

```sh
make build
```

Produces `dist/linux/amd64/glm-quota.so`. Requires `CGO_ENABLED=1` and a C
toolchain (the plugin ABI is a native shared library, not pure Go).

Pinned against CLIProxyAPI `v8.0.16`, plugin schema version 6, ABI version 1
(see `abi_types.go`). A host upgrade that changes this wire contract needs a
matching update there — there's no SDK dependency to bump instead, these
types are inlined on purpose; see the comment at the top of that file.

### Deploy

1. Copy `glm-quota.so` to `<plugins.dir>/linux/amd64/glm-quota.so` on the
   proxy host (for the mop deployment: `~/.config/mop/llm-proxy/plugins/linux/amd64/`).
2. Restart the proxy (`systemctl restart mop-llm-proxy` for the mop
   deployment) so it loads the new plugin.
3. Drop a credential file into the proxy's `oauth.auth-dir`
   (`~/.config/mop/llm-proxy/auths/` for mop), e.g. `glm-quota.json`:

   ```json
   {
     "type": "glm-quota",
     "api_key": "<your GLM Coding Plan key>",
     "label": "GLM Coding Plan"
   }
   ```

   Use the same key already configured for GLM inference. `label` is
   optional (defaults to "GLM Coding Plan").
4. Reload — the file watcher picks it up without a restart (per
   `internal/watcher` in the host); if the card doesn't appear, restart the
   proxy once.
5. Open the Management Center's Quota tab. A `glm-quota` card should appear
   with 5-hour and weekly (or whatever windows z.ai currently reports)
   remaining-fraction bars.

## What it does not do

- Does not support quota reset (`supports_reset: false` — z.ai's usage
  endpoint offers no such action).
- Does not support an interactive "add via panel" login flow — the
  credential is a plain file drop, like `ZCode`/`zai-coding-plan`-family
  plugins in the same ecosystem.
- Does not read or modify the config-level `openai-compatibility` /
  `claude-api-key` entries that actually route GLM inference traffic.

## Testing

```sh
make test   # go test ./...  -- pure-Go, no cgo build required
make vet
make fmt    # gofmt -l .; fix anything it lists
```

`zai_quota_test.go` covers the z.ai response parsing and the bucket/fraction
mapping without a live network call. `main_test.go` exercises the RPC
dispatch (`handleMethod`) directly, without the cgo boundary.
