package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wimpysworld/hue-jackman/internal/protocol"
	"github.com/wimpysworld/hue-jackman/internal/usb"
)

type globalConfig struct {
	devicePath   string
	timeout      time.Duration
	dryRun       bool
	keepDetached bool
	verbose      bool
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hue-jackman", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cfg globalConfig
	fs.StringVar(&cfg.devicePath, "device", "", "USB device node path (default: auto-discover 1532:0a2e via /sys)")
	fs.DurationVar(&cfg.timeout, "timeout", time.Second, "USB transfer timeout")
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "print encoded 64-byte frames without opening USB")
	fs.BoolVar(&cfg.keepDetached, "keep-detached", false, "leave xpad detached on exit (default reattaches)")
	fs.BoolVar(&cfg.verbose, "verbose", false, "log raw USB TX/RX frames to stderr")
	fs.Usage = func() {
		printUsage(stderr)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		printUsage(stderr)
		return errors.New("missing subcommand")
	}

	sub := fs.Arg(0)
	subArgs := fs.Args()[1:]

	switch sub {
	case "info":
		return runInfo(cfg, stdout, stderr)
	case "off":
		return runOff(cfg, subArgs, stdout, stderr)
	case "static":
		return runStatic(cfg, subArgs, stdout, stderr)
	case "segments":
		return runSegments(cfg, subArgs, stdout, stderr)
	case "breathing":
		return runBreathing(cfg, subArgs, stdout, stderr)
	case "spectrum":
		return runSpectrum(cfg, subArgs, stdout, stderr)
	case "brightness":
		return runBrightness(cfg, subArgs, stdout, stderr)
	case "raw":
		return runRaw(cfg, subArgs, stdout, stderr)
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", sub)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: hue-jackman [global flags] <command> [command flags] [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --device <path>     USB device path (default: auto-discover 1532:0a2e)")
	fmt.Fprintln(w, "  --timeout <dur>     USB transfer timeout (default: 1s)")
	fmt.Fprintln(w, "  --dry-run           Print 64-byte frames without opening USB")
	fmt.Fprintln(w, "  --keep-detached     Do not reattach kernel driver on exit")
	fmt.Fprintln(w, "  --verbose           Log raw TX/RX frames to stderr")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  info                              Read firmware, serial, device name, and active profile")
	fmt.Fprintln(w, "  off [--led 0x00] [--varstore 1]   Turn Chroma lighting off")
	fmt.Fprintln(w, "  static [--led 0x00] <RRGGBB>      Set both strips to one solid colour")
	fmt.Fprintln(w, "  segments <LEFT> <RIGHT>           Set left and right strips independently (RRGGBB)")
	fmt.Fprintln(w, "  breathing <RRGGBB> [RRGGBB2]      Set 1-colour or 2-colour breathing")
	fmt.Fprintln(w, "  spectrum [--led 0x00]             Set spectrum cycling")
	fmt.Fprintln(w, "  brightness [--led 0x05] <0-255>   Set brightness level")
	fmt.Fprintln(w, "  raw <class-hex> <cmd-hex> [hex..] Send arbitrary 0x0f vendor command")
}

type runner struct {
	cfg    globalConfig
	dev    *usb.Device
	seq    uint8
	stdout io.Writer
}

func openRunner(cfg globalConfig, stdout, stderr io.Writer) (*runner, func(), error) {
	r := &runner{cfg: cfg, stdout: stdout}
	if cfg.dryRun {
		return r, func() {}, nil
	}
	var dbg io.Writer
	if cfg.verbose {
		dbg = stderr
	}
	dev, err := usb.Open(usb.Options{
		DevicePath:   cfg.devicePath,
		Timeout:      cfg.timeout,
		KeepDetached: cfg.keepDetached,
		DebugWriter:  dbg,
	})
	if err != nil {
		return nil, nil, err
	}
	r.dev = dev
	return r, func() { _ = dev.Close() }, nil
}

func (r *runner) send(label string, req protocol.Request) (protocol.Response, error) {
	if r.cfg.dryRun {
		r.seq++
		if r.seq == 0 {
			r.seq = 1
		}
		frame, err := protocol.EncodeRequest(r.seq, req)
		if err != nil {
			return protocol.Response{}, err
		}
		fmt.Fprintf(r.stdout, "[dry-run] %s: %x\n", label, frame)
		return protocol.Response{Status: protocol.StatusSuccess}, nil
	}
	return r.dev.Exchange(req)
}

func runInfo(cfg globalConfig, stdout, stderr io.Writer) error {
	r, closeFn, err := openRunner(cfg, stdout, stderr)
	if err != nil {
		return err
	}
	defer closeFn()

	fwResp, err := r.send("firmware", protocol.GetFirmwareVersion())
	if err != nil {
		return fmt.Errorf("firmware query: %w", err)
	}
	serResp, err := r.send("serial", protocol.GetSerial())
	if err != nil {
		return fmt.Errorf("serial query: %w", err)
	}
	name0Resp, err := r.send("name chunk 0", protocol.GetDeviceNameChunk(0x0000))
	if err != nil {
		return fmt.Errorf("device name chunk 0: %w", err)
	}
	name1Resp, err := r.send("name chunk 1", protocol.GetDeviceNameChunk(0x0028))
	if err != nil {
		return fmt.Errorf("device name chunk 1: %w", err)
	}
	profResp, err := r.send("active profile", protocol.GetActiveProfile())
	if err != nil {
		return fmt.Errorf("active profile query: %w", err)
	}

	if cfg.dryRun {
		return nil
	}

	fw, err := protocol.ParseFirmwareVersion(fwResp)
	if err != nil {
		return err
	}
	serial, err := protocol.ParseSerial(serResp)
	if err != nil {
		return err
	}
	totalBytes, b0, err := protocol.ParseDeviceNameChunk(name0Resp)
	if err != nil {
		return err
	}
	_, b1, err := protocol.ParseDeviceNameChunk(name1Resp)
	if err != nil {
		return err
	}
	combined := append(append([]byte(nil), b0...), b1...)
	if int(totalBytes) < len(combined) {
		combined = combined[:totalBytes]
	}
	name := protocol.DecodeUTF16LE(combined)

	prof, err := protocol.ParseActiveProfile(profResp)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Device:   %s\n", name)
	fmt.Fprintf(stdout, "Firmware: %s\n", fw)
	fmt.Fprintf(stdout, "Serial:   %s\n", serial)
	fmt.Fprintf(stdout, "Profile:  %d\n", prof)
	return nil
}

func parseTargetFlags(name string, args []string, def protocol.Target, stderr io.Writer) (protocol.Target, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	varstoreStr := fs.String("varstore", fmt.Sprintf("0x%02x", def.Varstore), "storage/profile byte (decimal or 0x hex)")
	ledStr := fs.String("led", fmt.Sprintf("0x%02x", def.LEDID), "LED zone ID byte (decimal or 0x hex)")
	if err := fs.Parse(args); err != nil {
		return protocol.Target{}, nil, err
	}
	vs, err := parseUint8(*varstoreStr)
	if err != nil {
		return protocol.Target{}, nil, fmt.Errorf("invalid --varstore: %w", err)
	}
	led, err := parseUint8(*ledStr)
	if err != nil {
		return protocol.Target{}, nil, fmt.Errorf("invalid --led: %w", err)
	}
	return protocol.Target{Varstore: vs, LEDID: led}, fs.Args(), nil
}

func runOff(cfg globalConfig, args []string, stdout, stderr io.Writer) error {
	target, _, err := parseTargetFlags("off", args, protocol.DefaultEffectTarget(), stderr)
	if err != nil {
		return err
	}
	return sendSimple(cfg, "off", protocol.Off(target), stdout, stderr)
}

func runStatic(cfg globalConfig, args []string, stdout, stderr io.Writer) error {
	target, rest, err := parseTargetFlags("static", args, protocol.DefaultEffectTarget(), stderr)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("static requires one RGB colour (RRGGBB)")
	}
	rgb, err := protocol.ParseRGB(rest[0])
	if err != nil {
		return err
	}
	return sendSimple(cfg, "static "+rgb.String(), protocol.Static(target, rgb), stdout, stderr)
}

func runSegments(cfg globalConfig, args []string, stdout, stderr io.Writer) error {
	if len(args) != 2 {
		return errors.New("segments requires two RGB colours: <left-RRGGBB> <right-RRGGBB>")
	}
	left, err := protocol.ParseRGB(args[0])
	if err != nil {
		return fmt.Errorf("left colour: %w", err)
	}
	right, err := protocol.ParseRGB(args[1])
	if err != nil {
		return fmt.Errorf("right colour: %w", err)
	}

	rowReq, err := protocol.SetCustomFrameRow(0, 0, []protocol.RGB{left, right})
	if err != nil {
		return err
	}

	r, closeFn, err := openRunner(cfg, stdout, stderr)
	if err != nil {
		return err
	}
	defer closeFn()

	modeResp, err := r.send("custom-frame mode", protocol.CustomFrameMode())
	if err != nil {
		return fmt.Errorf("custom-frame mode: %w", err)
	}
	if !cfg.dryRun && !modeResp.OK() {
		return fmt.Errorf("custom-frame mode failed with status %s", protocol.StatusName(modeResp.Status))
	}

	frameResp, err := r.send("segments "+left.String()+" "+right.String(), rowReq)
	if err != nil {
		return fmt.Errorf("set segments: %w", err)
	}
	if cfg.dryRun {
		return nil
	}
	if !frameResp.OK() {
		return fmt.Errorf("set segments failed with status %s", protocol.StatusName(frameResp.Status))
	}
	fmt.Fprintf(stdout, "segments: left=%s right=%s status=%s\n", left, right, protocol.StatusName(frameResp.Status))
	return nil
}

func runBreathing(cfg globalConfig, args []string, stdout, stderr io.Writer) error {
	target, rest, err := parseTargetFlags("breathing", args, protocol.DefaultEffectTarget(), stderr)
	if err != nil {
		return err
	}
	switch len(rest) {
	case 1:
		c1, err := protocol.ParseRGB(rest[0])
		if err != nil {
			return err
		}
		return sendSimple(cfg, "breathing "+c1.String(), protocol.BreathingSingle(target, c1), stdout, stderr)
	case 2:
		c1, err := protocol.ParseRGB(rest[0])
		if err != nil {
			return err
		}
		c2, err := protocol.ParseRGB(rest[1])
		if err != nil {
			return err
		}
		return sendSimple(cfg, "breathing "+c1.String()+" "+c2.String(), protocol.BreathingDual(target, c1, c2), stdout, stderr)
	default:
		return errors.New("breathing requires one or two RGB colours (RRGGBB [RRGGBB2])")
	}
}

func runSpectrum(cfg globalConfig, args []string, stdout, stderr io.Writer) error {
	target, _, err := parseTargetFlags("spectrum", args, protocol.DefaultEffectTarget(), stderr)
	if err != nil {
		return err
	}
	return sendSimple(cfg, "spectrum", protocol.Spectrum(target), stdout, stderr)
}

func runBrightness(cfg globalConfig, args []string, stdout, stderr io.Writer) error {
	target, rest, err := parseTargetFlags("brightness", args, protocol.DefaultBrightnessTarget(), stderr)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("brightness requires a level (0-255)")
	}
	level, err := parseUint8(rest[0])
	if err != nil {
		return fmt.Errorf("invalid brightness level: %w", err)
	}
	return sendSimple(cfg, fmt.Sprintf("brightness %d", level), protocol.SetBrightness(target, level), stdout, stderr)
}

func runRaw(cfg globalConfig, args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return errors.New("raw requires <class-hex> <cmd-hex> [arg-hex...]")
	}
	cls, err := parseHexByte(args[0])
	if err != nil {
		return fmt.Errorf("invalid class: %w", err)
	}
	cmd, err := parseHexByte(args[1])
	if err != nil {
		return fmt.Errorf("invalid cmd: %w", err)
	}

	var payload []byte
	for _, a := range args[2:] {
		cleaned := strings.TrimPrefix(strings.TrimSpace(a), "0x")
		b, err := hex.DecodeString(cleaned)
		if err != nil {
			return fmt.Errorf("invalid hex arg %q: %w", a, err)
		}
		payload = append(payload, b...)
	}

	req := protocol.Request{CommandClass: cls, CommandID: cmd, Args: payload}
	return sendSimple(cfg, fmt.Sprintf("raw 0x%02x 0x%02x", cls, cmd), req, stdout, stderr)
}

func sendSimple(cfg globalConfig, label string, req protocol.Request, stdout, stderr io.Writer) error {
	r, closeFn, err := openRunner(cfg, stdout, stderr)
	if err != nil {
		return err
	}
	defer closeFn()

	resp, err := r.send(label, req)
	if err != nil {
		return err
	}
	if cfg.dryRun {
		return nil
	}
	fmt.Fprintf(stdout, "%s: status=%s args=%x\n", label, protocol.StatusName(resp.Status), resp.Args)
	if !resp.OK() {
		return fmt.Errorf("controller returned status %s", protocol.StatusName(resp.Status))
	}
	return nil
}

func parseUint8(s string) (uint8, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 0, 8)
	if err != nil {
		return 0, err
	}
	return uint8(v), nil
}

func parseHexByte(s string) (uint8, error) {
	cleaned := strings.TrimPrefix(strings.TrimSpace(s), "0x")
	v, err := strconv.ParseUint(cleaned, 16, 8)
	if err != nil {
		return 0, err
	}
	return uint8(v), nil
}
