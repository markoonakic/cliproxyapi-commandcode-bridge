{
  description = "Native CLIProxyAPI plugin giving Command Code parity with the built-in channels";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: rec {
        command-code = pkgs.callPackage ./nix/default.nix { };
        default = command-code;
      });

      checks = forAllSystems (pkgs: {
        # Builds the shared library, which is the real deliverable.
        build = self.packages.${pkgs.system}.command-code;
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt-tree);

      # Go toolchain and a C compiler for the c-shared build. The Nix package
      # pins its own Go, so this shell is only for local development and CI.
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go_1_26
            pkgs.gcc
          ];
        };
      });
    };
}
