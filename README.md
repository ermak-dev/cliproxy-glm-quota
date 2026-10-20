# glm-quota

Native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin
that surfaces Z.ai GLM Coding Plan quota — the same numbers as
https://z.ai/manage-apikey/coding-plan/personal/usage — as a quota card in
CLIProxyAPI's built-in Management Center (the `/quota` tab).

It is deliberately small: two capabilities, no scheduler, no executor, no UI.
It does not route any inference traffic and does not touch any existing
credential.

- **AuthProvider** — parses the pointer files this plugin maintains in
  auth-dir (see below); holds no key.
- **QuotaProvider** — answers the host's `quota.fetch` by reading the GLM
  key out of CLIProxyAPI's own config and calling
  `https://api.z.ai/api/monitor/usage/quota/limit` directly (fixed, not
  configurable — a plugin whose upstream URL is configurable is an open
  SSRF relay).

Everything else is automatic. There is no form to fill in, no key to paste,
and no second place secrets live.

## Where the keys come from

From CLIProxyAPI's own `config.yaml` — the `openai-compatibility:` entries
whose `base-url` names a GLM host (`api.z.ai`, `open.bigmodel.cn`). The
plugin reads them, never copies them, and re-reads on every quota poll, so
rotating a key in the config needs no action here and no second copy of the
secret ever lands on disk.

Point it at the config with the plugin's one setting, `cpa-config-path`
(rendered as an input in the panel's "Configure glm-quota" dialog):

```yaml
plugins:
  configs:
    glm-quota:
      enabled: true
      cpa-config-path: /home/ermak/.config/mop/llm-proxy/config.yaml
```

### Why a credential file per key, and why it holds no key

CLIProxyAPI attaches a quota card to a *credential*, by matching the
credential's `provider` key against this plugin's declared
`supported_providers`. An `openai-compatibility` entry is config, not a
credential — it never appears in `/v0/management/auth-files` (confirmed on a
live host) — so there is nothing to attach a card to until a credential
exists. Plugins cannot create runtime-only credentials either; only
`host.auth.save`, which writes a physical file.

So the plugin writes one small file per key into `auth-dir`, holding a
*pointer* at the config — never the key:

```json
{"type":"glm-quota","source_provider":"Z.AI","source_key_index":0,"label":"Z.AI"}
```

These files appear, and stay in sync, on their own: a background loop re-reads
the config every couple of minutes and registers a credential for any GLM key
that does not have one yet. You never create or edit them by hand.

The residue, stated plainly: **removing** a key from the config does not
remove its credential — the host exposes no delete callback to plugins. Delete
the stale `glm-quota-*.json` from the panel or the auth-dir; it is one click
and it is visible, rather than silently rotting. The loop also rewrites a
file only when its content actually changed, so a steady-state tick issues no
writes and does not churn the host's file watcher.

## Multiple keys

Free: every key under every GLM `openai-compatibility` entry gets its own
credential (`glm-quota-<provider>.json`, `glm-quota-<provider>-2.json`, …)
and therefore its own quota card. A disabled entry, or an entry with an empty
key, is skipped — the operator already said it should not be used, and a card
that always errors is worse than none.

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
3. Point the plugin at CLIProxyAPI's own config, in `plugins.configs.glm-quota`
   of that same `config.yaml`:

   ```yaml
   cpa-config-path: /home/ermak/.config/mop/llm-proxy/config.yaml
   ```

   (The panel's "Configure glm-quota" dialog renders this as an input.)
4. Nothing else. Within a few seconds of startup the plugin reads the config,
   finds every GLM key, and registers one credential per key; a
   reconfiguration through the panel picks up a changed path immediately.
5. Open the Management Center's Quota tab. A `glm-quota` card should appear
   with 5-hour and weekly remaining-fraction bars (confirmed against a live
   account; see the note on `windowLabel` in `zai_quota.go` for how those two
   are actually told apart — z.ai's own `type` field doesn't do it).

## What it does not do

- Does not support quota reset (`supports_reset: false` — z.ai's usage
  endpoint offers no such action).
- Does not support an OAuth-style login flow. Declaring `AuthProvider` at
  all makes this plugin appear as a login tile on the Management Center's
  `#/ai-providers` page (the host sets `supports_oauth` just because
  `AuthProvider != nil`, with no check that `StartLogin` does anything
  real) — clicking that tile will fail with a "method not supported" error.
  Ignore that tile; it is the host's blanket behaviour for any plugin that
  declares `AuthProvider`, not something this plugin can opt out of.
- Does not modify the config-level `openai-compatibility` entries that route
  GLM inference traffic. It only reads them.
- Does not remove a credential whose key disappeared from the config — the
  host exposes no delete callback to plugins. See the residue note above.

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
