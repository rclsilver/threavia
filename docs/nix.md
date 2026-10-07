# Running a backend on NixOS

A backend is a separate deployment from Core, never a part of it: it opens the
outbound control stream, holds the provider credentials and owns the filesystem
the agent works on. That is why a laptop backend needs no cluster, no inbound
port and no chart — only a service on the machine that holds the code.

The flake packages the Claude backend and a NixOS module for it. Core is not
packaged here: it is deployed from its container image and its Helm chart, and
its binary embeds a web client that needs the whole Node toolchain to build. A
Nix package of Core that silently served no client would be worse than none.

## What the flake offers

```sh
nix run   github:rclsilver/threavia              # the backend, with the ambient environment
nix build github:rclsilver/threavia              # the same, into ./result
nix develop github:rclsilver/threavia            # the development shell
```

| Output | What it is |
| --- | --- |
| `packages.<system>.threavia-backend-claude` | the backend binary |
| `nixosModules.backend-claude` | `services.threavia-backend-claude` |
| `overlays.default` | the package under `pkgs.threavia-backend-claude` |
| `devShells.<system>.default` | Go, Node, Helm, buf — the same shell as `shell.nix` |

The flake pins its own nixpkgs, because the module graph needs Go 1.26 and a
machine whose channel is older would otherwise fail with a message about
`go.mod` rather than about its channel.

## On a laptop

```nix
# flake.nix of your configuration
{
  inputs.threavia.url = "github:rclsilver/threavia";

  outputs = { nixpkgs, threavia, ... }: {
    nixosConfigurations.laptop = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ./configuration.nix
        threavia.nixosModules.backend-claude
        { services.threavia-backend-claude.package =
            threavia.packages.x86_64-linux.threavia-backend-claude; }
      ];
    };
  };
}
```

```nix
# configuration.nix
services.threavia-backend-claude = {
  enable = true;

  coreAddress = "threavia.example.com:9090";   # the control stream
  coreApi = "https://threavia.example.com";    # called once, to register

  # The account the agent works as. Not a formality: it edits this account's
  # checkouts, runs commands with its permissions and reads the Claude Code
  # credentials in its home.
  user = "thomas";

  discoveryRoots = [ "/home/thomas/git" ];
  defaultWorkingDirectory = "/home/thomas/git";

  # Claude Code authenticates against an account and updates itself, so this is
  # usually an install you already keep. pkgs.claude-code exists but is unfree,
  # which is why the module names neither by default.
  claudeBinary = "/home/thomas/.local/bin/claude";

  # One-shot: needed until the backend has registered, after which its identity
  # lives in the state directory. A file of NAME=value lines, outside the store.
  environmentFile = "/run/secrets/threavia-backend";
};
```

The registration token comes from Core — in the web client, the user panel at
the foot of the sidebar, under Backends. Put it in the environment file:

```
THREAVIA_BACKEND_REGISTRATION_TOKEN=…
```

Then `nixos-rebuild switch`. The backend registers once, stores its persistent
credential in `/var/lib/threavia-backend-claude`, and never uses the token
again. It appears in the client as soon as it connects.

## What the service does and does not bound

`NoNewPrivileges` is on. Nothing else is: no `ProtectHome`, no read-only paths,
no private namespace. The agent's whole job is to read and write the account's
files and run its tools, so each of those would break the thing being asked
for. What bounds it is the execution policy enforced by Core and the backend,
and the permissions of the account it runs as — a guard rail, never a sandbox
(spec section 17).

The service PATH is deliberately short: git, OpenSSH, coreutils and bash, plus
Claude Code. A command the agent runs resolves against that, not against the
person's shell.

## Rebuilding the package

`nix/backend-claude.nix` pins a `vendorHash` over the Go module graph. Changing
a dependency changes it; set it to `lib.fakeHash`, build once, and copy the hash
the error reports.
