package usb

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DeviceInfo describes a USB device discovered from Linux sysfs.
type DeviceInfo struct {
	SysfsPath    string
	DevicePath   string
	BusNum       int
	DevNum       int
	VendorID     uint16
	ProductID    uint16
	Manufacturer string
	Product      string
}

// FindDevice scans <sysfsRoot>/bus/usb/devices for the first device matching vid and pid.
// Pass "/sys" on a standard Linux host.
func FindDevice(sysfsRoot string, vid, pid uint16) (DeviceInfo, error) {
	devicesDir := filepath.Join(sysfsRoot, "bus", "usb", "devices")
	entries, err := os.ReadDir(devicesDir)
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("read %s: %w", devicesDir, err)
	}

	for _, entry := range entries {
		dir := filepath.Join(devicesDir, entry.Name())
		gotVID, err := readHexUint16(filepath.Join(dir, "idVendor"))
		if err != nil || gotVID != vid {
			continue
		}
		gotPID, err := readHexUint16(filepath.Join(dir, "idProduct"))
		if err != nil || gotPID != pid {
			continue
		}
		busNum, err := readDecInt(filepath.Join(dir, "busnum"))
		if err != nil {
			return DeviceInfo{}, fmt.Errorf("read busnum for %s: %w", dir, err)
		}
		devNum, err := readDecInt(filepath.Join(dir, "devnum"))
		if err != nil {
			return DeviceInfo{}, fmt.Errorf("read devnum for %s: %w", dir, err)
		}

		return DeviceInfo{
			SysfsPath:    dir,
			DevicePath:   fmt.Sprintf("/dev/bus/usb/%03d/%03d", busNum, devNum),
			BusNum:       busNum,
			DevNum:       devNum,
			VendorID:     gotVID,
			ProductID:    gotPID,
			Manufacturer: readOptionalString(filepath.Join(dir, "manufacturer")),
			Product:      readOptionalString(filepath.Join(dir, "product")),
		}, nil
	}

	return DeviceInfo{}, fmt.Errorf("USB device %04x:%04x not found in %s", vid, pid, devicesDir)
}

func readHexUint16(path string) (uint16, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 16, 16)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}

func readDecInt(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}

func readOptionalString(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
