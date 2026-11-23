package main

import "io"

const publicHelp = `Inspect and control Chroma RGB lighting on the Razer Wolverine V2 Chroma.

Usage: hue-jackman [command] [options]

Without a command, inspect all connected supported controllers.

Commands:
  rgb <MODE> [RGB...]           Change the Chroma RGB lighting.
  brightness <0-255>            Change the Chroma brightness level.
  raw <CLASS> <CMD> [HEX...]    Send a raw 0x0f vendor command.

Options:
  --device N|PATH               Select device N or a /dev/bus/usb path.
  --timeout DURATION            USB transfer timeout (default: 1s).
  --dry-run                     Print encoded 64-byte frames without USB access.
  --keep-detached               Leave xpad detached on exit.
  --verbose                     Log raw USB TX/RX frames to stderr.
  -h, --help                    Show help without device access.

Examples:
  hue-jackman
` + `  hue-jackman rgb segments FF0000 0000FF
  hue-jackman rgb static FF00FF
  hue-jackman rgb breathe 00FF00 FF00FF
  hue-jackman rgb spectrum
  hue-jackman rgb off
  hue-jackman brightness 128

Run "hue-jackman <command> --help" for values and options.
`

const rgbHelp = `Usage: hue-jackman rgb <MODE> [RGB...] [options]

Run "hue-jackman" without arguments to inspect connected controllers.
RGB is six hexadecimal digits (optional leading #), for example FF0000 for red.

Modes:
  static <RGB>              Set both light strips to one solid colour
  segments <LEFT> <RIGHT>   Set left (col 0) and right (col 1) strips independently
  breathe <RGB> [RGB2]      Set 1-colour or 2-colour breathing (alias: breathing)
  spectrum                  Enable full-spectrum colour cycling
  off                       Turn Chroma lighting off

Options:
  --led BYTE                LED zone ID byte (default: 0x00, decimal or 0x hex).
  --varstore BYTE           Storage or profile byte (default: 0x01).
  --device N|PATH           Select device N or a /dev/bus/usb path.
  --timeout DURATION        USB transfer timeout (default: 1s).
  --dry-run                 Print encoded 64-byte frames without USB access.
  --keep-detached           Leave xpad detached on exit.
  --verbose                 Log raw USB TX/RX frames to stderr.
  -h, --help                Show help without device access.

Examples:
  hue-jackman rgb segments FF0000 0000FF
  hue-jackman rgb static FF00FF
  hue-jackman rgb breathe 00FF00 FF00FF
  hue-jackman rgb spectrum
  hue-jackman rgb off
`

const brightnessHelp = `Usage: hue-jackman brightness <0-255> [options]

Run "hue-jackman" without arguments to inspect connected controllers.
Set Chroma brightness from 0 (dark) to 255 (full) on led_id 0x05.

Options:
  --led BYTE                LED zone ID byte (default: 0x05, decimal or 0x hex).
  --varstore BYTE           Storage or profile byte (default: 0x01).
  --device N|PATH           Select device N or a /dev/bus/usb path.
  --timeout DURATION        USB transfer timeout (default: 1s).
  --dry-run                 Print encoded 64-byte frames without USB access.
  --keep-detached           Leave xpad detached on exit.
  --verbose                 Log raw USB TX/RX frames to stderr.
  -h, --help                Show help without device access.

Examples:
  hue-jackman brightness 128
  hue-jackman brightness 255
`

const rawHelp = `Usage: hue-jackman raw <CLASS-HEX> <CMD-HEX> [ARG-HEX...] [options]

Run "hue-jackman" without arguments to inspect connected controllers.
Send an arbitrary 64-byte GIP 0x0f Razer vendor command over interface 0.
Do not send class 0x00 cmd 0x04 with payload 0x01 (controller reset/shutdown).

Options:
  --device N|PATH           Select device N or a /dev/bus/usb path.
  --timeout DURATION        USB transfer timeout (default: 1s).
  --dry-run                 Print encoded 64-byte frames without USB access.
  --keep-detached           Leave xpad detached on exit.
  --verbose                 Log raw USB TX/RX frames to stderr.
  -h, --help                Show help without device access.

Examples:
  hue-jackman raw 00 87 00000000 --dry-run
  hue-jackman raw 0f 04 0105ff --verbose
`

func printHumanHelp(out io.Writer, text string) error {
	_, err := io.WriteString(out, text)
	return err
}
