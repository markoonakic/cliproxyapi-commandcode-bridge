# cliproxyapi-commandcode-bridge

A clean-room native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin that gives
[Command Code](https://commandcode.ai) accounts parity with the built-in `antigravity` and `codex`
channels: quota and limits, credit balances, account health, and a Management Center dashboard.

It is a Go `c-shared` library loaded by the CLIProxyAPI plugin host. It is a **rewrite**, not a fork,
of the community `commandcode-bridge` plugin. The community plugin is used only as a reference for
the C ABI wiring, the credential file format, and the OpenAI wire handling.

## Status

Phase 1 of the implementation plan: the C ABI, the plugin registration, and the full
`QuotaProvider` capability with fail-closed parsing and unit tests against live-captured payloads.

## Why quota parity is possible

The host exposes a first-class `QuotaProvider` plugin capability
(`sdk/pluginapi`, first shipped in CLIProxyAPI v7.2.159). Command Code's undocumented alpha
billing routes return real window limits with **caps included**, so a correct percentage needs no
external plan catalogue:

| Purpose | Endpoint |
| --- | --- |
| Identity | `GET /alpha/whoami` |
| Plan and period | `GET /alpha/billing/subscriptions` |
| Windows and credits | `GET /alpha/billing/credits` |
| Usage and cost | `GET /alpha/usage/summary` |

All use `Authorization: Bearer <user_ key>`. The `goat` plan was verified live; other tiers are
unverified and fail closed.

## Design rules

- **Fail closed.** A window is emitted only when `cap > 0` and `used` is finite and non-negative.
  An unreadable window is reported as unavailable, never as 0%.
- **No token or credential custody.** The plugin reads the credential the host already stores and
  never writes, copies, or refreshes tokens.
- **No reset.** `ResetQuota` always reports failure, because returning success would make the host
  clear local cooldown state despite nothing being reset upstream.
- **Upstream calls go through the host.** Requests use the `host.http.do` callback, so the host's
  transport policy and request logging apply.
- **Backward compatible credentials.** The existing deployed credential file (seven keys) parses
  unchanged; identity fields are optional additions.

## Build

```sh
go build -buildmode=c-shared -trimpath \
  -ldflags="-s -w -X main.Version=1.0.0" \
  -o commandcode-bridge.so ./cmd/commandcode-bridge
```

The output is `commandcode-bridge.so` (with a generated `commandcode-bridge.h`), installed to
`$out/lib/commandcode-bridge.so` by the Nix package.

Requires Go 1.26 and `github.com/router-for-me/CLIProxyAPI/v7 v7.3.6` or newer. `QuotaProvider`
needs `>= v7.2.159`; `QuotaMetric` needs `>= v7.3.6`.

## Test

```sh
go test ./...
```

Tests run offline with no credentials. The quota mapping is asserted against a live-captured
payload, including the fail-closed cases.

## Layout

```
cmd/commandcode-bridge/   C ABI entry point and exported symbols
internal/abi/             RPC envelope, method names, schema version
internal/host/            host callback bridge and callback-id propagation
internal/plugin/          method dispatch and capability registration
internal/quota/           Command Code client and QuotaProvider implementation
```

## Disclaimer

Unofficial and community-maintained. Not affiliated with, endorsed by, or supported by Command Code
or the CLIProxyAPI project. You must use your own authorized Command Code account and credentials,
and the Command Code Terms of Service, pricing, quotas, and usage rules apply. This project does not
grant access to Command Code, credits, or support, and it does not permit restriction bypasses,
credential sharing, resale, scraping, or unauthorized automation.

## License

MIT. See `LICENSE`.
