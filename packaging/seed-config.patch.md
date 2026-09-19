# Seed config patch for `hosts/sarmica/cliproxyapi/config.yaml`

## Why

The Nix seed says `plugins.enabled: false`, but the live UI-owned config at
`/var/lib/cliproxyapi/config/config.yaml` has plugins enabled. `seedConfig` never
overwrites an existing file, so the two have diverged.

Consequence: a fresh rebuild or disaster recovery would seed plugins **disabled** and
silently load no plugin at all. Fix the seed so desired state matches live.

## Change

In `hosts/sarmica/cliproxyapi/config.yaml`, replace:

```yaml
plugins:
  enabled: false
```

with:

```yaml
plugins:
  enabled: true
  dir: /etc/cliproxyapi/plugins
  configs:
    commandcode-bridge:
      enabled: true
```

## Safety

- This file is a **config template** input, not a SOPS secret file. It does not touch
  the SOPS restart behaviour that caused the documented activation incident.
- It is already listed in `systemd.services.cliproxyapi-compose.restartTriggers`, so
  it restarts only the Compose unit.
- Verify with `nixos-rebuild dry-activate --flake .#sarmica` and confirm that ONLY
  `cliproxyapi-compose.service` is listed. Pangolin, Forgejo and Docker must not restart.
