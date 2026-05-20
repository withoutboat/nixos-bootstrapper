{
  description = "Modular Air-Gapped Workstation Infrastructure Configuration";

 inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    
    # Placeholder
    nix-home-work.url = "github:nix-systems/default";
  }; 
  outputs = { self, nixpkgs, home-manager, nix-home-work, ... }: {
    
    # -------------------------------------------------------------------------
    # Target Deployment Workstation Configuration
    # -------------------------------------------------------------------------
    nixosConfigurations.workstation = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ./hosts/workstation/configuration.nix
        home-manager.nixosModules.home-manager {
          home-manager.useGlobalPkgs = true;
          home-manager.useUserPackages = true;
          home-manager.users.developer = {
            imports = [ 
              ./hosts/workstation/home.nix
              nix-home-work.homeManagerModules.default
            ];
          };
        }
      ];
    };

    # -------------------------------------------------------------------------
    # Custom Bootstrapper ISO Configuration (Flashdrive A)
    # -------------------------------------------------------------------------
    nixosConfigurations.iso = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        # Base NixOS Live-CD profile
        "${nixpkgs}/nixos/modules/installer/cd-dvd/installation-cd-minimal.nix"
        
        ({ pkgs, ... }: {
          # Core utilities required for partitioning, encryption, and running the installer
          boot.supportedFilesystems = [ "zfs" "ext4" "vfat" ];
          boot.kernelPackages = pkgs.linuxPackages_latest;

          environment.systemPackages = [ 
            pkgs.git
            pkgs.cryptsetup
            pkgs.parted
            pkgs.util-linux
            
            # Inline package generation that injects the pre-built Go binary from Actions
            (pkgs.stdenv.mkDerivation {
              name = "go-installer-bin";
              src = ./go-installer;
              phases = [ "installPhase" ];
              installPhase = ''
                mkdir -p $out/bin
                if [ -f $src/go-installer-linux-amd64 ]; then
                  cp $src/go-installer-linux-amd64 $out/bin/go-installer
                else
                  # Local development fallback script if binary hasn't been compiled yet
                  echo '#!/bin/sh\necho "Error: Go runtime asset not compiled via Actions workflow."' > $out/bin/go-installer
                  chmod +x $out/bin/go-installer
                fi
              '';
            })
          ];

          # Bake the entire repository codebase into the ISO image for offline staging
          environment.etc."nixos-source-registry/nixos-config".source = ./.;

          # Symlink package output to deterministic local workspace tree
          system.activationScripts.installer-symlink = {
            text = ''
              mkdir -p /usr/local/bin
              ln -sf /run/current-system/sw/bin/go-installer /usr/local/bin/go-installer
            '';
          };

          # Handle background TTY initialization configuration
          systemd.services.autostart-installer = {
            description = "Launch NixOS TUI Bootstrapper on Startup";
            after = [ "getty@tty1.service" ];
            wantedBy = [ "multi-user.target" ];
            
            serviceConfig = {
              Type = "idle";
              ExecStart = ''
                ${pkgs.util-linux}/bin/agetty --autologin root --noclear tty1 $TERM
              '';
              StandardInput = "tty";
              StandardOutput = "tty";
              StandardError = "journal";
              Restart = "no";
            };
          };

          # Automate direct launch execution handler when root logs onto console
          programs.bash.loginShellInit = ''
            if [ "$(tty)" = "/dev/tty1" ]; then
              echo "--------------------------------------------------"
              echo " Initializing Secure OS Deployment Console...     "
              echo "--------------------------------------------------"
              if [ -f /usr/local/bin/go-installer ]; then
                exec /usr/local/bin/go-installer
              else
                echo "💥 Error: Bootstrapper binary was not compiled successfully."
                echo "Falling back to safe default operational shell."
              fi
            fi
          '';

          # Optimize storage footprint of the generated deployment media
          documentation.enable = false;
          documentation.nixos.enable = false;
        })
      ];
    };
  };
}
