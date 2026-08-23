{
  description = "wt: a git worktree pool manager";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];

      forAllSystems = f: nixpkgs.lib.genAttrs systems f;

      version = self.shortRev or self.dirtyShortRev or "dev";
    in
    {
      # Install into a NixOS system via the overlay:
      #   nixpkgs.overlays = [ wt.overlays.default ];
      #   environment.systemPackages = [ pkgs.wt ];
      overlays.default = final: _prev: {
        wt = self.packages.${final.stdenv.hostPlatform.system}.wt;
      };

      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};

          wt = pkgs.buildGoModule {
            pname = "wt";
            inherit version;

            src = self;

            vendorHash = "sha256-7K17JaXFsjf163g5PXCb5ng2gYdotnZ2IDKk8KFjNj0=";

            subPackages = [ "cmd/wt" ];

            ldflags = [
              "-s"
              "-w"
              "-X"
              "wt/internal/version.Version=${version}"
            ];

            # The integration suite execs `git` and shells out to `go build` for its own
            # test binary, both of which work fine in the sandbox: git is added to
            # nativeCheckInputs below, and the tests set an isolated HOME and never touch
            # /dev/tty, so they don't need network or a real terminal.
            nativeCheckInputs = [ pkgs.git ];

            # buildGoModule's default checkPhase restricts `go test` to subPackages once
            # that's set, which would skip ./tests entirely. Run the whole suite instead,
            # matching the Makefile's `test` target.
            checkPhase = ''
              runHook preCheck
              go test ./...
              runHook postCheck
            '';
          };
        in
        {
          default = wt;
          inherit wt;
        }
      );

      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              pkgs.golangci-lint
              pkgs.gnumake
              pkgs.git
            ];
          };
        }
      );
    };
}
