# Side-by-side trial

Run the community plugin and ours at the same time, then A/B them. The community plugin is
untouched, so nothing regresses while we prove ours.

## How coexistence works

The host identifies a plugin by its **`.so` filename**, and a credential by its **`type`
field**. Two plugins coexist when those differ:

| | Community plugin | Ours |
| --- | --- | --- |
| Plugin id / `.so` | `commandcode-bridge` | `command-code` |
| Credential `type` | `commandcode-bridge` | `command-code` |
| Credential file | `commandcode-bridge-57fca5a1eddd.json` | `command-code-<fingerprint>.json` |
| Provider key | `commandcode-bridge` | `command-code` |

Both credentials hold the **same Command Code API key**, so it is one authorized account
reached through two plugins. Command Code quota is shared across them.

## Apply

From the `nixos-machines` root, on a clean tree:

```sh
patch -p1 < .../packaging/coexist/01-compose.diff
patch -p1 < .../packaging/coexist/02-check-compose.diff
patch -p1 < .../packaging/coexist/03-cliproxyapi-nix.diff
patch -p1 < .../packaging/coexist/04-seed-config.diff
```

Then add the package:

```sh
cp .../packaging/command-code-package.nix packages/command-code/default.nix
```

All four diffs apply cleanly, and the patched security guard passes against the patched
Compose model (`CLIProxyAPI private Compose boundary: PASS`).

`check-compose.py` was updated deliberately: it asserts the exact mount set and a volume
count, so adding a plugin mount must update both. That guard is doing its job.

## Enroll the second credential

Ours needs its own credential file. Once the plugin is loaded, either:

- **Through the dashboard** at
  `https://cliproxy.sarma.love/v0/resource/plugins/command-code/accounts`
  (`POST /validate` then save), or
- **By copying** the existing file and changing `type`:

```sh
cd /var/lib/cliproxyapi/auth
sudo cp commandcode-bridge-57fca5a1eddd.json command-code-<fingerprint>.json
# then set "type": "command-code" in the copy
```

The plugin's `Fingerprint()` is `sha256(api_key)[:12]`, and `FileName()` is
`command-code-<fingerprint>.json`. Use the dashboard to avoid computing it by hand.

`type` **must** be `command-code`. The host derives the provider key from it, and a mismatch
means the plugin never sees the credential.

## Test

Both plugins answer for their own provider key:

```sh
KEY=$(cat ~/.config/cliproxyapi/management-key.txt)

# Ours must now appear as a separate plugin.
curl -s -H "Authorization: Bearer $KEY" .../v0/management/plugins \
  | python3 -c "import json,sys;[print(p['id'],p.get('supports_quota')) for p in json.load(sys.stdin)['plugins']]"

# Ours must answer; theirs must still answer.
curl -s -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"auth_index":"<our auth_index>"}' .../v0/management/quota/fetch

# Both dashboards must load.
curl -sI .../v0/resource/plugins/command-code/accounts
curl -sI .../v0/resource/plugins/commandcode-bridge/accounts
```

Inference A/B: request the same model twice, once per provider, and compare. Inference calls
need `force-model-prefix` or a credential `prefix` to select a specific provider for an
otherwise identical model id. Otherwise the host picks by its own scheduling.

## Rollback

Remove the four patches and the package. The community plugin was never modified, so its
behaviour is unchanged throughout. Our credential file can be deleted from the auth dir;
nothing else was touched.

## After the trial

If ours is better, remove the community plugin from `compose.yaml`, `check-compose.py`,
`cliproxyapi.nix` and the seed config, and delete its credential. That returns to the
single-plugin design with our plugin owning the gateway.
