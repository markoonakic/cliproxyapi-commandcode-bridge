# Packaging handoff

Artifacts in this directory are prepared but **not applied**. Nothing here has been
written into `nixos-machines`.

| File | Purpose |
| --- | --- |
| `commandcode-bridge-package.nix` | Drop-in replacement for the community plugin derivation |
| `seed-config.patch.md` | The `plugins.enabled` drift fix |

## Apply order

1. Push this repository (see the root `docs/handoff.md`).
2. Copy `commandcode-bridge-package.nix` over
   `nixos-machines/packages/commandcode-bridge/default.nix` and set `rev`.
   `pname` stays `commandcode-bridge`, so the installed path remains
   `$out/lib/commandcode-bridge.so` and both `compose.yaml` and
   `check-compose.py` need no change.
3. Apply `seed-config.patch.md`.
4. `nixos-rebuild dry-activate --flake .#sarmica` and confirm ONLY
   `cliproxyapi-compose.service` is scheduled to restart.
5. Activate, then run the smoke checks in `docs/handoff.md`.

## Notes

- `hash` is content-addressed by the source tree. The value in the package file was
  computed from the current revision and is expected to work for `fetchFromGitHub`.
  If Nix reports a mismatch it prints the correct hash; substitute it.
- `vendorHash` changes only when the SDK version in `go.mod` changes.
