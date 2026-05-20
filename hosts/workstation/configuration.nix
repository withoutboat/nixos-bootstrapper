{ config, pkgs, ... }:

{
  system.stateVersion = "24.11"; # Set to match target base evaluation channel version state

  boot.loader.systemd-boot.enable = true;
  boot.loader.efi.canTouchEfiVariables = true;

  networking.hostName = "workstation";
  networking.networkmanager.enable = true;

  time.timeZone = "UTC";

  users.users.developer = {
    isNormalUser = true;
    description = "Primary Developer Account";
    extraGroups = [ "networkmanager" "wheel" "docker" ];
  };

  environment.systemPackages = with pkgs; [
    git
    vim
    curl
  ];
}
