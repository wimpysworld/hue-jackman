package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustFrame(t *testing.T, prefixHex string, crc byte) [FrameSize]byte {
	t.Helper()
	b, err := hex.DecodeString(prefixHex)
	if err != nil {
		t.Fatalf("bad hex %q: %v", prefixHex, err)
	}
	if len(b) > offsetCRC {
		t.Fatalf("prefix too long: %d", len(b))
	}
	var out [FrameSize]byte
	copy(out[:], b)
	out[offsetCRC] = crc
	return out
}

func TestGoldenCaptureRequests(t *testing.T) {
	t.Parallel()

	// Every expected byte sequence and CRC comes directly from OpenRazer issue #1982 capture (Bus 7 Dev 13).
	tests := []struct {
		name     string
		seq      uint8
		req      Request
		wantPref string
		wantCRC  byte
	}{
		{
			name:     "firmware version query (#75302)",
			seq:      0x01,
			req:      GetFirmwareVersion(),
			wantPref: "0f00013c001f00000004008700000000",
			wantCRC:  0x83,
		},
		{
			name:     "serial query (#75312)",
			seq:      0x03,
			req:      GetSerial(),
			wantPref: "0f00033c001f000000160082",
			wantCRC:  0x94,
		},
		{
			name:     "device name chunk 0 (#75334)",
			seq:      0x05,
			req:      GetDeviceNameChunk(0x0000),
			wantPref: "0f00053c001f0000002d00b400000000",
			wantCRC:  0x99,
		},
		{
			name:     "device name chunk 1 (#75348)",
			seq:      0x06,
			req:      GetDeviceNameChunk(0x0028),
			wantPref: "0f00063c001f0000002d00b400002800",
			wantCRC:  0xb1,
		},
		{
			name:     "active profile query (#75406)",
			seq:      0x0e,
			req:      GetActiveProfile(),
			wantPref: "0f000e3c001f00000001058400",
			wantCRC:  0x80,
		},
		{
			name:     "chroma static magenta (#80969)",
			seq:      0x12,
			req:      Static(DefaultEffectTarget(), RGB{0xff, 0x00, 0xff}),
			wantPref: "0f00123c001f000000090f02010001000101ff00ff",
			wantCRC:  0x04,
		},
		{
			name:     "chroma breathing 1-colour green (#96469)",
			seq:      0x42,
			req:      BreathingSingle(DefaultEffectTarget(), RGB{0x00, 0xff, 0x00}),
			wantPref: "0f00423c001f000000090f0201000200010100ff00",
			wantCRC:  0xf8,
		},
		{
			name:     "chroma breathing 2-colour green+magenta (#100493)",
			seq:      0x43,
			req:      BreathingDual(DefaultEffectTarget(), RGB{0x00, 0xff, 0x00}, RGB{0xff, 0x00, 0xff}),
			wantPref: "0f00433c001f0000000c0f0201000200010200ff00ff00ff",
			wantCRC:  0xfe,
		},
		{
			name:     "chroma spectrum cycling (#124421)",
			seq:      0x61,
			req:      Spectrum(DefaultEffectTarget()),
			wantPref: "0f00613c001f000000060f02010003000100",
			wantCRC:  0x08,
		},
		{
			name:     "chroma off (#131117)",
			seq:      0x7a,
			req:      Off(DefaultEffectTarget()),
			wantPref: "0f007a3c001f000000060f02010000000100",
			wantCRC:  0x0b,
		},
		{
			name:     "chroma brightness 0xff (#81223)",
			seq:      0x22,
			req:      SetBrightness(DefaultBrightnessTarget(), 0xff),
			wantPref: "0f00223c001f000000030f040105ff",
			wantCRC:  0xf3,
		},
		{
			name:     "chroma brightness 0x99 (#127417)",
			seq:      0x65,
			req:      SetBrightness(DefaultBrightnessTarget(), 0x99),
			wantPref: "0f00653c001f000000030f04010599",
			wantCRC:  0x95,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := EncodeRequest(tc.seq, tc.req)
			if err != nil {
				t.Fatalf("EncodeRequest failed: %v", err)
			}
			want := mustFrame(t, tc.wantPref, tc.wantCRC)
			if got != want {
				t.Fatalf("frame mismatch:\ngot:  %x\nwant: %x", got, want)
			}
		})
	}
}

func TestDecodeGoldenResponses(t *testing.T) {
	t.Parallel()

	t.Run("firmware version (#75304)", func(t *testing.T) {
		t.Parallel()
		raw := mustFrame(t, "1000013c021f00000004008701010100", 0x82)
		resp, err := DecodeResponse(raw[:])
		if err != nil {
			t.Fatalf("DecodeResponse failed: %v", err)
		}
		ver, err := ParseFirmwareVersion(resp)
		if err != nil {
			t.Fatalf("ParseFirmwareVersion failed: %v", err)
		}
		if ver != "1.1.1.0" {
			t.Fatalf("got firmware %q, want %q", ver, "1.1.1.0")
		}
	})

	t.Run("serial number (#75314)", func(t *testing.T) {
		t.Parallel()
		raw := mustFrame(t, "1000033c021f000000160082504d32323330433038323030353636", 0xc6)
		resp, err := DecodeResponse(raw[:])
		if err != nil {
			t.Fatalf("DecodeResponse failed: %v", err)
		}
		serial, err := ParseSerial(resp)
		if err != nil {
			t.Fatalf("ParseSerial failed: %v", err)
		}
		if serial != "PM2230C08200566" {
			t.Fatalf("got serial %q, want %q", serial, "PM2230C08200566")
		}
	})

	t.Run("device name chunks (#75338 + #75350)", func(t *testing.T) {
		t.Parallel()
		raw0 := mustFrame(t, "1000053c021f0000002d00b40000000032520061007a0065007200200057006f006c0076006500720069006e00650020005600320020004300", 0xa5)
		raw1 := mustFrame(t, "1000063c021f0000002d00b40000280032680072006f006d006100", 0xfa)

		resp0, err := DecodeResponse(raw0[:])
		if err != nil {
			t.Fatalf("DecodeResponse chunk 0 failed: %v", err)
		}
		resp1, err := DecodeResponse(raw1[:])
		if err != nil {
			t.Fatalf("DecodeResponse chunk 1 failed: %v", err)
		}
		total0, b0, err := ParseDeviceNameChunk(resp0)
		if err != nil {
			t.Fatalf("ParseDeviceNameChunk 0 failed: %v", err)
		}
		total1, b1, err := ParseDeviceNameChunk(resp1)
		if err != nil {
			t.Fatalf("ParseDeviceNameChunk 1 failed: %v", err)
		}
		if total0 != 0x32 || total1 != 0x32 {
			t.Fatalf("unexpected total byte lengths: %d, %d", total0, total1)
		}
		combined := append(append([]byte(nil), b0...), b1...)
		got := DecodeUTF16LE(combined[:total0])
		if got != "Razer Wolverine V2 Chroma" {
			t.Fatalf("got device name %q, want %q", got, "Razer Wolverine V2 Chroma")
		}
	})

	t.Run("bad checksum rejected", func(t *testing.T) {
		t.Parallel()
		raw := mustFrame(t, "1000013c021f00000004008701010100", 0x00)
		if _, err := DecodeResponse(raw[:]); err == nil {
			t.Fatal("expected checksum mismatch error, got nil")
		}
	})
}

func TestParseRGB(t *testing.T) {
	t.Parallel()

	c, err := ParseRGB("#ff00aa")
	if err != nil {
		t.Fatalf("ParseRGB failed: %v", err)
	}
	if c != (RGB{0xff, 0x00, 0xaa}) {
		t.Fatalf("got %+v, want {255 0 170}", c)
	}
	if _, err := ParseRGB("12345"); err == nil {
		t.Fatal("expected error for 5-char hex")
	}
}

func TestSetCustomFrameRow(t *testing.T) {
	t.Parallel()

	req, err := SetCustomFrameRow(0, 0, []RGB{{0xff, 0x00, 0x00}, {0x00, 0x00, 0xff}})
	if err != nil {
		t.Fatalf("SetCustomFrameRow failed: %v", err)
	}
	wantArgs := []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0xff, 0x00, 0x00, 0x00, 0x00, 0xff}
	if req.CommandClass != ClassExtendedMatrix || req.CommandID != CmdSetCustomFrame || !bytes.Equal(req.Args, wantArgs) {
		t.Fatalf("unexpected custom frame request: %+v", req)
	}
}
