package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/wimpysworld/hue-jackman/internal/protocol"
	"github.com/wimpysworld/hue-jackman/internal/usb"
)

type fakeExchanger struct {
	sent      []protocol.Request
	responses map[uint16]protocol.Response
	closed    bool
	err       error
}

func (f *fakeExchanger) Exchange(req protocol.Request) (protocol.Response, error) {
	f.sent = append(f.sent, req)
	if f.err != nil {
		return protocol.Response{}, f.err
	}
	key := uint16(req.CommandClass)<<8 | uint16(req.CommandID)
	if resp, ok := f.responses[key]; ok {
		return resp, nil
	}
	return protocol.Response{Status: protocol.StatusSuccess}, nil
}

func (f *fakeExchanger) Close() error {
	f.closed = true
	return nil
}

func utf16Bytes(s string) []byte {
	enc := utf16.Encode([]rune(s))
	out := make([]byte, len(enc)*2)
	for i, u := range enc {
		binary.LittleEndian.PutUint16(out[i*2:], u)
	}
	return out
}

func sampleInfoExchanger() *fakeExchanger {
	nameRaw := utf16Bytes("Razer Wolverine V2 Chroma")
	nameArgs := append([]byte{0x00, 0x00, 0x00, 0x00, uint8(len(nameRaw) & 0xff)}, nameRaw...) //nolint:gosec // test fixture name is short
	return &fakeExchanger{
		responses: map[uint16]protocol.Response{
			uint16(protocol.ClassDevice)<<8 | uint16(protocol.CmdGetFirmware): {
				Status: protocol.StatusSuccess,
				Args:   []byte{1, 1, 1, 0},
			},
			uint16(protocol.ClassDevice)<<8 | uint16(protocol.CmdGetSerial): {
				Status: protocol.StatusSuccess,
				Args:   []byte("PM2212F12345678\x00\x00"),
			},
			uint16(protocol.ClassDevice)<<8 | uint16(protocol.CmdGetDeviceName): {
				Status: protocol.StatusSuccess,
				Args:   nameArgs,
			},
			uint16(protocol.ClassProfile)<<8 | uint16(protocol.CmdGetActiveProf): {
				Status: protocol.StatusSuccess,
				Args:   []byte{1},
			},
		},
	}
}

func TestHelpCommandsAvoidDeviceAccess(t *testing.T) {
	t.Parallel()

	called := false
	access := deviceAccess{
		discover: func() ([]usb.DeviceInfo, error) {
			called = true
			return nil, errors.New("should not discover on help")
		},
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "Usage: hue-jackman [command] [options]"},
		{[]string{"-h"}, "Without a command, inspect all connected supported controllers."},
		{[]string{"rgb", "--help"}, "Usage: hue-jackman rgb <MODE> [RGB...] [options]"},
		{[]string{"brightness", "-h"}, "Usage: hue-jackman brightness <0-255> [options]"},
		{[]string{"raw", "--help"}, "Usage: hue-jackman raw <CLASS-HEX> <CMD-HEX> [ARG-HEX...] [options]"},
	} {
		var stdout, stderr bytes.Buffer
		if err := runWithAccess(tc.args, &stdout, &stderr, access); err != nil {
			t.Fatalf("args %v failed: %v", tc.args, err)
		}
		if called {
			t.Fatalf("args %v touched USB discovery", tc.args)
		}
		if !strings.Contains(stdout.String(), "🎮 hue-jackman\n\n") || !strings.Contains(stdout.String(), tc.want) {
			t.Fatalf("args %v stdout=%q, want %q", tc.args, stdout.String(), tc.want)
		}
		if stderr.Len() != 0 {
			t.Fatalf("args %v unexpected stderr=%q", tc.args, stderr.String())
		}
	}
}

func TestZeroArgInspectAndInfoAlias(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"info"}} {
		fx := sampleInfoExchanger()
		access := deviceAccess{
			discover: func() ([]usb.DeviceInfo, error) {
				return []usb.DeviceInfo{{
					PhysicalPath: "3-2.1",
					DevicePath:   "/dev/bus/usb/003/005",
					VendorID:     protocol.VendorID,
					ProductID:    protocol.ProductID,
					Product:      defaultProductName,
				}}, nil
			},
			open: func(usb.Options) (exchanger, error) { return fx, nil },
		}

		var stdout, stderr bytes.Buffer
		if err := runWithAccess(args, &stdout, &stderr, access); err != nil {
			t.Fatalf("args %v failed: %v", args, err)
		}
		out := stdout.String()
		for _, want := range []string{
			"🎮 hue-jackman",
			"[1] Razer Wolverine V2 Chroma (1532:0a2e)",
			"USB path   3-2.1",
			"Device     /dev/bus/usb/003/005",
			"Firmware   1.1.1.0",
			"Serial     PM2212F12345678",
			"Profile    1",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("args %v stdout missing %q:\n%s", args, want, out)
			}
		}
		if !fx.closed {
			t.Errorf("args %v did not close device", args)
		}
	}
}

func TestMultipleDevicesRequireSelectionOnWrite(t *testing.T) {
	t.Parallel()

	devices := []usb.DeviceInfo{
		{PhysicalPath: "1-2", DevicePath: "/dev/bus/usb/001/002", VendorID: protocol.VendorID, ProductID: protocol.ProductID, Product: defaultProductName},
		{PhysicalPath: "1-4", DevicePath: "/dev/bus/usb/001/004", VendorID: protocol.VendorID, ProductID: protocol.ProductID, Product: defaultProductName},
	}

	var openedPath string
	access := deviceAccess{
		discover: func() ([]usb.DeviceInfo, error) { return devices, nil },
		open: func(opts usb.Options) (exchanger, error) {
			openedPath = opts.DevicePath
			return &fakeExchanger{}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := runWithAccess([]string{"rgb", "static", "FF00FF"}, &stdout, &stderr, access)
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("expected runtime error (exit 1), got err=%v code=%d", err, exitCode(err))
	}
	if !strings.Contains(stderr.String(), "--device 1") || !strings.Contains(stderr.String(), "--device 2") {
		t.Fatalf("stderr missing device selection guidance:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := runWithAccess([]string{"rgb", "static", "FF00FF", "--device", "2"}, &stdout, &stderr, access); err != nil {
		t.Fatalf("selected --device 2 failed: %v", err)
	}
	if openedPath != "/dev/bus/usb/001/004" {
		t.Fatalf("opened %q, want /dev/bus/usb/001/004", openedPath)
	}
	if !strings.Contains(stdout.String(), "[2] Razer Wolverine V2 Chroma") || !strings.Contains(stdout.String(), "Lighting updated.") {
		t.Fatalf("unexpected stdout:\n%s", stdout.String())
	}
}

func TestWriteCommandsAndTrailingFlags(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		args     []string
		wantTx   int
		wantText []string
	}{
		{
			name:     "segments",
			args:     []string{"rgb", "segments", "FF0000", "0000FF"},
			wantTx:   2,
			wantText: []string{"mode: segments", "left: #FF0000", "right: #0000FF", "Lighting updated."},
		},
		{
			name:     "static",
			args:     []string{"rgb", "static", "#00ff88"},
			wantTx:   1,
			wantText: []string{"mode: static", "colour: #00FF88", "Lighting updated."},
		},
		{
			name:     "breathe dual alias",
			args:     []string{"rgb", "breathing", "00FF00", "FF00FF"},
			wantTx:   1,
			wantText: []string{"mode: breathe", "colour 1: #00FF00", "colour 2: #FF00FF", "Lighting updated."},
		},
		{
			name:     "spectrum",
			args:     []string{"rgb", "spectrum"},
			wantTx:   1,
			wantText: []string{"mode: spectrum", "Lighting updated."},
		},
		{
			name:     "off",
			args:     []string{"rgb", "off"},
			wantTx:   1,
			wantText: []string{"mode: off", "Lighting updated."},
		},
		{
			name:     "brightness",
			args:     []string{"brightness", "128"},
			wantTx:   1,
			wantText: []string{"brightness: 128/255 (50%)", "Brightness saved."},
		},
		{
			name:     "raw",
			args:     []string{"raw", "0f", "04", "0105ff"},
			wantTx:   1,
			wantText: []string{"command: class=0x0f cmd=0x04", "Status     success", "Command sent."},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := &fakeExchanger{}
			access := deviceAccess{
				discover: func() ([]usb.DeviceInfo, error) {
					return []usb.DeviceInfo{{
						PhysicalPath: "1-2",
						DevicePath:   "/dev/bus/usb/001/002",
						VendorID:     protocol.VendorID,
						ProductID:    protocol.ProductID,
					}}, nil
				},
				open: func(usb.Options) (exchanger, error) { return fx, nil },
			}

			var stdout, stderr bytes.Buffer
			if err := runWithAccess(tc.args, &stdout, &stderr, access); err != nil {
				t.Fatalf("args %v failed: %v (stderr=%s)", tc.args, err, stderr.String())
			}
			if len(fx.sent) != tc.wantTx {
				t.Fatalf("sent %d frames, want %d", len(fx.sent), tc.wantTx)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout missing %q:\n%s", want, stdout.String())
				}
			}
		})
	}
}

func TestDryRunWorksBeforeOrAfterCommand(t *testing.T) {
	t.Parallel()

	access := deviceAccess{
		discover: func() ([]usb.DeviceInfo, error) {
			return nil, errors.New("dry-run must not discover USB devices")
		},
	}

	for _, args := range [][]string{
		{"--dry-run", "rgb", "segments", "FF0000", "0000FF"},
		{"rgb", "segments", "FF0000", "0000FF", "--dry-run"},
		{"--dry-run"},
	} {
		var stdout, stderr bytes.Buffer
		if err := runWithAccess(args, &stdout, &stderr, access); err != nil {
			t.Fatalf("args %v failed: %v", args, err)
		}
		if !strings.Contains(stdout.String(), "Dry run (no USB access)") || !strings.Contains(stdout.String(), "No USB frames sent.") {
			t.Fatalf("args %v unexpected stdout:\n%s", args, stdout.String())
		}
	}
}

func TestParseErrorsReturnExitCode2(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"bogus"}, `unknown command "bogus"`},
		{[]string{"static", "FF00FF"}, `use "hue-jackman rgb static ..."`},
		{[]string{"rgb"}, "rgb requires a lighting mode"},
		{[]string{"rgb", "static", "ZZZZZZ"}, "expected six hexadecimal digits"},
		{[]string{"rgb", "segments", "FF0000"}, "rgb segments requires two colours"},
		{[]string{"rgb", "off", "FF0000"}, "rgb off accepts no colours"},
		{[]string{"brightness", "300"}, "invalid brightness"},
		{[]string{"--device", "0", "rgb", "off"}, "--device needs a positive device number"},
	} {
		var stdout, stderr bytes.Buffer
		err := runWithAccess(tc.args, &stdout, &stderr, deviceAccess{})
		if err == nil || exitCode(err) != 2 {
			t.Fatalf("args %v: got err=%v code=%d, want exit 2", tc.args, err, exitCode(err))
		}
		if !strings.Contains(stderr.String(), tc.want) || !strings.Contains(stderr.String(), `Run "hue-jackman --help" for commands.`) {
			t.Fatalf("args %v stderr=%q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestHumanColourPolicyAndSwatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, term, noColour, capability string
		tty, colour, rgb                 bool
	}{
		{"truecolour", "xterm-256color", "", "truecolor", true, true, true},
		{"24bit", "xterm", "", "24bit", true, true, true},
		{"ansi", "xterm", "", "", true, true, false},
		{"redirected", "xterm", "", "truecolor", false, false, false},
		{"no-colour", "xterm", "1", "truecolor", true, false, false},
		{"dumb", "dumb", "", "truecolor", true, false, false},
		{"unset-term", "", "", "truecolor", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			env := map[string]string{"TERM": tc.term, "NO_COLOR": tc.noColour, "COLORTERM": tc.capability}
			h := humanFor(&out, tc.tty, func(k string) string { return env[k] })
			h.header()
			want := "🎮 hue-jackman\n\n"
			if tc.colour {
				want = "\x1b[34m🎮 hue-jackman\x1b[0m\n\n"
			}
			if out.String() != want {
				t.Fatalf("header=%q, want %q", out.String(), want)
			}
			h.changes([]changeView{{Setting: "left", Value: "#1234EF", Swatch: true}})
			got := out.String()
			if strings.Contains(got, "\x1b[") != tc.colour || strings.Contains(got, "\x1b[48;2;18;52;239m") != tc.rgb || !strings.Contains(got, "#1234EF") {
				t.Fatalf("unexpected output: %q", got)
			}
		})
	}
}

func TestHumanEscapingAndNarrowLayout(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	h := newHuman(&out)
	h.field("Serial", "bad\x1b[31m\nline\r\t\u202e")
	got := out.String()
	if strings.ContainsAny(got, "\x1b\r\t\u202e") || strings.Contains(got, "\nline") || !strings.Contains(got, `\x1b`) {
		t.Fatalf("unsafe escaping: %q", got)
	}

	out.Reset()
	h.width = 40
	h.field("Device", "/dev/bus/usb/003/005")
	if !strings.Contains(out.String(), "  Device\n    /dev/bus/usb/003/005\n") {
		t.Fatalf("expected stacked narrow field, got %q", out.String())
	}
}

type failingWriter struct {
	short  bool
	failed bool
	calls  int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.calls++
	if !w.failed {
		w.failed = true
		if w.short {
			return len(p) - 1, nil
		}
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestHumanRetainsOutputFailure(t *testing.T) {
	t.Parallel()

	for _, short := range []bool{false, true} {
		fw := &failingWriter{short: short}
		h := newHuman(fw)
		h.heading("Heading")
		h.field("Device", "/dev/bus/usb/001/002")
		want := io.ErrClosedPipe
		if short {
			want = io.ErrShortWrite
		}
		if !errors.Is(h.out.err, want) || fw.calls != 1 {
			t.Fatalf("err=%v calls=%d, want %v and 1 call", h.out.err, fw.calls, want)
		}
	}
}
