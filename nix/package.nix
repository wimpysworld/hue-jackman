{ pkgs, self, ... }:
let
  # The release workflow writes nix/version into the commit that the latest tag names.
  version =
    if builtins.pathExists ./version then
      pkgs.lib.trim (builtins.readFile ./version)
    else
      "0.0.0-${self.sourceInfo.shortRev or (self.sourceInfo.dirtyShortRev or "dirty")}";
in
pkgs.buildGo126Module {
  pname = "hue-jackman";
  inherit version;
  src = self;
  vendorHash = null;
  subPackages = [ "cmd/hue-jackman" ];
  env.CGO_ENABLED = 0;
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];
  meta = {
    description = "Razer Wolverine V2 Chroma RGB control for Linux.";
    homepage = "https://hue-jackman.wimpys.world/";
    license = pkgs.lib.licenses.blueOak100;
    mainProgram = "hue-jackman";
    platforms = pkgs.lib.platforms.linux;
  };
}
