# nixos-bootstrapper

`nixos-bootstrapper` — это Linux amd64 bootstrapper/installer, который запускается **внутри ISO-установщика** и разворачивает NixOS на выбранный диск.  
Сам бинарник `nixos-bootstrapper` **не является macOS-приложением**.

## Запись ISO на загрузочную флешку (Linux)

1. Подключите флешку.
2. Найдите устройство:

```bash
lsblk
```

3. Запишите ISO на **весь диск** (пример: `/dev/sdb`, не `/dev/sdb1`):

```bash
ISO_PATH="/path/to/nixos-installer.iso"
USB_DISK="/dev/sdX"

sudo dd if="$ISO_PATH" of="$USB_DISK" bs=4M status=progress oflag=sync conv=fsync
sync
```

⚠️ `of=` должен указывать на **диск целиком**, а не на раздел.  
⚠️ Выбранный диск будет полностью перезаписан.

## Запись ISO на загрузочную флешку (macOS)

1. Подключите флешку.
2. Найдите номер диска флешки:

```bash
diskutil list
```

3. Размонтируйте диск флешки:

```bash
diskutil unmountDisk /dev/diskN
```

4. Запишите ISO через raw-устройство (быстрее):

```bash
sudo dd if=/path/to/nixos-installer.iso of=/dev/rdiskN bs=1m
sync
diskutil eject /dev/diskN
```

Замените `N` на номер вашей флешки из `diskutil list` (например, `disk2`/`rdisk2`).

⚠️ Не используйте команды, которые автоматически выбирают диск.  
⚠️ Будьте внимательны: выбранный диск будет полностью перезаписан.

## Загрузка с флешки и установка

1. Загрузите целевую машину с записанной USB-флешки (через Boot Menu/UEFI).
2. Запустите bootstrapper в среде ISO.
3. Пройдите интерактивный сценарий:
   - выбор `host`;
   - выбор целевого диска;
   - настройка EFI;
   - `username`;
   - `passphrase`;
   - Wi-Fi (SSID/пароль), если требуется.
4. Подтвердите установку и дождитесь завершения.

⚠️ Выбранный целевой диск стирается в процессе установки.

## Сборка и тесты (Go)

Команды, которые используются в этом репозитории:

```bash
go test ./...
go build
```

Для сборки Linux amd64 статического бинарника (как в релизном workflow):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o nixos-bootstrapper main.go
```

## Релизы

- `.github/workflows/release.yml` в этом репозитории собирает и публикует `nixos-bootstrapper-linux-amd64.tar.gz` при push тега `v*`.
- ISO-образ собирается и публикуется workflow в репозитории `withoutboat/nix-core`, а не в этом репозитории.
