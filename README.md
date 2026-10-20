# glm-quota

Native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin
that surfaces Z.ai GLM Coding Plan quota — the same numbers as
https://z.ai/manage-apikey/coding-plan/personal/usage — as a quota card in
CLIProxyAPI's built-in Management Center (the `/quota` tab).

It is deliberately small: three capabilities, no scheduler, no executor, no
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
- **ManagementAPI** — registers one page, "GLM Coding Plan", under the
  Management Center's Plugins nav: a plain form that writes the same
  auth-dir file the manual install step below describes, via the host's
  `host.auth.save` callback. This is the GUI alternative to step 3 below —
  see "Adding the credential from the GUI".

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
3. Add the credential — either from the GUI or by hand:

   **From the GUI:** open the Management Center, find "GLM Coding Plan"
   under the Plugins nav, paste the key, submit. Done — skip to step 5.

   **By hand:** drop a credential file into the proxy's `oauth.auth-dir`
   (`~/.config/mop/llm-proxy/auths/` for mop), e.g. `glm-quota.json`:

   ```json
   {
     "type": "glm-quota",
     "api_key": "<your GLM Coding Plan key>",
     "label": "GLM Coding Plan"
   }
   ```

   Either way, use the same key already configured for GLM inference.
   `label` is optional (defaults to "GLM Coding Plan").
4. If you used the file-drop path: the file watcher picks it up without a
   restart (per `internal/watcher` in the host); if the card doesn't appear,
   restart the proxy once. The GUI path takes effect immediately.
5. Open the Management Center's Quota tab. A `glm-quota` card should appear
   with 5-hour and weekly (or whatever windows z.ai currently reports)
   remaining-fraction bars.

## Adding the credential from the GUI

The "GLM Coding Plan" page is a `ResourceRoute` (`/v0/resource/plugins/glm-quota/add`),
which CLIProxyAPI documents as **not** management-authenticated — the host
serves it without checking the management Bearer key, same as any other
plugin resource page (e.g. the quota-monitor dashboards in this ecosystem).
In the mop deployment this is fine in practice: the only route to that page
at all is through `mop-web`'s own admin-role gate in front of the otherwise
loopback-only management port, so reaching the form already implies the
viewer is an authenticated admin. The one thing this plugin does better than
the reference example it's based on (`examples/plugin/host-callback-auth-files`
in the main CLIProxyAPI repo, which also writes a credential from an
unauthenticated resource page): the API key is submitted as a POST body,
never as a URL query string, so it never ends up in an access log or browser
history.

If you deploy this plugin somewhere the management port is reachable by
untrusted parties without an equivalent edge gate, don't rely on this page —
use the file-drop path instead, over a channel you already trust (SSH).

## What it does not do

- Does not support quota reset (`supports_reset: false` — z.ai's usage
  endpoint offers no such action).
- Does not support an OAuth-style login flow. Declaring `AuthProvider` at
  all makes this plugin appear as a login tile on the Management Center's
  `#/ai-providers` page (the host sets `supports_oauth` just because
  `AuthProvider != nil`, with no check that `StartLogin` does anything
  real) — clicking that tile will fail with a "method not supported" error.
  The working path is the "GLM Coding Plan" page under Plugins nav, or the
  manual file drop; `#/ai-providers` is not it, by design of the host, not
  a bug in this plugin.
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
