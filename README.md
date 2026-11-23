<h1 align="center">
  <img src="pages/icon.svg" width="256" height="256" alt="hue-jackman">
  <br />
  hue-jackman
</h1>

<p align="center"><b>Razer Wolverine V2 Chroma RGB control for Linux. Hijack the hues, bub 🎮</b></p>

<p align="center">Made with 💝 for 🐧</p>

`hue-jackman` is a Linux command-line tool that controls Chroma RGB lighting on the Razer Wolverine V2 Chroma (`1532:0a2e`). It speaks directly to Linux `usbdevfs` in pure Go with no CGo or `libusb` dependency, detaches `xpad` while sending commands, and reattaches `xpad` on exit.

It supports whole-controller effects, brightness, device info queries, and independent 24-bit RGB control of the left and right light strips.

## Install

| | Platform | Repository |
| :-: | --- | --- |
| <img src="https://apt.wimpys.world/logo.svg" width="32" height="32" alt=""> | Debian and Ubuntu | [apt.wimpys.world](https://apt.wimpys.world/#hue-jackman) |
| <img src="https://rpm.wimpys.world/logo.svg" width="32" height="32" alt=""> | Fedora and openSUSE | [rpm.wimpys.world](https://rpm.wimpys.world/#hue-jackman) |
| <img src="https://pkg.wimpys.world/logo.svg" width="32" height="32" alt=""> | Arch Linux | [pkg.wimpys.world](https://pkg.wimpys.world/#hue-jackman) |
| <img src="https://apk.wimpys.world/logo.svg" width="32" height="32" alt=""> | Alpine Linux | [apk.wimpys.world](https://apk.wimpys.world/#hue-jackman) |
| <img src="https://nix.wimpys.world/logo.svg" width="32" height="32" alt=""> | Nix and NixOS | [nix.wimpys.world](https://nix.wimpys.world/#hue-jackman) |

Each repository is signed, and its page gives the setup steps and keeps `hue-jackman` up to date.

### Any Linux

[bin](https://github.com/marcosnils/bin) installs and updates the release binary:

```sh
bin install github.com/wimpysworld/hue-jackman
```

### Build from source

Build a static binary with Go:

```bash
CGO_ENABLED=0 go build -o bin/hue-jackman ./cmd/hue-jackman
```

If you use the Nix development shell and `just`, run:

```bash
just build
```

## Usage

```bash
# Read firmware version, serial number, device name, and active profile
sudo ./bin/hue-jackman info

# Set the left and right light strips independently (left red, right blue)
sudo ./bin/hue-jackman segments FF0000 0000FF

# Set both strips to one solid colour
sudo ./bin/hue-jackman static FF00FF

# Set 1-colour or 2-colour breathing, spectrum cycling, brightness, or off
sudo ./bin/hue-jackman breathing 00FF00 FF00FF
sudo ./bin/hue-jackman spectrum
sudo ./bin/hue-jackman brightness 128
sudo ./bin/hue-jackman off
```

Pass `--dry-run` to print encoded 64-byte frames without touching USB, or `--verbose` to log raw TX and RX frames to `stderr`.

| Command | Action |
| --- | --- |
| `info` | Read firmware version, serial number, device name, and active profile |
| `segments <LEFT> <RIGHT>` | Set left (`col 0`) and right (`col 1`) strips independently (`RRGGBB`) |
| `static <RRGGBB>` | Set both strips to one solid colour |
| `breathing <RGB1> [RGB2]` | Set 1-colour or 2-colour breathing |
| `spectrum` | Enable spectrum cycling |
| `brightness <0-255>` | Set Chroma brightness (`led_id 0x05`) |
| `off` | Turn Chroma lighting off |
| `raw <class> <cmd> [hex...]` | Send an arbitrary `0x0f` vendor command |

## USB permissions

By default, `/dev/bus/usb/*` for `1532:0a2e` has no `uaccess` tag, so running without `sudo` fails with permission denied. To grant access to your desktop session, add a udev rule such as `/etc/udev/rules.d/99-hue-jackman.rules`:

```udev
SUBSYSTEM=="usb", ATTR{idVendor}=="1532", ATTR{idProduct}=="0a2e", TAG+="uaccess"
```

Reload udev rules and replug the controller (or trigger udev):

```bash
sudo udevadm control --reload-rules && sudo udevadm trigger
```

## Protocol summary

The Wolverine V2 Chroma exposes Xbox One Game Input Protocol (GIP) interfaces (`ff/47/d0`) with no USB HID interface. `hue-jackman` sends 64-byte GIP vendor frames on interface `0` interrupt endpoints `0x01` (OUT) and `0x81` (IN):

- Bytes `0x00..0x03`: GIP header (`0x0f` request or `0x10` reply, flags `0x00`, rolling sequence byte, length `0x3c`).
- Bytes `0x04..0x3f`: 60-byte Razer report (`status`, `transaction_id 0x1f`, `data_size`, `command_class`, `command_id`, up to 50 argument bytes, XOR checksum of `0x06..0x3d` at `0x3e`, reserved `0x00`).
- Presets and brightness use Extended Matrix class `0x0f` (`cmd 0x02` effect, `cmd 0x04` brightness on `led_id 0x05`).
- Independent two-segment control switches to Extended Matrix Custom Frame mode (`0x0f 0x02`, `effect_id 0x08`) and writes row `0`, columns `0..1` via `0x0f 0x03`.
