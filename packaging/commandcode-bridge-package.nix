# Drop-in replacement for nixos-machines packages/commandcode-bridge/default.nix.
#
# Apply once the plugin repository is pushed:
#   1. Replace packages/commandcode-bridge/default.nix with this file.
#   2. Set rev and hash below.
#   3. Keep the pname, so the installed path stays
#      $out/lib/commandcode-bridge.so and Compose plus check-compose.py need no change.
#
# The hash below is content-addressed by the source tree, so the same value is
# expected to work for fetchFromGitHub. Verify with:
#   nix-prefetch-url --unpack https://github.com/markoonakic/cliproxyapi-commandcode-bridge/archive/<rev>.tar.gz
# and use `nix build` to confirm; if Nix reports a mismatch it prints the correct hash.
{
  lib,
  buildGoModule,
  fetchFromGitHub,
}:
buildGoModule (finalAttrs: {
  pname = "commandcode-bridge";
  version = "1.0.0";

  # Clean-room rewrite replacing the community plugin. Pin the revision; do not
  # follow a branch.
  src = fetchFromGitHub {
    owner = "markoonakic";
    repo = "cliproxyapi-commandcode-bridge";
    rev = "REPLACE_WITH_PUSHED_COMMIT_SHA";
    hash = "sha256-arAF0Xk5QO+dtnhCmeoWu8TrQK8gqNK/nzQYhiSEBr4=";
  };

  # Only the CLIProxyAPI SDK packages are imported, so the vendor closure is
  # small. Recompute if the SDK version in go.mod changes.
  vendorHash = "sha256-vSLDY8mpkqVv5NNF9MA1EsBeZtbyAz/WTKHT4g9aRgY=";

  env.CGO_ENABLED = "1";

  # The plugin is a C shared library, so the default Go build is replaced.
  buildPhase = ''
    runHook preBuild
    go build -mod=vendor -trimpath -buildmode=c-shared \
      -ldflags="-s -w -X main.Version=${finalAttrs.version}" \
      -o commandcode-bridge.so ./cmd/commandcode-bridge
    runHook postBuild
  '';

  installPhase = ''
    runHook preInstall
    install -Dm444 commandcode-bridge.so "$out/lib/commandcode-bridge.so"
    install -Dm444 LICENSE "$out/share/licenses/commandcode-bridge/LICENSE"
    runHook postInstall
  '';

  # The shared library is the deliverable; the default check phase expects Go binaries.
  doCheck = false;

  meta = {
    description = "Native CLIProxyAPI plugin giving Command Code parity with the built-in channels";
    homepage = "https://github.com/markoonakic/cliproxyapi-commandcode-bridge";
    license = lib.licenses.mit;
    platforms = [ "x86_64-linux" ];
  };
})
