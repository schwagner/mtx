{
  description = "mtx: a terminal Matrix client with end-to-end encryption, built on slk's TUI";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs {
          inherit system;
        };
        lib = pkgs.lib;

        isLinux = pkgs.stdenv.hostPlatform.isLinux;
        libX11 = pkgs.libX11;

        # Nix has no view of the tag a flake was fetched at, so the version
        # has to be written down; bump it alongside the git tag.
        version = "0.1.0";

        # sqlite_fts5: hicli's local message search needs SQLite's FTS5
        #   (go-sqlite3 compiles its bundled SQLite with it).
        # goolm: mautrix's pure-Go Olm/Megolm instead of libolm, which is
        #   unmaintained and marked insecure in nixpkgs.
        tags = [ "sqlite_fts5" "goolm" ];

        mtx = pkgs.buildGo126Module {
          pname = "mtx";
          inherit version tags;
          src = ./.;
          subPackages = [ "cmd/mtx" ];
          vendorHash = "sha256-zGJjqSwxmDVVxi1+F/vRJ8yXLTAaXR1v24bt9cysnmA=";

          # golang.design/x/clipboard compiles a cgo shim that #includes
          # X11/Xlib.h, so the headers are needed to build at all on Linux.
          # See postFixup for why this is necessary but not sufficient.
          buildInputs = lib.optionals isLinux [ libX11 ];

          nativeBuildInputs = lib.optionals isLinux [ pkgs.makeWrapper ];

          # The test suite spins up httptest servers on loopback. The macOS
          # Nix sandbox denies all networking by default, so allow loopback
          # there; the attribute is a no-op on Linux. (The Matrix
          # integration test skips itself without MTX_TEST_HOMESERVER.)
          __darwinAllowLocalNetworking = true;

          # Run the whole module's tests, not just cmd/mtx's.
          checkPhase = ''
            runHook preCheck
            go test -tags "${lib.concatStringsSep "," tags}" ./...
            runHook postCheck
          '';

          # lastModifiedDate is used for the build date because it is a
          # property of the locked revision — a wall-clock timestamp would
          # make the derivation non-reproducible.
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${version}"
            "-X main.commit=${self.shortRev or (self.dirtyShortRev or "unknown")}"
            "-X main.date=${self.lastModifiedDate or "unknown"}"
          ];

          # golang.design/x/clipboard does not link against libX11 — it
          # dlopen()s a bare "libX11.so" at runtime, so on NixOS that lookup
          # fails and copy/paste silently degrades to the OSC 52 fallback
          # unless libX11 is on LD_LIBRARY_PATH.
          postFixup = lib.optionalString isLinux ''
            wrapProgram $out/bin/mtx \
              --prefix LD_LIBRARY_PATH : ${lib.makeLibraryPath [ libX11 ]}
          '';

          meta = {
            description = "Terminal Matrix client with end-to-end encryption";
            license = lib.licenses.mit;
            mainProgram = "mtx";
            platforms = lib.platforms.unix;
          };
        };
      in {
        packages.default = mtx;
        packages.mtx = mtx;
        # `nix develop`: Go, a C compiler (cgo SQLite) and libX11.
        devShells.default = pkgs.mkShell {
          inputsFrom = [ mtx ];
          packages = [ pkgs.gopls pkgs.golangci-lint ];
        };
      });
}
