{
  description = "A blazingly fast Slack TUI";

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

        # Bumped alongside the git tag at release time. Nix has no view of the
        # tag a flake was fetched at, so unlike goreleaser this cannot derive
        # itself from `{{.Version}}` — it has to be written down.
        version = "0.25.0";

        slk = pkgs.buildGo126Module {
          pname = "slk";
          inherit version;
          src = ./.;
          vendorHash = "sha256-/J4gr4m9v6Y0Be8BU4wepIdl2sjoPh0pFCvJL2kIeLk=";

          # golang.design/x/clipboard compiles a cgo shim that #includes
          # X11/Xlib.h, so the headers are needed to build at all on Linux.
          # See postFixup for why this is necessary but not sufficient.
          buildInputs = lib.optionals isLinux [ libX11 ];

          # The test suite spins up httptest servers on loopback. The macOS
          # Nix sandbox denies all networking by default, so allow loopback
          # there; the attribute is a no-op on Linux.
          __darwinAllowLocalNetworking = true;

          nativeBuildInputs = lib.optionals isLinux [ pkgs.makeWrapper ];

          # Mirrors the ldflags in .goreleaser.yaml so a Nix-built slk reports
          # the same `slk --version` as a released one. Without these, every
          # Nix user sees "slk dev (commit none, built unknown)".
          #
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

          # The buildInput above supplies X11 headers at COMPILE time, but
          # golang.design/x/clipboard does not link against libX11 — it
          # dlopen()s a bare "libX11.so" at runtime. The built binary
          # therefore carries no reference to it, and on any system without a
          # global library directory (NixOS, or `nix profile install` on a
          # foreign distro) that dlopen fails and copy/paste silently
          # degrades to the OSC 52 fallback.
          #
          # Putting libX11 on LD_LIBRARY_PATH is what actually makes the
          # native clipboard path reachable. Linux-only: the darwin backend
          # is ObjC and needs no help.
          postFixup = lib.optionalString isLinux ''
            wrapProgram $out/bin/slk \
              --prefix LD_LIBRARY_PATH : ${lib.makeLibraryPath [ libX11 ]}
          '';

          meta = {
            description = "A blazingly fast Slack TUI";
            homepage = "https://getslk.sh/";
            license = lib.licenses.mit;
            mainProgram = "slk";
            platforms = lib.platforms.unix;
          };
        };
        # At this lock, nixpkgs builds libwebsockets with its event-loop
        # plugin directory as the store path twice over, so ttyd can't load
        # evlib_uv, exits at startup, and VHS fails with
        # ERR_CONNECTION_REFUSED. This is nixpkgs' own fix ("libwebsockets:
        # fix plugin search path", 3c38ce6492); drop it once the nixpkgs
        # lock is past that commit.
        libwebsockets-fixed = pkgs.libwebsockets.overrideAttrs (old: {
          postPatch = old.postPatch + ''
            substituteInPlace cmake/lws_config.h.in \
              --replace-fail '"''${CMAKE_INSTALL_PREFIX}/''${LWS_INSTALL_LIB_DIR}"' '"''${CMAKE_INSTALL_FULL_LIBDIR}"'
          '';
        });
        ttyd-fixed = pkgs.ttyd.override { libwebsockets = libwebsockets-fixed; };
        # VHS with sixel capture (charmbracelet/vhs#783), so the demo GIFs
        # show real images instead of half-blocks. Switch back to pkgs.vhs
        # once that PR is in a release. Go 1.26 because the fork needs Go
        # 1.25.12+, newer than this nixpkgs' go_1_25. Its tests need ttyd
        # and a browser, which the build sandbox lacks.
        vhs-sixel = (pkgs.vhs.override {
          buildGoModule = pkgs.buildGo126Module;
          ttyd = ttyd-fixed;
        }).overrideAttrs (old: {
          version = "${old.version}-sixel";
          src = pkgs.fetchFromGitHub {
            owner = "jchook";
            repo = "vhs";
            rev = "113917ffe5585f19b58596f01b141eb1ebc01b54";
            hash = "sha256-lL5+VOEGP3yiDhUO7CH2CI2Jvy09Zt12YJTiQJesSJ4=";
          };
          vendorHash = "sha256-0/GA+AGyHiw7PvUXRBuL9yylE3NOY3vVUA3uSFAP11Q=";
          doCheck = false;
        });
      in {
        packages.default = slk;
        packages.slk = slk;
        # `nix develop`: slk's build inputs (Go, libX11) plus vhs, for `make demo-gifs`.
        devShells.default = pkgs.mkShell {
          inputsFrom = [ slk ];
          packages = [ vhs-sixel ];
        };
      });
}
