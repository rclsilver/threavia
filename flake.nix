{
  description = "Threavia — a self-hosted control plane for coding agents";

  # Pinned rather than taken from the channel: the module graph needs Go 1.26,
  # and a machine whose channel is older would fail to build with a message
  # about go.mod rather than about its channel.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
        "x86_64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      # What the binary reports as its version. A working tree has no revision,
      # and saying so is better than stamping a release number on a build that
      # is not one.
      version = self.shortRev or self.dirtyShortRev or "dev";
    in
    {
      packages = forAllSystems (pkgs: rec {
        threavia-backend-claude = pkgs.callPackage ./nix/backend-claude.nix { inherit version; };
        default = threavia-backend-claude;
      });

      nixosModules = rec {
        backend-claude = import ./nix/backend-claude-module.nix;
        default = backend-claude;
      };

      overlays.default = _final: prev: {
        threavia-backend-claude = self.packages.${prev.stdenv.hostPlatform.system}.threavia-backend-claude;
      };

      devShells = forAllSystems (pkgs: {
        default = import ./shell.nix { inherit pkgs; };
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt-rfc-style);
    };
}
