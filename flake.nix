{
  description = "Modular Air-Gapped Workstation Infrastructure Configuration";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    
    # Public fallback repository placeholder. 
    # The Go installer overrides this input target locally when building in private environments.
    nix-home-work.url = "github:nix-community/empty-flake";
  };

  outputs = { self, nixpkgs, home-manager, nix-home-work, ... }: {
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

    nixosConfigurations.iso = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        "${nixpkgs}/nixos/modules/installer/cd-dvd/installation-cd-minimal.nix"
        ({ pkgs, ... }: {
          environment.systemPackages = [ pkgs.git ];
        })
      ];
    };
  };
}
