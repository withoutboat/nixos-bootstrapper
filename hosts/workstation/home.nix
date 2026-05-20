{ config, pkgs, ... }:

{
  home.username = "developer";
  home.homeDirectory = "/home/developer";
  home.stateVersion = "24.11";

  programs.home-manager.enable = true;

  programs.bash = {
    enable = true;
    shellAliases = {
      ll = "ls -l";
      ".." = "cd ..";
    };
  };
}
