# Handoff: switchover procedure

Status: plugin source complete (Phases 1–2, plus packaging). Not yet deployed.

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
| Nix derivation | Builds; the resulting `.so` loads and registers |

Not done, by design: `Executor`, `Scheduler`, and the live smoke test. The plugin
currently declares four capabilities; it does **not** yet serve inference. That means
**replacing the community plugin today would break Command Code inference.**

## Blockers before switchover

1. **The plugin cannot serve inference yet.** It declares `auth_provider`,
   `model_provider`, `quota_provider`, and `management_api`. It does not declare
   `executor`. The community plugin currently serves all Command Code model traffic
   through `/v1/chat/completions`. Swapping the `.so` would leave requests unserved.

   Either implement `Executor` first, or run the two plugins side by side under
   different provider ids. The latter needs a deployment change, because the current
   setup mounts one specific filename at a fixed path.

2. **The plugin repository has no remote.** `packages/commandcode-bridge/default.nix`
   in nixos-machines expects `fetchFromGitHub`. The source must be pushed first, then
   the `rev` and `hash` pinned. GitHub SSH authentication works from this machine.

3. **The nixos-machines tree has uncommitted pre-existing work.** Per that repo's
   AGENTS.md, full work should start from a clean tree. The switchover commit must not
   capture unrelated changes.

4. **`plugins.enabled` drift.** The Nix seed at
   `hosts/sarmica/cliproxyapi/config.yaml` says `enabled: false`, but the live
   UI-owned config has plugins enabled. A fresh rebuild would seed plugins disabled
   and silently load nothing. The seed must be fixed as part of this work.

5. **Sarmica activation is a separately approved operation.** Deployment is not a
   side effect of a code change.

## Switchover sequence (once Executor exists)

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
