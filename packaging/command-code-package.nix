# New CLIProxyAPI plugin derivation for the side-by-side trial.
#
# This is a SECOND plugin package. It does not replace
# packages/commandcode-bridge/default.nix, which stays pinned to the community
# plugin so that plugin keeps running unchanged during the trial.
#
# Install path is $out/lib/command-code.so, and the file name is the plugin id,
# so the host treats it as a separate plugin from commandcode-bridge.
{
  lib,
  buildGoModule,
  fetchFromGitHub,
}:
buildGoModule (finalAttrs: {
  pname = "cliproxyapi-command-code";
  version = "1.2.0";

  src = fetchFromGitHub {
    owner = "markoonakic";
    repo = "cliproxyapi-commandcode-bridge";
    rev = "b9b958ac289ebc441dee5d899e1729d4ac966c0c";
    hash = "sha256-xpuwSoIU5igK37X8wuywanibMBNsAFRQWdt7mXNHY2E=";
  };

  # Only the CLIProxyAPI SDK packages are imported, so the closure is small.
  # Recompute if the CLIProxyAPI version in go.mod changes.
  vendorHash = "sha256-vSLDY8mpkqVv5NNF9MA1EsBeZtbyAz/WTKHT4g9aRgY=";

  env.CGO_ENABLED = "1";

  # The plugin is a C shared library, so the default Go build is replaced.
  buildPhase = ''
    runHook preBuild
    go build -mod=vendor -trimpath -buildmode=c-shared \
      -ldflags="-s -w -X main.Version=${finalAttrs.version}" \
      -o command-code.so ./cmd/commandcode-bridge
    runHook postBuild
  '';

  installPhase = ''
    runHook preInstall
    install -Dm444 command-code.so "$out/lib/command-code.so"
    install -Dm444 LICENSE "$out/share/licenses/command-code/LICENSE"
    runHook postInstall
  '';

  doCheck = false;

  meta = {
    description = "Native CLIProxyAPI plugin for Command Code, with parity to the built-in channels";
    homepage = "https://github.com/markoonakic/cliproxyapi-commandcode-bridge";
    license = lib.licenses.mit;
    platforms = [ "x86_64-linux" ];
  };
})
