# Handoff: switchover procedure

Status: plugin source complete (all planned phases, including the scheduler). Not yet deployed.

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
| `Scheduler` | Implemented; quota-aware multi-account routing |
| Nix derivation | Builds; the resulting `.so` loads and registers |

All six capabilities are implemented, so parity with the native channels is complete at
the capability level.

The only thing not done is the live smoke test: the plugin has never made a real upstream
call. That is what step 5 of the switchover proves.

### Scheduler behaviour

The host consults a plugin scheduler for **every** provider, so ours decides only for
Command Code candidates and declines everything else, leaving antigravity and codex on the
host's built-in selection.

Selection matches the host's built-in semantics: highest ready priority band, then
round-robin within it, using a successor walk over ID-sorted candidates. On top of that it
skips an account whose cached quota window is exceeded and has not reset — a signal the
host cannot know before upstream returns a rate-limit error. The mark expires in memory
when the reset passes; there is no polling and no background timer.

## Blockers before switchover

1. **The `nixos-machines` tree is dirty with pre-existing work, and the entire
   CLIProxyAPI deployment is among it.** `git status` shows the deployment files as
   intent-to-add (` A`) — `hosts/sarmica/cliproxyapi.nix`, `hosts/sarmica/cliproxyapi/*`,
   and `packages/commandcode-bridge/default.nix` are **not in HEAD**. They exist only as
   uncommitted work. The switchover must therefore be committed on top of, or together
   with, that change, and must not capture the unrelated work in the same commit.

2. **Sarmica activation is a separately approved operation.** Deployment is not a
   side effect of a code change. Gate on `dry-activate`.

## Switchover sequence

Keep the provider id and the `.so` filename `commandcode-bridge`. That keeps
`compose.yaml`, `check-compose.py`, and the existing credential file unchanged —
the guard asserts the exact mount path and volume count.

1. Apply `packaging/01-package.diff` and `packaging/02-seed-config.diff` from the
   plugin repository root, on a clean `nixos-machines` tree.
2. Back up `/var/lib/cliproxyapi/auth` on Sarmica. Preserve
   `commandcode-bridge-57fca5a1eddd.json` (plan goat, priority 6).
3. `nixos-rebuild dry-activate --flake .#sarmica` and **confirm only
   `cliproxyapi-compose.service` restarts**. Pangolin, Forgejo and Docker must not.
4. Do **not** touch `cliproxyapi-secrets.sops.yaml` or
   `cliproxyapi-access.sops.yaml`. The documented incident came from SOPS-driven
   restarts, not from the Compose unit.
5. Activate, then run the read-only smoke checks below.

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
