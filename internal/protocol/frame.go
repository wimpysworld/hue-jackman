package protocol

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// USB identity and interface layout for the Razer Wolverine V2 Chroma.
	VendorID     uint16 = 0x1532
	ProductID    uint16 = 0x0a2e
	InterfaceNum uint32 = 0
	EndpointOut  uint8  = 0x01
	EndpointIn   uint8  = 0x81

	// 64-byte GIP vendor frame wrapping a 60-byte Razer report.
	FrameSize     = 64
	PayloadSize   = 0x3c // 60 bytes
	MaxArgSize    = 50   // offsets 0x0c..0x3d inclusive
	MsgRequest    = 0x0f
	MsgResponse   = 0x10
	GIPFlags      = 0x00
	TransactionID = 0x1f

	// Razer report status codes at offset 0x04.
	StatusNew          uint8 = 0x00
	StatusBusy         uint8 = 0x01
	StatusSuccess      uint8 = 0x02
	StatusFailure      uint8 = 0x03
	StatusNoResponse   uint8 = 0x04
	StatusNotSupported uint8 = 0x05

	// Offsets within the 64-byte GIP vendor frame.
	offsetMsgID         = 0x00
	offsetFlags         = 0x01
	offsetSeq           = 0x02
	offsetPayloadLen    = 0x03
	offsetStatus        = 0x04
	offsetTransactionID = 0x05
	offsetCRCStart      = 0x06
	offsetDataSize      = 0x09
	offsetCommandClass  = 0x0a
	offsetCommandID     = 0x0b
	offsetArgs          = 0x0c
	offsetCRC           = 0x3e
	offsetReserved      = 0x3f
)

// RGB represents a 24-bit colour value.
type RGB struct {
	R uint8
	G uint8
	B uint8
}

// ParseRGB parses a 6-digit hexadecimal colour string (with optional leading '#').
func ParseRGB(s string) (RGB, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(trimmed) != 6 {
		return RGB{}, fmt.Errorf("invalid RGB colour %q: expected 6 hex digits (RRGGBB)", s)
	}
	v, err := strconv.ParseUint(trimmed, 16, 32)
	if err != nil {
		return RGB{}, fmt.Errorf("invalid RGB colour %q: %w", s, err)
	}
	return RGB{
		R: uint8((v >> 16) & 0xff),
		G: uint8((v >> 8) & 0xff),
		B: uint8(v & 0xff),
	}, nil
}

// String formats RGB as uppercase RRGGBB.
func (c RGB) String() string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// StatusName returns a readable name for a Razer status byte.
func StatusName(status uint8) string {
	switch status {
	case StatusNew:
		return "new"
	case StatusBusy:
		return "busy"
	case StatusSuccess:
		return "success"
	case StatusFailure:
		return "failure"
	case StatusNoResponse:
		return "no_response"
	case StatusNotSupported:
		return "not_supported"
	default:
		return fmt.Sprintf("unknown(0x%02x)", status)
	}
}

// Request describes a Razer vendor command sent inside a 64-byte GIP 0x0f frame.
type Request struct {
	CommandClass uint8
	CommandID    uint8
	Args         []byte
}

// Response describes a decoded 64-byte GIP 0x10 reply from the controller.
type Response struct {
	Seq           uint8
	Status        uint8
	TransactionID uint8
	DataSize      uint8
	CommandClass  uint8
	CommandID     uint8
	Args          []byte
	CRC           uint8
	Raw           [FrameSize]byte
}

// OK reports whether the controller accepted the command with StatusSuccess.
func (r Response) OK() bool {
	return r.Status == StatusSuccess
}

// Checksum computes the Razer XOR checksum over bytes 0x06..0x3d inclusive.
func Checksum(frame *[FrameSize]byte) uint8 {
	var crc uint8
	for i := offsetCRCStart; i < offsetCRC; i++ {
		crc ^= frame[i]
	}
	return crc
}

// EncodeRequest encodes a Request into a 64-byte GIP 0x0f frame with checksum.
func EncodeRequest(seq uint8, req Request) ([FrameSize]byte, error) {
	var frame [FrameSize]byte
	if len(req.Args) > MaxArgSize {
		return frame, fmt.Errorf("argument length %d exceeds maximum %d bytes", len(req.Args), MaxArgSize)
	}

	frame[offsetMsgID] = MsgRequest
	frame[offsetFlags] = GIPFlags
	frame[offsetSeq] = seq
	frame[offsetPayloadLen] = PayloadSize
	frame[offsetStatus] = StatusNew
	frame[offsetTransactionID] = TransactionID
	frame[offsetDataSize] = uint8(len(req.Args)) //nolint:gosec // bounded to MaxArgSize (50) above
	frame[offsetCommandClass] = req.CommandClass
	frame[offsetCommandID] = req.CommandID
	copy(frame[offsetArgs:], req.Args)
	frame[offsetCRC] = Checksum(&frame)
	frame[offsetReserved] = 0x00

	return frame, nil
}

// DecodeResponse validates and decodes a 64-byte GIP 0x10 response frame.
func DecodeResponse(data []byte) (Response, error) {
	if len(data) != FrameSize {
		return Response{}, fmt.Errorf("invalid frame length %d: expected %d", len(data), FrameSize)
	}

	var raw [FrameSize]byte
	copy(raw[:], data)

	if raw[offsetMsgID] != MsgResponse {
		return Response{}, fmt.Errorf("unexpected message ID 0x%02x: expected 0x%02x", raw[offsetMsgID], MsgResponse)
	}
	if raw[offsetPayloadLen] != PayloadSize {
		return Response{}, fmt.Errorf("unexpected payload length 0x%02x: expected 0x%02x", raw[offsetPayloadLen], PayloadSize)
	}

	wantCRC := Checksum(&raw)
	gotCRC := raw[offsetCRC]
	if wantCRC != gotCRC {
		return Response{}, fmt.Errorf("checksum mismatch: got 0x%02x, want 0x%02x", gotCRC, wantCRC)
	}

	dataSize := raw[offsetDataSize]
	if int(dataSize) > MaxArgSize {
		return Response{}, errors.New("response data_size exceeds maximum argument capacity")
	}

	args := make([]byte, dataSize)
	copy(args, raw[offsetArgs:offsetArgs+int(dataSize)])

	return Response{
		Seq:           raw[offsetSeq],
		Status:        raw[offsetStatus],
		TransactionID: raw[offsetTransactionID],
		DataSize:      dataSize,
		CommandClass:  raw[offsetCommandClass],
		CommandID:     raw[offsetCommandID],
		Args:          args,
		CRC:           gotCRC,
		Raw:           raw,
	}, nil
}
