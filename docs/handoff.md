# Handoff: switchover procedure

Status: plugin source complete (phases 1–3, plus packaging). Not yet deployed.

## What is done

| Item | State |
| --- | --- |
| Plugin source | Complete, committed, builds |
| Unit tests | Pass, offline, no credentials |
| C ABI surface | Verified through `dlopen` like the real host |
| `QuotaProvider` | Implemented, fail-closed, mapped to SDK v7.3.6 types |
| `AuthProvider` | Implemented; enrollment boundary documented |
| `ModelProvider` | Implemented; fixes both community-plugin defects |
| `ManagementAPI` | Implemented; dashboard + 2 routes |
| `Executor` | Implemented; serves inference via `/alpha/generate` |
| Nix derivation | Builds; the resulting `.so` loads and registers |

Not done, by design: `Scheduler`, and the live smoke test. The plugin declares five
capabilities and now serves inference, so it can replace the community plugin.

`Scheduler` is only needed for multi-account routing. With one enrolled account the
host's own round-robin scheduler is sufficient.

## Blockers before switchover

1. **The plugin repository has no remote.** `packages/commandcode-bridge/default.nix`
   in nixos-machines expects `fetchFromGitHub`. The source must be pushed first, then
   the `rev` and `hash` pinned. GitHub SSH authentication works from this machine.

2. **The nixos-machines tree has uncommitted pre-existing work.** Per that repo's
   AGENTS.md, full work should start from a clean tree. The switchover commit must not
   capture unrelated changes.

3. **`plugins.enabled` drift.** The Nix seed at
   `hosts/sarmica/cliproxyapi/config.yaml` says `enabled: false`, but the live
   UI-owned config has plugins enabled. A fresh rebuild would seed plugins disabled
   and silently load nothing. The seed must be fixed as part of this work.

4. **Sarmica activation is a separately approved operation.** Deployment is not a
   side effect of a code change.

## Switchover sequence

Keep the provider id and the `.so` filename `commandcode-bridge`. That keeps
`compose.yaml`, `check-compose.py`, and the existing credential file unchanged —
the guard asserts the exact mount path and volume count.

1. Push the plugin repository; pin `rev` and `hash` in the package derivation.
2. Back up `/var/lib/cliproxyapi/auth` on Sarmica. Preserve
   `commandcode-bridge-57fca5a1eddd.json` (plan goat, priority 6).
3. Update the Nix seed config to enable plugins and declare the plugin.
4. `nixos-rebuild dry-activate --flake .#sarmica` and **confirm only
   `cliproxyapi-compose.service` restarts**. Pangolin, Forgejo and Docker must not.
5. Do **not** touch `cliproxyapi-secrets.sops.yaml` or
   `cliproxyapi-access.sops.yaml`. The documented incident came from SOPS-driven
   restarts, not from the Compose unit.
6. Activate, then run the read-only smoke checks below.

## Smoke checks

```sh
KEY=$(cat ~/.config/cliproxyapi/management-key.txt)
# The plugin must be listed with supports_quota true.
curl -s -H "Authorization: Bearer $KEY" https://cliproxy.sarma.love/v0/management/plugins
# This must stop returning {"providers":[]}.
curl -s -H "Authorization: Bearer $KEY" https://cliproxy.sarma.love/v0/management/quota/providers
# This must return plan, groups and metrics instead of a 501.
curl -s -X POST -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"auth_index":"a317e78d6ea3c905"}' https://cliproxy.sarma.love/v0/management/quota/fetch
# The dashboard must render.
curl -sI https://cliproxy.sarma.love/v0/resource/plugins/commandcode-bridge/accounts
```

Expected quota for the live account: `individual-goat`, 5h about 88% remaining, weekly
about 78%, and a `credits_remaining` metric.

## Rollback

Revert the package derivation to the pinned community revision `fcf8b30` and
`nixos-rebuild switch` again. The credential directory is persistent host storage and
is never removed by a plugin change.

## Known limitation

Quota was verified live only on the `goat` plan. Other tiers may return a different
`windowLimits` shape; an absent `cap` fails closed and reports "unavailable" rather
than a wrong percentage.

## Verified baseline (2026-09-19, pre-switchover)

Recorded so the smoke checks have a before/after reference:

| Check | Current value | Expected after |
| --- | --- | --- |
| `/v0/management/plugins` → `supports_quota` | `false` | `true` |
| `/v0/management/quota/providers` | `{"providers":[]}` | `{"providers":["commandcode-bridge"]}` |
| `POST /v0/management/quota/fetch` | `501 no quota provider available for credential` | `200` with plan, groups and metrics |
| `/v0/resource/plugins/commandcode-bridge/accounts` | `200` (community page) | `200` (our page) |

## Ready-made packaging

`packaging/` holds the prepared, unapplied deployment artifacts:

- `commandcode-bridge-package.nix` — drop-in replacement derivation. Its exact shape was
  built and verified locally against a local source tree.
- `seed-config.patch.md` — the `plugins.enabled` drift fix.
- `README.md` — apply order.

The `hash` in the package file is content-addressed by the source tree; the value was
derived from the tree and is expected to work for `fetchFromGitHub` after pushing. Nix
prints the correct value if it differs.
