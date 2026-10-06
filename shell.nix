# Development shell for Threavia.
#
#   nix-shell            # enter the shell
#   nix-shell --run make # or run a single target
#
# Go needs no C toolchain, and the repository has no cgo dependency: a plain
# `go build ./...` is fully static. `go test -race` is the exception. The race
# detector is ThreadSanitizer, a C++ runtime shipped precompiled with Go, so a
# race-enabled binary forces cgo and external linking, which needs a C compiler
# and the system linker. Hence gcc below.
{ pkgs ? import <nixpkgs> { } }:

pkgs.mkShell {
  packages = [
    # go.mod requires Go 1.25; fall back to the channel default when the
    # versioned attribute is gone, with GOTOOLCHAIN below as the safety net.
    (pkgs.go_1_25 or pkgs.go)

    # Required by `make test-race` only.
    pkgs.gcc

    pkgs.gnumake

    # `helm lint deploy/helm/threavia` and `helm template`.
    pkgs.kubernetes-helm
  ];

  # buf, protoc-gen-go and protoc-gen-go-grpc are deliberately absent: the
  # Makefile pins their versions and installs them with `go install`, so that
  # `make generate` produces identical bytes on every machine. Run `make tools`
  # once, they land in $(go env GOPATH)/bin.

  shellHook = ''
    # Let the go command fetch the toolchain go.mod asks for when the Go in this
    # shell is older than that.
    export GOTOOLCHAIN=''${GOTOOLCHAIN:-auto}
    export PATH="$(go env GOPATH)/bin:$PATH"
  '';
}
