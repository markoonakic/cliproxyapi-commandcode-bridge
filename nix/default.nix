{
  lib,
  buildGoModule,
}:
buildGoModule (finalAttrs: {
  pname = "cliproxyapi-command-code";
  version = "1.2.3";

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
      -o command-code.so ./cmd/commandcode-bridge
    runHook postBuild
  '';

  installPhase = ''
    runHook preInstall
    install -Dm444 command-code.so "$out/lib/command-code.so"
    install -Dm444 LICENSE "$out/share/licenses/command-code/LICENSE"
    runHook postInstall
  '';

  # The .so is the deliverable; the default check phase would look for Go binaries.
  doCheck = false;

  meta = {
    description = "Native CLIProxyAPI plugin for Command Code, with parity to the built-in channels";
    homepage = "https://github.com/markoonakic/cliproxyapi-commandcode-bridge";
    license = lib.licenses.mit;
    platforms = [ "x86_64-linux" ];
  };
})
