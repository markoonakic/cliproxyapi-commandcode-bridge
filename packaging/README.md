# Packaging handoff

Ready-to-apply artifacts for the Sarmica switchover. **Nothing here has been applied.**
No change has been made to `nixos-machines` or to Sarmica.

## Artifacts

| File | Purpose |
| --- | --- |
| `01-package.diff` | Replaces the community plugin derivation with ours |
| `02-seed-config.diff` | Fixes the `plugins.enabled` seed drift |
| `commandcode-bridge-package.nix` | The full target file, for reference |

Both diffs apply cleanly to the current `nixos-machines` worktree.

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

- `version = "1.1.0"`
- `rev = "debef6b2407a348e3d4f87cd3d5625e4ce139c69"` (tag `v1.1.0`)
- `hash = "sha256-7JVRZ5HbKQYUmlTOCDiyDEO4cxhzg2r4jxJm6SzL1HA="` — verified against a real
  archive download with substitution disabled, not a local cache hit
- `vendorHash = "sha256-vSLDY8mpkqVv5NNF9MA1EsBeZtbyAz/WTKHT4g9aRgY="` — unchanged, because
  the new package adds no third-party dependency. Recompute only if the SDK version in
  `go.mod` changes.

## The repository is public

`fetchFromGitHub` on a private repository fails without `access-tokens` configured, and
this deployment has none. Every other `fetchFromGitHub` in `nixos-machines` points at a
public repository, and the one private source (`pi-zza`) is consumed from a local clone
instead.

The plugin repository is therefore public, so the token-free fetch path works with the
existing convention. It contains no secrets: the credential scan is clean.

## Verified

The patched derivation was built with `--option substitute false`, so nothing came from a
local cache, and produced a `.so` exporting all four ABI symbols and declaring all six
capabilities, confirmed by loading it the same way the host does.
