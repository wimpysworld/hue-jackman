package protocol

import "fmt"

const (
	// Extended Matrix Chroma command class and subcommands.
	ClassExtendedMatrix uint8 = 0x0f

	CmdSetEffect      uint8 = 0x02
	CmdSetCustomFrame uint8 = 0x03 // Experimental: standard OpenRazer extended matrix per-LED frame write.
	CmdSetBrightness  uint8 = 0x04
	CmdGetEffect      uint8 = 0x82 // Experimental: standard OpenRazer extended matrix effect query.
	CmdGetBrightness  uint8 = 0x84 // Experimental: standard OpenRazer extended matrix brightness query.

	// Storage / LED defaults observed in the OpenRazer issue #1982 capture.
	DefaultVarstore    uint8 = 0x01
	DefaultEffectLED   uint8 = 0x00 // Requests send 0x00; firmware replies with 0x05 (BacklightLED).
	BacklightLED       uint8 = 0x05
	DefaultEffectParam uint8 = 0x01 // Observed at args[4] in all captured 0x0f 0x02 requests.

	// Extended Matrix effect IDs.
	EffectOff         uint8 = 0x00
	EffectStatic      uint8 = 0x01
	EffectBreathing   uint8 = 0x02
	EffectSpectrum    uint8 = 0x03
	EffectWave        uint8 = 0x04 // Unverified on Wolverine V2 Chroma.
	EffectReactive    uint8 = 0x05 // Unverified on Wolverine V2 Chroma.
	EffectStarlight   uint8 = 0x07 // Unverified on Wolverine V2 Chroma.
	EffectCustomFrame uint8 = 0x08 // Experimental: standard OpenRazer custom frame mode.
)

// Target selects storage and LED zone parameters for Chroma commands.
type Target struct {
	Varstore uint8
	LEDID    uint8
}

// DefaultEffectTarget returns the varstore and LED ID captured for 0x0f 0x02.
func DefaultEffectTarget() Target {
	return Target{Varstore: DefaultVarstore, LEDID: DefaultEffectLED}
}

// DefaultBrightnessTarget returns the varstore and LED ID captured for 0x0f 0x04.
func DefaultBrightnessTarget() Target {
	return Target{Varstore: DefaultVarstore, LEDID: BacklightLED}
}

// Off builds a verified Chroma Off request (class 0x0f, cmd 0x02, effect 0x00).
func Off(t Target) Request {
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdSetEffect,
		Args:         []byte{t.Varstore, t.LEDID, EffectOff, 0x00, DefaultEffectParam, 0x00},
	}
}

// Static builds a verified solid RGB colour request (class 0x0f, cmd 0x02, effect 0x01).
func Static(t Target, c RGB) Request {
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdSetEffect,
		Args:         []byte{t.Varstore, t.LEDID, EffectStatic, 0x00, DefaultEffectParam, 0x01, c.R, c.G, c.B},
	}
}

// BreathingSingle builds a verified 1-colour breathing request (class 0x0f, cmd 0x02, effect 0x02).
func BreathingSingle(t Target, c RGB) Request {
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdSetEffect,
		Args:         []byte{t.Varstore, t.LEDID, EffectBreathing, 0x00, DefaultEffectParam, 0x01, c.R, c.G, c.B},
	}
}

// BreathingDual builds a verified 2-colour breathing request (class 0x0f, cmd 0x02, effect 0x02).
func BreathingDual(t Target, c1, c2 RGB) Request {
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdSetEffect,
		Args: []byte{
			t.Varstore, t.LEDID, EffectBreathing, 0x00, DefaultEffectParam, 0x02,
			c1.R, c1.G, c1.B,
			c2.R, c2.G, c2.B,
		},
	}
}

// Spectrum builds a verified spectrum cycling request (class 0x0f, cmd 0x02, effect 0x03).
func Spectrum(t Target) Request {
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdSetEffect,
		Args:         []byte{t.Varstore, t.LEDID, EffectSpectrum, 0x00, DefaultEffectParam, 0x00},
	}
}

// SetBrightness builds a verified brightness request (class 0x0f, cmd 0x04).
func SetBrightness(t Target, level uint8) Request {
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdSetBrightness,
		Args:         []byte{t.Varstore, t.LEDID, level},
	}
}

// GetBrightness builds an experimental read-only brightness query (class 0x0f, cmd 0x84).
func GetBrightness(t Target) Request {
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdGetBrightness,
		Args:         []byte{t.Varstore, t.LEDID, 0x00},
	}
}

// GetEffect builds an experimental read-only effect query (class 0x0f, cmd 0x82).
func GetEffect(t Target) Request {
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdGetEffect,
		Args:         []byte{t.Varstore, t.LEDID, 0x00, 0x00, 0x00, 0x00},
	}
}

// CustomFrameMode builds an experimental Extended Matrix Custom Frame mode switch (effect 0x08).
func CustomFrameMode() Request {
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdSetEffect,
		Args:         []byte{0x00, 0x00, EffectCustomFrame, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	}
}

// SetCustomFrameRow builds an experimental per-LED custom frame row write (class 0x0f, cmd 0x03).
// Up to 15 RGB LEDs fit in a single 50-byte argument payload (5 header bytes + 45 RGB bytes).
func SetCustomFrameRow(row, startCol uint8, colors []RGB) (Request, error) {
	if len(colors) == 0 {
		return Request{}, fmt.Errorf("custom frame row requires at least one colour")
	}
	if 5+3*len(colors) > MaxArgSize {
		return Request{}, fmt.Errorf("custom frame row with %d colours exceeds %d-byte payload limit", len(colors), MaxArgSize)
	}
	stopColInt := int(startCol) + len(colors) - 1
	if stopColInt > 0xff {
		return Request{}, fmt.Errorf("custom frame stop column %d exceeds 255", stopColInt)
	}
	stopCol := uint8(stopColInt) //nolint:gosec // bounded to 0..255 above
	args := make([]byte, 0, 5+3*len(colors))
	args = append(args, 0x00, 0x00, row, startCol, stopCol)
	for _, c := range colors {
		args = append(args, c.R, c.G, c.B)
	}
	return Request{
		CommandClass: ClassExtendedMatrix,
		CommandID:    CmdSetCustomFrame,
		Args:         args,
	}, nil
}
