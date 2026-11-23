# AGENTS.md

`hue-jackman` is a Linux pure-Go CLI that controls Chroma RGB lighting on the Razer Wolverine V2 Chroma (`1532:0a2e`) over `usbdevfs`.

## Setup

- Stage new Nix files (`flake.nix`, `nix/`) in Git before entering the dev shell, because Nix flakes ignore untracked files.
- Approve `.envrc` with `direnv allow` when using direnv, or enter the dev shell directly with `nix develop`.
- Keep the Go module dependency-free (`CGO_ENABLED=0`, Go standard library only) unless a clear cross-platform requirement justifies external packages.

## Build and test

Run these commands from the repository root:

```bash
go vet ./...
go test -cover -covermode=atomic ./...
CGO_ENABLED=0 go build -o bin/hue-jackman ./cmd/hue-jackman
```

Inside the Nix dev shell (where `just`, `golangci-lint`, `govulncheck`, `gocyclo`, and `actionlint` are installed):

```bash
just build
just test
just lint
just lint correct
```

## Architecture notes

- Keep protocol encoding, decoding, and checksum logic in `internal/protocol` free of OS or USB I/O dependencies so tests run anywhere.
- Keep Linux `usbdevfs` syscalls and sysfs discovery in `internal/usb`, isolated behind `//go:build linux` for ioctl code.
- Keep CLI flag parsing and command dispatch in `cmd/hue-jackman`.

## Hardware and protocol rules

- Target USB VID `0x1532`, PID `0x0a2e`, interface `0`, interrupt OUT `0x01`, interrupt IN `0x81`.
- Frame every Razer vendor command as a 64-byte GIP `0x0f` request (`0x10` reply) with length `0x3c`, `transaction_id 0x1f`, up to 50 argument bytes (`0x0c..0x3d`), and an XOR checksum of bytes `0x06..0x3d` inclusive at `0x3e`.
- Match replies on message ID `0x10`, sequence byte (`0x02`), command class (`0x0a`), and command ID (`0x0b`), retrying once with the next sequence byte after a stale reply; skip routine GIP input (`0x20`), keepalive (`0x03`), and guide (`0x07`) packets on endpoint `0x81`.
- Pin `usbdevfsURB` and transfer buffers with `runtime.Pinner` across `USBDEVFS_SUBMITURB` and `USBDEVFS_REAPURBNDELAY`. On timeout, call `USBDEVFS_DISCARDURB` and wait for `USBDEVFS_REAPURB` before unpinning.
- Always detach a bound kernel driver (`xpad`) via `USBDEVFS_IOCTL(USBDEVFS_DISCONNECT)` on open and reattach via `USBDEVFS_CONNECT` on close unless `--keep-detached` is set.
- Do not send `class 0x00, cmd 0x04` with payload `0x01`; sibling Wolverine firmware associates it with reset or shutdown.
- Treat `row 0, col 0` as the left light strip and `row 0, col 1` as the right strip under Extended Matrix Custom Frame (`0x0f 0x02` effect `0x08` + `0x0f 0x03`). Firmware rejects `stop_col >= 3` with `not_supported` (`0x05`).

## Testing

- Verify any new `internal/protocol` frame builder against golden byte sequences and XOR CRCs in `internal/protocol/protocol_test.go`.
- Preserve 64-bit Linux `usbdevfs` struct size and ioctl constant tests in `internal/usb/usb_test.go`.
- Use `--dry-run` to validate CLI frame output in environments without `/dev/bus/usb` access.

## Tailor and repo management

- Do not hand-edit Tailor `always`-managed files (`just/tailor.just`, `just/loader.just`, `nix/loader.nix`, `nix/go.nix`, `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SUPPORT.md`).
- Put custom build, test, or lint hooks in `just/project/{build,test,lint}.sh` rather than editing `just/tailor.just`.
- Keep `.goreleaser.yaml`, `.github/workflows/build-go.yml`, and `nix/package.nix` Linux-only (`deb`, `rpm`, `apk`, `archlinux`, bare Linux binaries, and `meta.platforms = pkgs.lib.platforms.linux`) for Haberdasher. Do not re-add `Dockerfile` (`alteration: never` in `.tailor.yml`).
- Preview Tailor changes with `tailor baste --verbose` and get explicit approval before running `tailor alter`.

## Gotchas

- Linux `usbdevice_fs.h` inverts `_IOR` and `_IOW` on several ioctls (for example `USBDEVFS_SUBMITURB` uses `_IOR` and `USBDEVFS_REAPURB` uses `_IOW`). Never "fix" ioctl directions without checking `<linux/usbdevice_fs.h>`.
- `/dev/bus/usb` for `1532:0a2e` lacks `uaccess` by default; live runs need `sudo` or a udev rule granting `uaccess`.
