# nixos-bootstrapper

`nixos-bootstrapper` is a Linux amd64 bootstrapper/installer that runs **inside the installer ISO** and deploys NixOS to the selected disk.  
The `nixos-bootstrapper` binary itself is **not a macOS application**.

## Write ISO to a bootable USB drive (Linux)

1. Plug in your USB drive.
2. Find the device:

```bash
lsblk
```

3. Write the ISO to the **whole disk** (example: `/dev/sdb`, not `/dev/sdb1`):

```bash
ISO_PATH="/path/to/nixos-installer.iso"
USB_DISK="/dev/sdX"

sudo dd if="$ISO_PATH" of="$USB_DISK" bs=4M status=progress oflag=sync conv=fsync
sync
```

⚠️ `of=` must point to the **entire disk**, not a partition.  
⚠️ The selected disk will be completely overwritten.

## Write ISO to a bootable USB drive (macOS)

1. Plug in your USB drive.
2. Find the USB disk number:

```bash
diskutil list
```

3. Unmount the USB disk:

```bash
diskutil unmountDisk /dev/diskN
```

4. Write the ISO using the raw device (faster):

```bash
sudo dd if=/path/to/nixos-installer.iso of=/dev/rdiskN bs=1m
sync
diskutil eject /dev/diskN
```

Replace `N` with your USB disk number from `diskutil list` (for example, `disk2`/`rdisk2`).

⚠️ Do not use commands that auto-select the disk.  
⚠️ Be careful: the selected disk will be completely overwritten.

## Boot from USB and run installation

1. Boot the target machine from the USB drive (Boot Menu/UEFI).
2. Start the bootstrapper in the ISO environment.
3. Complete the interactive flow:
   - select `host`;
   - select target disk;
   - configure EFI;
   - set `username`;
   - set `passphrase`;
   - configure Wi-Fi (SSID/password), if needed.
4. Confirm installation and wait for completion.

⚠️ The selected target disk is erased during installation.

## Build and test (Go)

Commands used in this repository:

```bash
go test ./...
go build
```

To build a static Linux amd64 binary (as in the release workflow):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o nixos-bootstrapper main.go
```

## Get SHA-256/SRI for release asset (without Nix)

For `v0.1.50`, hash this release asset:
`nixos-bootstrapper-linux-amd64.tar.gz`

Linux (GNU coreutils):

```bash
curl -fL 'https://github.com/withoutboat/nixos-bootstrapper/releases/download/v0.1.50/nixos-bootstrapper-linux-amd64.tar.gz' -o nixos-bootstrapper-linux-amd64.tar.gz
sha256sum nixos-bootstrapper-linux-amd64.tar.gz
```

macOS (BSD tools):

```bash
curl -fL 'https://github.com/withoutboat/nixos-bootstrapper/releases/download/v0.1.50/nixos-bootstrapper-linux-amd64.tar.gz' -o nixos-bootstrapper-linux-amd64.tar.gz
shasum -a 256 nixos-bootstrapper-linux-amd64.tar.gz
```

The hexadecimal digest printed by `sha256sum`/`shasum` is not directly the Nix SRI value. Convert the downloaded file to SRI format with portable Python 3:

```bash
python3 -c 'import base64,hashlib; print("sha256-" + base64.b64encode(hashlib.sha256(open("nixos-bootstrapper-linux-amd64.tar.gz","rb").read()).digest()).decode())'
```

Use the result in `nix-core/pkgs/nixos-bootstrapper.nix`:

```nix
version = "0.1.50";
sha256 = "sha256-...";
```

This hashes the `.tar.gz` archive itself (not an unpacked directory), because the package uses `fetchurl`.

## Releases

- `.github/workflows/release.yml` in this repository builds and publishes `nixos-bootstrapper-linux-amd64.tar.gz` on push of tags matching `v*`.
- The ISO image is built and published by a workflow in `withoutboat/nix-core`, not by this repository.
