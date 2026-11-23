package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wimpysworld/hue-jackman/internal/protocol"
	"github.com/wimpysworld/hue-jackman/internal/usb"
)

const defaultProductName = "Razer Wolverine V2 Chroma"

// CLIError wraps an error with an explicit process exit code (2 for usage, 1 for runtime).
type CLIError struct {
	Err  error
	Code int
}

func (e *CLIError) Error() string { return e.Err.Error() }
func (e *CLIError) Unwrap() error { return e.Err }

func exitCode(err error) int {
	if cliErr, ok := errors.AsType[*CLIError](err); ok {
		return cliErr.Code
	}
	return 1
}

type exchanger interface {
	Exchange(protocol.Request) (protocol.Response, error)
	Close() error
}

type deviceAccess struct {
	discover func() ([]usb.DeviceInfo, error)
	open     func(usb.Options) (exchanger, error)
}

func liveDeviceAccess() deviceAccess {
	return deviceAccess{
		discover: func() ([]usb.DeviceInfo, error) {
			return usb.FindDevices("/sys", protocol.VendorID, protocol.ProductID)
		},
		open: func(opts usb.Options) (exchanger, error) {
			return usb.Open(opts)
		},
	}
}

type command struct {
	name         string
	help         bool
	deviceIndex  int
	devicePath   string
	seenDevice   bool
	timeout      time.Duration
	seenTimeout  bool
	dryRun       bool
	keepDetached bool
	verbose      bool

	led          uint8
	seenLED      bool
	varstore     uint8
	seenVarstore bool

	rgbMode    string
	colours    []protocol.RGB
	brightness uint8
	rawClass   uint8
	rawCmd     uint8
	rawPayload []byte
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		os.Exit(exitCode(err))
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	return runWithAccess(args, stdout, stderr, liveDeviceAccess())
}

func runWithAccess(args []string, stdout, stderr io.Writer, access deviceAccess) error {
	cmd, err := parseArgs(args)
	code := 2
	headerOut := stderr
	if err == nil {
		code = 1
		headerOut = stdout
	}

	h := newHuman(headerOut)
	h.header()
	if h.out.err != nil {
		return &CLIError{Err: h.out.err, Code: code}
	}

	if err == nil {
		if cmd.help {
			return printHumanHelp(stdout, helpTextFor(cmd.name))
		}
		err = cmd.execute(h, stderr, access)
	}

	if err != nil {
		writeCLIError(stderr, err)
		return &CLIError{Err: err, Code: code}
	}
	return nil
}

func helpTextFor(name string) string {
	switch name {
	case "rgb":
		return rgbHelp
	case "brightness":
		return brightnessHelp
	case "raw":
		return rawHelp
	default:
		return publicHelp
	}
}

func writeCLIError(stderr io.Writer, err error) {
	errH := newHuman(stderr)
	lines := strings.Split(err.Error(), "\n")
	if len(lines) == 1 {
		errH.line("31", "Error: "+lines[0]+".")
	} else {
		errH.line("31", "Error: "+lines[0])
		for _, line := range lines[1:] {
			fmt.Fprintln(errH.out, escapeHuman(line))
		}
	}
	fmt.Fprintln(stderr, `Run "hue-jackman --help" for commands.`)
}

func parseArgs(args []string) (command, error) {
	c := command{timeout: time.Second}
	var positional []string

	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		if arg == "--" {
			positional = append(positional, args...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			var err error
			args, err = c.parseOption(arg, args)
			if err != nil {
				return c, err
			}
			continue
		}
		if c.name == "" && len(positional) == 0 {
			c.name = arg
			continue
		}
		positional = append(positional, arg)
	}

	if c.name == "" {
		if c.help {
			return c, nil
		}
		if c.seenLED || c.seenVarstore {
			return c, errors.New("--led and --varstore require rgb or brightness")
		}
		return c, nil
	}

	return c.finalize(positional)
}

func (c *command) parseOption(arg string, rest []string) ([]string, error) {
	opt, val, hasEq := strings.Cut(arg, "=")
	switch opt {
	case "-h", "--help":
		if hasEq {
			return rest, fmt.Errorf("%s accepts no value", opt)
		}
		c.help = true
		return rest, nil
	case "--dry-run":
		if hasEq {
			return rest, errors.New("--dry-run accepts no value")
		}
		c.dryRun = true
		return rest, nil
	case "--keep-detached":
		if hasEq {
			return rest, errors.New("--keep-detached accepts no value")
		}
		c.keepDetached = true
		return rest, nil
	case "--verbose":
		if hasEq {
			return rest, errors.New("--verbose accepts no value")
		}
		c.verbose = true
		return rest, nil
	case "--device", "--timeout", "--led", "--varstore":
		var err error
		val, rest, err = consumeValue(opt, val, hasEq, rest)
		if err != nil {
			return rest, err
		}
		return rest, c.applyValueOption(opt, val)
	default:
		return rest, fmt.Errorf("unknown option %q", arg)
	}
}

func consumeValue(opt, val string, hasEq bool, rest []string) (string, []string, error) {
	if hasEq {
		if val == "" {
			return "", rest, fmt.Errorf("%s requires a value", opt)
		}
		return val, rest, nil
	}
	if len(rest) == 0 {
		return "", rest, fmt.Errorf("%s requires a value", opt)
	}
	return rest[0], rest[1:], nil
}

func (c *command) applyValueOption(opt, val string) error {
	switch opt {
	case "--device":
		if c.seenDevice {
			return errors.New("--device is accepted once")
		}
		if strings.HasPrefix(val, "/") {
			c.devicePath = val
			c.seenDevice = true
			return nil
		}
		n, err := parsePositiveInt(val)
		if err != nil {
			return errors.New("--device needs a positive device number or a /dev/bus/usb path")
		}
		c.deviceIndex = n
		c.seenDevice = true
		return nil
	case "--timeout":
		if c.seenTimeout {
			return errors.New("--timeout is accepted once")
		}
		d, err := time.ParseDuration(val)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid --timeout %q; use a positive Go duration such as 1s or 500ms", val)
		}
		c.timeout = d
		c.seenTimeout = true
		return nil
	case "--led":
		if c.seenLED {
			return errors.New("--led is accepted once")
		}
		v, err := parseUint8(val)
		if err != nil {
			return fmt.Errorf("invalid --led %q; expected 0..255 or 0x00..0xff", val)
		}
		c.led = v
		c.seenLED = true
		return nil
	case "--varstore":
		if c.seenVarstore {
			return errors.New("--varstore is accepted once")
		}
		v, err := parseUint8(val)
		if err != nil {
			return fmt.Errorf("invalid --varstore %q; expected 0..255 or 0x00..0xff", val)
		}
		c.varstore = v
		c.seenVarstore = true
		return nil
	default:
		return fmt.Errorf("unknown option %q", opt)
	}
}

func (c command) finalize(positional []string) (command, error) {
	switch c.name {
	case "info":
		if c.help {
			c.name = ""
			return c, nil
		}
		if c.seenLED || c.seenVarstore {
			return c, errors.New("--led and --varstore require rgb or brightness")
		}
		if len(positional) > 0 {
			return c, errors.New("info accepts no positional arguments")
		}
		c.name = ""
		return c, nil
	case "rgb":
		if c.help {
			return c, nil
		}
		return c.parseRGB(positional)
	case "brightness":
		if c.help {
			return c, nil
		}
		return c.parseBrightness(positional)
	case "raw":
		if c.help {
			return c, nil
		}
		if c.seenLED || c.seenVarstore {
			return c, errors.New("--led and --varstore are supported on rgb and brightness only")
		}
		return c.parseRaw(positional)
	case "static", "segments", "breathe", "breathing", "spectrum", "off":
		return c, fmt.Errorf("unknown command %q; use \"hue-jackman rgb %s ...\"", c.name, c.name)
	default:
		return c, fmt.Errorf("unknown command %q; use rgb, brightness or raw", c.name)
	}
}

func (c command) parseRGB(positional []string) (command, error) {
	if len(positional) == 0 {
		return c, errors.New(`rgb requires a lighting mode, for example "hue-jackman rgb static FF00FF" or "hue-jackman rgb segments FF0000 0000FF"; run "hue-jackman rgb --help" for modes`)
	}
	mode := strings.ToLower(positional[0])
	if mode == "breathing" {
		mode = "breathe"
	}
	rest := positional[1:]
	c.rgbMode = mode

	switch mode {
	case "off", "spectrum":
		if len(rest) > 0 {
			return c, fmt.Errorf("rgb %s accepts no colours", mode)
		}
		return c, nil
	case "static":
		if len(rest) != 1 {
			return c, errors.New(`rgb static requires one colour (RRGGBB), for example "hue-jackman rgb static FF00FF"`)
		}
		col, err := parseColourArg(rest[0])
		if err != nil {
			return c, err
		}
		c.colours = []protocol.RGB{col}
		return c, nil
	case "segments":
		if c.seenLED || c.seenVarstore {
			return c, errors.New("rgb segments does not accept --led or --varstore")
		}
		if len(rest) != 2 {
			return c, errors.New(`rgb segments requires two colours (<LEFT> <RIGHT>), for example "hue-jackman rgb segments FF0000 0000FF"`)
		}
		left, err := parseColourArg(rest[0])
		if err != nil {
			return c, fmt.Errorf("left %w", err)
		}
		right, err := parseColourArg(rest[1])
		if err != nil {
			return c, fmt.Errorf("right %w", err)
		}
		c.colours = []protocol.RGB{left, right}
		return c, nil
	case "breathe":
		if len(rest) < 1 || len(rest) > 2 {
			return c, errors.New(`rgb breathe requires one or two colours (RRGGBB [RRGGBB2]), for example "hue-jackman rgb breathe 00FF00 FF00FF"`)
		}
		for _, raw := range rest {
			col, err := parseColourArg(raw)
			if err != nil {
				return c, err
			}
			c.colours = append(c.colours, col)
		}
		return c, nil
	default:
		return c, fmt.Errorf("unknown RGB mode %q; use static, segments, breathe, spectrum or off", positional[0])
	}
}

func (c command) parseBrightness(positional []string) (command, error) {
	if len(positional) != 1 {
		return c, errors.New(`brightness requires a level from 0 to 255, for example "hue-jackman brightness 128"`)
	}
	level, err := parseUint8(positional[0])
	if err != nil {
		return c, fmt.Errorf("invalid brightness %q; expected an integer from 0 to 255", positional[0])
	}
	c.brightness = level
	return c, nil
}

func (c command) parseRaw(positional []string) (command, error) {
	if len(positional) < 2 {
		return c, errors.New(`raw requires <class-hex> <cmd-hex> [arg-hex...], for example "hue-jackman raw 00 87 00000000"`)
	}
	cls, err := parseHexByte(positional[0])
	if err != nil {
		return c, fmt.Errorf("invalid class %q; expected one hex byte", positional[0])
	}
	cmdID, err := parseHexByte(positional[1])
	if err != nil {
		return c, fmt.Errorf("invalid command %q; expected one hex byte", positional[1])
	}
	var payload []byte
	for _, a := range positional[2:] {
		cleaned := strings.TrimPrefix(strings.TrimSpace(a), "0x")
		b, err := hex.DecodeString(cleaned)
		if err != nil {
			return c, fmt.Errorf("invalid hex argument %q", a)
		}
		payload = append(payload, b...)
	}
	if len(payload) > protocol.MaxArgSize {
		return c, fmt.Errorf("raw payload length %d exceeds maximum %d bytes", len(payload), protocol.MaxArgSize)
	}
	c.rawClass = cls
	c.rawCmd = cmdID
	c.rawPayload = payload
	return c, nil
}

func (c command) target(def protocol.Target) protocol.Target {
	t := def
	if c.seenVarstore {
		t.Varstore = c.varstore
	}
	if c.seenLED {
		t.LEDID = c.led
	}
	return t
}

func (c command) execute(h human, stderr io.Writer, access deviceAccess) error {
	if c.name == "" {
		return c.runInspect(h, stderr, access)
	}
	plan, views, doneMsg, err := c.buildPlan()
	if err != nil {
		return err
	}
	if c.dryRun {
		return c.runDryRun(h, plan, views)
	}
	selected, deviceID, err := c.selectDevice(access)
	if err != nil {
		return err
	}
	return c.runWrite(h, stderr, access, selected, deviceID, plan, views, doneMsg)
}

type frameStep struct {
	label string
	req   protocol.Request
}

func (c command) buildPlan() ([]frameStep, []changeView, string, error) {
	switch c.name {
	case "rgb":
		return c.buildRGBPlan()
	case "brightness":
		t := c.target(protocol.DefaultBrightnessTarget())
		pct := (int(c.brightness) * 100) / 255
		steps := []frameStep{{
			label: fmt.Sprintf("brightness %d", c.brightness),
			req:   protocol.SetBrightness(t, c.brightness),
		}}
		views := []changeView{{
			Setting: "brightness",
			Value:   fmt.Sprintf("%d/255 (%d%%)", c.brightness, pct),
		}}
		return steps, views, "Brightness saved.", nil
	case "raw":
		req := protocol.Request{
			CommandClass: c.rawClass,
			CommandID:    c.rawCmd,
			Args:         c.rawPayload,
		}
		steps := []frameStep{{
			label: fmt.Sprintf("raw 0x%02x 0x%02x", c.rawClass, c.rawCmd),
			req:   req,
		}}
		views := []changeView{{
			Setting: "command",
			Value:   fmt.Sprintf("class=0x%02x cmd=0x%02x (%d arg bytes)", c.rawClass, c.rawCmd, len(c.rawPayload)),
		}}
		return steps, views, "Command sent.", nil
	default:
		return nil, nil, "", fmt.Errorf("unknown command %q", c.name)
	}
}

func (c command) buildRGBPlan() ([]frameStep, []changeView, string, error) {
	t := c.target(protocol.DefaultEffectTarget())
	switch c.rgbMode {
	case "off":
		return []frameStep{{label: "off", req: protocol.Off(t)}},
			[]changeView{{Setting: "mode", Value: "off"}},
			"Lighting updated.", nil
	case "spectrum":
		return []frameStep{{label: "spectrum", req: protocol.Spectrum(t)}},
			[]changeView{{Setting: "mode", Value: "spectrum"}},
			"Lighting updated.", nil
	case "static":
		col := c.colours[0]
		return []frameStep{{label: "static " + col.String(), req: protocol.Static(t, col)}},
			[]changeView{
				{Setting: "mode", Value: "static"},
				{Setting: "colour", Value: col.String(), Swatch: true},
			},
			"Lighting updated.", nil
	case "segments":
		left, right := c.colours[0], c.colours[1]
		rowReq, err := protocol.SetCustomFrameRow(0, 0, []protocol.RGB{left, right})
		if err != nil {
			return nil, nil, "", err
		}
		steps := []frameStep{
			{label: "custom-frame mode", req: protocol.CustomFrameMode()},
			{label: "segments " + left.String() + " " + right.String(), req: rowReq},
		}
		views := []changeView{
			{Setting: "mode", Value: "segments"},
			{Setting: "left", Value: left.String(), Swatch: true},
			{Setting: "right", Value: right.String(), Swatch: true},
		}
		return steps, views, "Lighting updated.", nil
	case "breathe":
		if len(c.colours) == 1 {
			c1 := c.colours[0]
			return []frameStep{{label: "breathe " + c1.String(), req: protocol.BreathingSingle(t, c1)}},
				[]changeView{
					{Setting: "mode", Value: "breathe"},
					{Setting: "colour", Value: c1.String(), Swatch: true},
				},
				"Lighting updated.", nil
		}
		c1, c2 := c.colours[0], c.colours[1]
		return []frameStep{{label: "breathe " + c1.String() + " " + c2.String(), req: protocol.BreathingDual(t, c1, c2)}},
			[]changeView{
				{Setting: "mode", Value: "breathe"},
				{Setting: "colour 1", Value: c1.String(), Swatch: true},
				{Setting: "colour 2", Value: c2.String(), Swatch: true},
			},
			"Lighting updated.", nil
	default:
		return nil, nil, "", fmt.Errorf("unknown RGB mode %q", c.rgbMode)
	}
}

func (c command) runDryRun(h human, steps []frameStep, views []changeView) error {
	h.heading("Dry run (no USB access)")
	h.changes(views)
	var seq uint8
	for i, step := range steps {
		seq++
		frame, err := protocol.EncodeRequest(seq, step.req)
		if err != nil {
			return err
		}
		h.field(fmt.Sprintf("TX %d", i+1), fmt.Sprintf("%s: %x", step.label, frame))
	}
	h.line("", "No USB frames sent.")
	return h.out.err
}

func (c command) selectDevice(access deviceAccess) (usb.DeviceInfo, int, error) {
	if c.devicePath != "" {
		return usb.DeviceInfo{
			DevicePath: c.devicePath,
			VendorID:   protocol.VendorID,
			ProductID:  protocol.ProductID,
			Product:    defaultProductName,
		}, 1, nil
	}
	matches, err := access.discover()
	if err != nil {
		return usb.DeviceInfo{}, 0, err
	}
	if len(matches) == 0 {
		return usb.DeviceInfo{}, 0, fmt.Errorf("no compatible %s (%04x:%04x) found", defaultProductName, protocol.VendorID, protocol.ProductID)
	}
	if c.deviceIndex == 0 {
		if len(matches) == 1 {
			return matches[0], 1, nil
		}
		var b strings.Builder
		b.WriteString("multiple compatible controllers connected; specify --device <N>:")
		for i, m := range matches {
			fmt.Fprintf(&b, "\n  --device %d  %s (USB path %s)", i+1, productTitle(m), m.PhysicalPath)
		}
		return usb.DeviceInfo{}, 0, errors.New(b.String())
	}
	if c.deviceIndex > len(matches) {
		return usb.DeviceInfo{}, 0, fmt.Errorf("device %d not found (%d compatible controller(s) connected)", c.deviceIndex, len(matches))
	}
	return matches[c.deviceIndex-1], c.deviceIndex, nil
}

func (c command) runInspect(h human, stderr io.Writer, access deviceAccess) error {
	if c.dryRun {
		steps := []frameStep{
			{label: "firmware", req: protocol.GetFirmwareVersion()},
			{label: "serial", req: protocol.GetSerial()},
			{label: "name chunk 0", req: protocol.GetDeviceNameChunk(0x0000)},
			{label: "name chunk 1", req: protocol.GetDeviceNameChunk(0x0028)},
			{label: "active profile", req: protocol.GetActiveProfile()},
		}
		return c.runDryRun(h, steps, nil)
	}

	var targets []usb.DeviceInfo
	startID := 1
	if c.seenDevice {
		selected, id, err := c.selectDevice(access)
		if err != nil {
			return err
		}
		targets = []usb.DeviceInfo{selected}
		startID = id
	} else {
		var err error
		targets, err = access.discover()
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return fmt.Errorf("no compatible %s (%04x:%04x) found", defaultProductName, protocol.VendorID, protocol.ProductID)
		}
	}

	var runErr error
	for i, info := range targets {
		if i > 0 {
			_, _ = io.WriteString(h.out, "\n")
		}
		runErr = errors.Join(runErr, c.inspectOne(h, stderr, access, info, startID+i))
	}
	return errors.Join(runErr, h.out.err)
}

func (c command) inspectOne(h human, stderr io.Writer, access deviceAccess, info usb.DeviceInfo, deviceID int) error {
	dev, err := c.openDevice(info, stderr, access)
	if err != nil {
		renderDeviceHeading(h, info, deviceID, "")
		return err
	}
	defer func() { _ = dev.Close() }()

	fwResp, err := dev.Exchange(protocol.GetFirmwareVersion())
	if err != nil {
		return fmt.Errorf("firmware query: %w", err)
	}
	serResp, err := dev.Exchange(protocol.GetSerial())
	if err != nil {
		return fmt.Errorf("serial query: %w", err)
	}
	name0Resp, err := dev.Exchange(protocol.GetDeviceNameChunk(0x0000))
	if err != nil {
		return fmt.Errorf("device name chunk 0: %w", err)
	}
	name1Resp, err := dev.Exchange(protocol.GetDeviceNameChunk(0x0028))
	if err != nil {
		return fmt.Errorf("device name chunk 1: %w", err)
	}
	profResp, err := dev.Exchange(protocol.GetActiveProfile())
	if err != nil {
		return fmt.Errorf("active profile query: %w", err)
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

	renderDeviceHeading(h, info, deviceID, name)
	h.field("Firmware", fw)
	h.field("Serial", serial)
	h.field("Profile", strconv.Itoa(int(prof)))
	h.field("Supports", "rgb (static, segments, breathe, spectrum, off), brightness, raw")
	return h.out.err
}

func (c command) runWrite(h human, stderr io.Writer, access deviceAccess, info usb.DeviceInfo, deviceID int, steps []frameStep, views []changeView, doneMsg string) error {
	renderDeviceHeading(h, info, deviceID, "")
	h.changes(views)
	if h.out.err != nil {
		return h.out.err
	}

	dev, err := c.openDevice(info, stderr, access)
	if err != nil {
		return err
	}
	defer func() { _ = dev.Close() }()

	var lastResp protocol.Response
	for _, step := range steps {
		resp, err := dev.Exchange(step.req)
		if err != nil {
			return fmt.Errorf("%s: %w", step.label, err)
		}
		if !resp.OK() {
			return fmt.Errorf("%s failed with status %s", step.label, protocol.StatusName(resp.Status))
		}
		lastResp = resp
	}

	if c.name == "raw" {
		h.field("Status", protocol.StatusName(lastResp.Status))
		h.field("Reply", hex.EncodeToString(lastResp.Args))
	}
	h.line("32", doneMsg)
	return h.out.err
}

func (c command) openDevice(info usb.DeviceInfo, stderr io.Writer, access deviceAccess) (exchanger, error) {
	var dbg io.Writer
	if c.verbose {
		dbg = stderr
	}
	return access.open(usb.Options{
		DevicePath:   info.DevicePath,
		Timeout:      c.timeout,
		KeepDetached: c.keepDetached,
		DebugWriter:  dbg,
	})
}

func renderDeviceHeading(h human, info usb.DeviceInfo, deviceID int, queriedName string) {
	title := queriedName
	if title == "" {
		title = productTitle(info)
	}
	vid, pid := info.VendorID, info.ProductID
	if vid == 0 && pid == 0 {
		vid, pid = protocol.VendorID, protocol.ProductID
	}
	h.heading(fmt.Sprintf("[%d] %s (%04x:%04x)", deviceID, title, vid, pid))
	if info.PhysicalPath != "" {
		h.field("USB path", info.PhysicalPath)
	}
	if info.DevicePath != "" {
		h.field("Device", info.DevicePath)
	}
}

func productTitle(info usb.DeviceInfo) string {
	if info.Product != "" {
		return info.Product
	}
	return defaultProductName
}

func parseColourArg(raw string) (protocol.RGB, error) {
	col, err := protocol.ParseRGB(raw)
	if err != nil {
		return protocol.RGB{}, fmt.Errorf("colour %q is invalid; expected six hexadecimal digits (RRGGBB)", raw)
	}
	return col, nil
}

func parsePositiveInt(value string) (int, error) {
	if len(value) == 0 || value[0] < '1' || value[0] > '9' {
		return 0, errors.New("not a positive integer")
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, errors.New("not a positive integer")
		}
	}
	return strconv.Atoi(value)
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
