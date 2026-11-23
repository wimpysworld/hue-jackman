package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

const (
	// Device info and profile command classes verified from issue #1982.
	ClassDevice  uint8 = 0x00
	ClassProfile uint8 = 0x05

	CmdGetSerial       uint8 = 0x82
	CmdGetFirmware     uint8 = 0x87
	CmdGetDeviceName   uint8 = 0xb4
	CmdGetProfileCount uint8 = 0x80
	CmdGetActiveProf   uint8 = 0x84
)

// GetFirmwareVersion builds a read-only firmware version query (0x00 0x87).
func GetFirmwareVersion() Request {
	return Request{
		CommandClass: ClassDevice,
		CommandID:    CmdGetFirmware,
		Args:         make([]byte, 4),
	}
}

// ParseFirmwareVersion formats the 4-byte firmware version reply (e.g. "1.1.1.0").
func ParseFirmwareVersion(resp Response) (string, error) {
	if !resp.OK() {
		return "", fmt.Errorf("firmware query failed with status %s", StatusName(resp.Status))
	}
	if len(resp.Args) < 4 {
		return "", fmt.Errorf("firmware reply too short: %d bytes", len(resp.Args))
	}
	return fmt.Sprintf("%d.%d.%d.%d", resp.Args[0], resp.Args[1], resp.Args[2], resp.Args[3]), nil
}

// GetSerial builds a read-only serial number query (0x00 0x82).
func GetSerial() Request {
	return Request{
		CommandClass: ClassDevice,
		CommandID:    CmdGetSerial,
		Args:         make([]byte, 22),
	}
}

// ParseSerial extracts the ASCII serial number from a 0x00 0x82 response.
func ParseSerial(resp Response) (string, error) {
	if !resp.OK() {
		return "", fmt.Errorf("serial query failed with status %s", StatusName(resp.Status))
	}
	return string(bytes.TrimRight(resp.Args, "\x00")), nil
}

// GetDeviceNameChunk builds a read-only UTF-16LE device name chunk query (0x00 0xb4).
// The capture uses offset 0x0000 for chunk 0 and 0x0028 (40 bytes) for chunk 1.
func GetDeviceNameChunk(byteOffset uint16) Request {
	args := make([]byte, 45)
	binary.LittleEndian.PutUint16(args[2:4], byteOffset)
	return Request{
		CommandClass: ClassDevice,
		CommandID:    CmdGetDeviceName,
		Args:         args,
	}
}

// ParseDeviceNameChunk returns the declared total byte length and raw UTF-16LE bytes from a 0x00 0xb4 reply.
func ParseDeviceNameChunk(resp Response) (totalBytes uint8, rawUTF16 []byte, err error) {
	if !resp.OK() {
		return 0, nil, fmt.Errorf("device name query failed with status %s", StatusName(resp.Status))
	}
	if len(resp.Args) < 6 {
		return 0, nil, fmt.Errorf("device name reply too short: %d bytes", len(resp.Args))
	}
	return resp.Args[4], resp.Args[5:], nil
}

// DecodeUTF16LE decodes little-endian UTF-16 bytes and trims trailing NUL characters.
func DecodeUTF16LE(b []byte) string {
	n := len(b) / 2
	u16 := make([]uint16, 0, n)
	for i := 0; i+1 < len(b); i += 2 {
		v := binary.LittleEndian.Uint16(b[i : i+2])
		if v == 0 {
			break
		}
		u16 = append(u16, v)
	}
	return strings.TrimSpace(string(utf16.Decode(u16)))
}

// GetActiveProfile builds a read-only active profile query (0x05 0x84).
func GetActiveProfile() Request {
	return Request{
		CommandClass: ClassProfile,
		CommandID:    CmdGetActiveProf,
		Args:         []byte{0x00},
	}
}

// ParseActiveProfile returns the active profile ID from a 0x05 0x84 response.
func ParseActiveProfile(resp Response) (uint8, error) {
	if !resp.OK() {
		return 0, fmt.Errorf("active profile query failed with status %s", StatusName(resp.Status))
	}
	if len(resp.Args) < 1 {
		return 0, fmt.Errorf("active profile reply empty")
	}
	return resp.Args[0], nil
}
