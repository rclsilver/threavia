{
  lib,
  buildGoModule,
  go_1_26,
  version ? "dev",
}:

# The Codex backend, for a machine that holds the code.
#
# Only the backend is packaged here. Core is deployed from its container image
# and its Helm chart, and its binary embeds a web client that would need the
# whole Node toolchain to build; a Nix package of Core that silently served no
# client would be worse than none.
(buildGoModule.override { go = go_1_26; }) {
  pname = "threavia-backend-codex";
  inherit version;

  # Everything the Go build reads, and nothing else: the client, the artwork and
  # the deployment files are not inputs, and leaving them in would rebuild the
  # backend every time one of them changes.
  src = lib.cleanSourceWith {
    name = "threavia-source";
    src = ../.;
    filter =
      path: _type:
      let
        relative = lib.removePrefix (toString ../. + "/") (toString path);
        top = builtins.head (lib.splitString "/" relative);

        # The client is not an input of the backend, and the built one would
        # change this package's hash every time a stylesheet did. The Go package
        # that embeds it still has to exist, because resolving the module graph
        # walks Core's import of it, and the embed needs a directory to match.
        insideClient = lib.hasPrefix "web/ui" relative;
        clientKept = builtins.elem relative [
          "web/ui"
          "web/ui/dist"
          "web/ui/dist/.gitkeep"
        ];
      in
      !(builtins.elem top [
        "bin"
        "brand"
        "deploy"
        "docs"
        "examples"
        "nix"
      ])
      && !(lib.hasInfix "node_modules" relative)
      && (!insideClient || clientKept);
  };

  vendorHash = "sha256-BRVPytQwvq+BfymcH/4nzwEffPBfC+FgxSoUEz5+Hdw=";

  subPackages = [ "cmd/threavia-backend-codex" ];

  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  # The suite needs a PostgreSQL to talk to and a Docker daemon for the
  # end-to-end paths, neither of which exists in a build sandbox. It runs in CI,
  # where both do.
  doCheck = false;

  meta = {
    description = "Threavia backend for Codex CLI";
    homepage = "https://github.com/rclsilver/threavia";
    license = lib.licenses.asl20;
    mainProgram = "threavia-backend-codex";
    platforms = lib.platforms.unix;
  };
}
