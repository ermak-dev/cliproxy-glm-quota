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

## Why a separate credential, and why the key can't be read automatically

CLIProxyAPI's quota tab attaches a card to a credential by matching the
credential's `provider` key against this plugin's declared
`supported_providers`. The existing GLM inference credential's provider key
depends on how it was added in the panel and isn't something this plugin can
discover or assume. Rather than guess it, this plugin owns its own provider
key (`glm-quota`) and its own tiny credential, carrying nothing but the same
API key, used only for the quota probe. The real inference credential is
untouched.

This was checked, not assumed: `host.auth.list` *can* enumerate every
existing credential (including config-level `openai-compatibility` /
`claude-api-key` entries) — so discovering *that* a GLM credential exists,
and under what provider name, is possible. But `host.auth.get`, the call
that would actually return a credential's stored key, only works for
auth-dir *file-backed* credentials (`internal/pluginhost/auth_callbacks.go`'s
`authPhysicalJSONByIndex` reads the key straight off disk by path); a
config-level entry has no such file and the call fails with "auth file path
not found". The mop deployment's Z.ai GLM credential specifically lives in
`config.yaml`'s `openai-compatibility:` list (`deploy/roles/llmproxy`'s own
comment: "панель заводит Z.AI как openai-compatibility"), not as an
auth-dir file — so there is no supported way for this plugin to read that
key automatically. If it were ever moved to an auth-dir JSON file instead,
reading it directly would become possible and this plugin's own credential
would become redundant; until then, the key has to be told to this plugin
once, separately.

## Multiple keys

Supported — each key is its own auth-dir file (`glm-quota-<name>.json`), and
each gets its own quota card, exactly like multiple `keys:` entries under one
`openai-compatibility` block get multiple rows elsewhere in the panel. The
GUI form (below) asks for a short, unique **name** per key and uses it to
build that file's name; submitting the same name again replaces that key
(e.g. after rotating it) rather than adding a duplicate. From the CLI, just
drop more than one `glm-quota-*.json` file with distinct names.

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
3. Add one credential per key — either from the GUI or by hand:

   **From the GUI:** open the Management Center, find "GLM Coding Plan"
   under the Plugins nav, give the key a short unique name, paste the key,
   submit. Repeat with a different name for each additional key. Skip to
   step 5.

   **By hand:** drop a credential file into the proxy's `oauth.auth-dir`
   (`~/.config/mop/llm-proxy/auths/` for mop), named `glm-quota-<name>.json`
   (the `<name>` part just needs to be unique per key — it becomes the
   credential's ID):

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
   with 5-hour and weekly remaining-fraction bars (confirmed against a live
   account; see the note on `windowLabel` in `zai_quota.go` for how those two
   are actually told apart — z.ai's own `type` field doesn't do it).

## Adding the credential from the GUI

The "GLM Coding Plan" page itself is a `ResourceRoute`
(`/v0/resource/plugins/glm-quota/add`) — unauthenticated, confirmed against a
running v8.0.16 host, and GET-only (a bare POST 404s before the plugin is
even called; this is not documented, just how the host routes it). That's
fine for *displaying* the form: no secret is needed just to look at it.

The actual save is a separate, authenticated `ManagementRoute`
(`POST /v0/management/plugins/glm-quota/add`), reached by the page's own
inline script, not a plain `<form method=POST>` (a browser form post can't
attach a custom header; `fetch()` can). The form therefore asks for two
things: the **management key** (the same one used to log into this panel —
pasted in, never auto-filled, since a resource page is not allowed to carry
it) and the GLM API key. Submitting sends the GLM key as a `fetch()` POST
body with `Authorization: Bearer <management key>` — never as a URL query
string, so neither key ends up in an access log or browser history. This
improves on the one official plugin example that does something similar
(`examples/plugin/host-callback-auth-files` in the main CLIProxyAPI repo),
which saves via an unauthenticated resource GET with the secret inline in
the query string.

One subtlety specific to `ManagementRoute`, worth knowing if you ever add
another one: unlike `ResourceRoute` (which the host auto-namespaces under
`/v0/resource/plugins/<id>/`), a `ManagementRoute`'s path gets **no**
automatic per-plugin prefix — `internal/pluginhost/management.go`'s
`normalizeManagementRoute` just prepends the bare `/v0/management`. This
plugin supplies its own `/plugins/glm-quota/add` prefix explicitly (the
convention the host itself expects, per the same file's
`legacyPluginRoutePrefix`); a bare `/add` would 404 (confirmed — that was
this plugin's first, broken attempt) or, worse, silently collide with
another plugin's route of the same name.

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
mapping without a live network call, including a captured, anonymized live
response (`TestToQuotaFetchResponse/live_production_shape`). `main_test.go`
exercises the RPC dispatch (`handleMethod`) directly, without the cgo
boundary.

An earlier version of this plugin used the wrong field as the credit-total
denominator (`number`, which is actually the window's period — "5 hours",
"1 week" — not its allowance) and labeled windows from `type`, which z.ai
sends as the literal string `"CREDIT_LIMIT"` on every entry regardless of
period. Both bugs happened to look correct on the two specific values
observed during development (fully-fresh and fully-spent), which is why
they survived a non-live test suite; they were only caught by checking a
real deployed response. `usage` is the real allowance; `unit`+`number`
is the only signal that actually distinguishes windows. See the comments on
`zaiQuotaWireLimit` and `windowLabel` in `zai_quota.go`.

## Logo

`assets/logo.png` is embedded via `go:embed` (see `logo.go`) and sent as a
`data:` URI in `Metadata.Logo` — there's no public host for this repo yet to
link to instead.
