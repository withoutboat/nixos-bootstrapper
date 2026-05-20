# NixOS Automated Workstation Bootstrapper

An automated, offline-first NixOS deployment engine managed via an internal Go runtime manager.

## System Topology Layout

* `go-installer/` - System orchestration client responsible for environment setups and executing native local configurations.
* `hosts/workstation/` - Baseline structural settings defining primary operation patterns and runtime dependencies.
* `flake.nix` - Master state resolution index controlling tracking targets across external system nodes.

## Development Tasks

To build and compile execution binaries and test configuration generation logic locally on a machine with Nix enabled:

```bash
# 1. Build the Go installation controller target
cd go-installer && GOOS=linux GOARCH=amd64 go build -o ../go-installer-bin main.go && cd ..

# 2. Stage layout parameters for execution testing mock ups
mkdir -p ./iso-registry
cp -r ./hosts ./flake.nix ./iso-registry/nixos-config/
cp ./go-installer-bin ./iso-registry/installer

# 3. Evaluate and generate system installer boot media targets
nix build .#nixosConfigurations.iso.config.system.build.isoImage
