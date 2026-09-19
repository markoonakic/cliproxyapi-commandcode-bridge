# Packaging handoff

Ready-to-apply artifacts for the Sarmica switchover. **Nothing here has been applied.**
No change has been made to `nixos-machines` or to Sarmica.

## Artifacts

| File | Purpose |
| --- | --- |
| `01-package.diff` | Replaces the community plugin derivation with ours |
| `02-seed-config.diff` | Fixes the `plugins.enabled` seed drift |
| `commandcode-bridge-package.nix` | The full target file, for reference |

Both diffs were verified to **apply cleanly** to the current `nixos-machines` worktree, and
the patched package was **built successfully** from GitHub.

## Apply

From the `nixos-machines` root, with a clean tree:

```sh
patch -p1 < /path/to/packaging/01-package.diff
patch -p1 < /path/to/packaging/02-seed-config.diff
```

## Why no Compose or check-compose change

The installed filename stays `commandcode-bridge.so`, so the bind mount and the
`check-compose.py` assertion (which pins that exact path and a volume count of 4) remain
byte-identical and need no edit.

Docker creates the parent directory when it bind-mounts the file, so
`plugins.dir: /etc/cliproxyapi/plugins` resolves to a directory containing the one plugin.

## Pinned values

- `rev = "e5736abb69a692fb88a6b884eeaefffc4983b4f7"` (tag `v1.0.0`)
- `hash = "sha256-arAF0Xk5QO+dtnhCmeoWu8TrQK8gqNK/nzQYhiSEBr4="` — verified against
  `fetchFromGitHub` on the pushed repository
- `vendorHash = "sha256-vSLDY8mpkqVv5NNF9MA1EsBeZtbyAz/WTKHT4g9aRgY="` — recompute only if
  the SDK version in `go.mod` changes

## Safety

- `02-seed-config.diff` edits a **config template**, not a SOPS secret file. It does not
  touch the SOPS restart behaviour behind the documented activation incident.
- `config.yaml` is already in `restartTriggers`, so only `cliproxyapi-compose.service`
  restarts.
- Gate on `nixos-rebuild dry-activate` and confirm ONLY the Compose unit is scheduled.
