package usb

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// DeviceInfo describes a USB device discovered from Linux sysfs.
type DeviceInfo struct {
	PhysicalPath string
	SysfsPath    string
	DevicePath   string
	BusNum       int
	DevNum       int
	VendorID     uint16
	ProductID    uint16
	Manufacturer string
	Product      string
}

// FindDevices scans <sysfsRoot>/bus/usb/devices for all devices matching vid and pid,
// sorted deterministically by USB physical path.
func FindDevices(sysfsRoot string, vid, pid uint16) ([]DeviceInfo, error) {
	devicesDir := filepath.Join(sysfsRoot, "bus", "usb", "devices")
	entries, err := os.ReadDir(devicesDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", devicesDir, err)
	}

	var found []DeviceInfo
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
			return nil, fmt.Errorf("read busnum for %s: %w", dir, err)
		}
		devNum, err := readDecInt(filepath.Join(dir, "devnum"))
		if err != nil {
			return nil, fmt.Errorf("read devnum for %s: %w", dir, err)
		}

		found = append(found, DeviceInfo{
			PhysicalPath: entry.Name(),
			SysfsPath:    dir,
			DevicePath:   fmt.Sprintf("/dev/bus/usb/%03d/%03d", busNum, devNum),
			BusNum:       busNum,
			DevNum:       devNum,
			VendorID:     gotVID,
			ProductID:    gotPID,
			Manufacturer: readOptionalString(filepath.Join(dir, "manufacturer")),
			Product:      readOptionalString(filepath.Join(dir, "product")),
		})
	}

	slices.SortFunc(found, func(a, b DeviceInfo) int {
		return comparePhysicalPath(a.PhysicalPath, b.PhysicalPath)
	})
	return found, nil
}

// FindDevice scans <sysfsRoot>/bus/usb/devices for the first device matching vid and pid.
// Pass "/sys" on a standard Linux host.
func FindDevice(sysfsRoot string, vid, pid uint16) (DeviceInfo, error) {
	found, err := FindDevices(sysfsRoot, vid, pid)
	if err != nil {
		return DeviceInfo{}, err
	}
	if len(found) == 0 {
		return DeviceInfo{}, fmt.Errorf("USB device %04x:%04x not found in %s", vid, pid, filepath.Join(sysfsRoot, "bus", "usb", "devices"))
	}
	return found[0], nil
}

func comparePhysicalPath(a, b string) int {
	split := func(r rune) bool { return r == '-' || r == '.' }
	sa, sb := strings.FieldsFunc(a, split), strings.FieldsFunc(b, split)
	for i := range min(len(sa), len(sb)) {
		na, errA := strconv.Atoi(sa[i])
		nb, errB := strconv.Atoi(sb[i])
		switch {
		case errA == nil && errB == nil:
			if c := cmp.Compare(na, nb); c != 0 {
				return c
			}
		case errA == nil:
			return -1
		case errB == nil:
			return 1
		default:
			if c := strings.Compare(sa[i], sb[i]); c != 0 {
				return c
			}
		}
	}
	if c := cmp.Compare(len(sa), len(sb)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
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
