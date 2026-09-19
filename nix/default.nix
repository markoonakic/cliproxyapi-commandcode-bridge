{
  lib,
  buildGoModule,
}:
buildGoModule (finalAttrs: {
  pname = "commandcode-bridge";
  version = "1.0.0";

  # Built from the local source tree. The NixOS module passes the repository
  # checkout here; there is no upstream release to fetch.
  src = ../.;

  # Only the CLIProxyAPI SDK packages are imported, so the vendor closure is
  # small. vendorHash is recomputed when the SDK version in go.mod changes.
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

  # The .so is the deliverable; the default check phase would look for Go binaries.
  doCheck = false;

  meta = {
    description = "Native CLIProxyAPI plugin giving Command Code parity with the built-in channels";
    homepage = "https://github.com/markoonakic/cliproxyapi-commandcode-bridge";
    license = lib.licenses.mit;
    platforms = [ "x86_64-linux" ];
  };
})
