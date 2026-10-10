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
        threavia-backend-codex = pkgs.callPackage ./nix/backend-codex.nix { inherit version; };
        default = threavia-backend-claude;
      });

      nixosModules = rec {
        backend-claude = import ./nix/backend-claude-module.nix;
        backend-codex = import ./nix/backend-codex-module.nix;
        default = backend-claude;
      };

      overlays.default = _final: prev: {
        threavia-backend-claude = self.packages.${prev.stdenv.hostPlatform.system}.threavia-backend-claude;
        threavia-backend-codex = self.packages.${prev.stdenv.hostPlatform.system}.threavia-backend-codex;
      };

      checks = forAllSystems (
        pkgs:
        {
          package = self.packages.${pkgs.stdenv.hostPlatform.system}.threavia-backend-claude;
          package-codex = self.packages.${pkgs.stdenv.hostPlatform.system}.threavia-backend-codex;
        }
        // nixpkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          nixos-module-codex =
            (nixpkgs.lib.nixosSystem {
              system = pkgs.stdenv.hostPlatform.system;
              modules = [
                self.nixosModules.backend-codex
                {
                  boot.loader.grub.devices = [ "nodev" ];
                  fileSystems."/" = {
                    device = "/dev/null";
                    fsType = "ext4";
                  };
                  system.stateVersion = "25.05";
                  networking.hostName = "codex-laptop";
                  services.threavia-backend-codex = {
                    enable = true;
                    package = self.packages.${pkgs.stdenv.hostPlatform.system}.threavia-backend-codex;
                    coreAddress = "threavia.example.com:9090";
                    coreApi = "https://threavia.example.com";
                    user = "alex";
                    defaultWorkingDirectory = "/home/alex/git";
                    codexBinary = "/home/alex/.local/bin/codex";
                    environmentFile = "/run/secrets/threavia-codex";
                  };
                }
              ];
            }).config.systemd.units."threavia-backend-codex.service".unit;
          # The module has to keep evaluating. A renamed option or an
          # environment variable the backend no longer reads would otherwise be
          # found by the first person to rebuild their laptop, which is the one
          # moment they cannot afford it.
          nixos-module =
            (nixpkgs.lib.nixosSystem {
              system = pkgs.stdenv.hostPlatform.system;
              modules = [
                self.nixosModules.backend-claude
                {
                  boot.loader.grub.devices = [ "nodev" ];
                  fileSystems."/" = {
                    device = "/dev/null";
                    fsType = "ext4";
                  };
                  system.stateVersion = "25.05";
                  networking.hostName = "laptop";

                  services.threavia-backend-claude = {
                    enable = true;
                    package = self.packages.${pkgs.stdenv.hostPlatform.system}.threavia-backend-claude;
                    coreAddress = "threavia.example.com:9090";
                    coreApi = "https://threavia.example.com";
                    user = "thomas";
                    defaultWorkingDirectory = "/home/thomas/git";
                    discoveryRoots = [ "/home/thomas/git" ];
                    claudeBinary = "/home/thomas/.local/bin/claude";
                    environmentFile = "/run/secrets/threavia-backend";
                  };
                }
              ];
            }).config.systemd.units."threavia-backend-claude.service".unit;
        }
      );

      devShells = forAllSystems (pkgs: {
        default = import ./shell.nix { inherit pkgs; };
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt-rfc-style);
    };
}
