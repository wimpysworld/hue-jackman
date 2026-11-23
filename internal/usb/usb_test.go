//go:build linux

package usb

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

func TestFindDevice(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	devDir := filepath.Join(root, "bus", "usb", "devices", "1-1.2.2.1")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files := map[string]string{
		"idVendor":     "1532\n",
		"idProduct":    "0a2e\n",
		"busnum":       "1\n",
		"devnum":       "12\n",
		"manufacturer": "Razer\n",
		"product":      "Razer Wolverine V2 Chroma\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(devDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Add an unrelated interface directory without idVendor to ensure graceful skipping.
	if err := os.MkdirAll(filepath.Join(root, "bus", "usb", "devices", "1-1.2.2.1:1.0"), 0o755); err != nil {
		t.Fatalf("mkdir iface: %v", err)
	}

	info, err := FindDevice(root, 0x1532, 0x0a2e)
	if err != nil {
		t.Fatalf("FindDevice failed: %v", err)
	}
	if info.DevicePath != "/dev/bus/usb/001/012" {
		t.Fatalf("got DevicePath %q, want /dev/bus/usb/001/012", info.DevicePath)
	}
	if info.Product != "Razer Wolverine V2 Chroma" {
		t.Fatalf("got Product %q, want Razer Wolverine V2 Chroma", info.Product)
	}
	if info.PhysicalPath != "1-1.2.2.1" {
		t.Fatalf("got PhysicalPath %q, want 1-1.2.2.1", info.PhysicalPath)
	}
}

func TestFindDevicesMultipleSorted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, dev := range []struct {
		path   string
		busnum string
		devnum string
	}{
		{"1-10", "1", "10"},
		{"1-2.1", "1", "4"},
		{"1-2", "1", "3"},
	} {
		dir := filepath.Join(root, "bus", "usb", "devices", dev.path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dev.path, err)
		}
		for name, val := range map[string]string{
			"idVendor":  "1532\n",
			"idProduct": "0a2e\n",
			"busnum":    dev.busnum + "\n",
			"devnum":    dev.devnum + "\n",
			"product":   "Razer Wolverine V2 Chroma\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(val), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
	}

	got, err := FindDevices(root, 0x1532, 0x0a2e)
	if err != nil {
		t.Fatalf("FindDevices failed: %v", err)
	}
	want := []string{"1-2", "1-2.1", "1-10"}
	if len(got) != len(want) {
		t.Fatalf("got %d devices, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].PhysicalPath != w {
			t.Errorf("device[%d].PhysicalPath = %q, want %q", i, got[i].PhysicalPath, w)
		}
	}
}

func TestLinuxUSBDevFSABI(t *testing.T) {
	t.Parallel()

	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("ABI size assertions cover 64-bit Linux")
	}
	if got := unsafe.Sizeof(usbdevfsGetDriver{}); got != 260 {
		t.Fatalf("sizeof(usbdevfsGetDriver) = %d, want 260", got)
	}
	if got := unsafe.Sizeof(usbdevfsIoctl{}); got != 16 {
		t.Fatalf("sizeof(usbdevfsIoctl) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(usbdevfsURB{}); got != 56 {
		t.Fatalf("sizeof(usbdevfsURB) = %d, want 56", got)
	}

	// Verify computed ioctl constants against <linux/usbdevice_fs.h> on x86_64/arm64.
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"USBDEVFS_GETDRIVER", ioctlUSBDevFSGetDriver, 0x41045508},
		{"USBDEVFS_SUBMITURB", ioctlUSBDevFSSubmitURB, 0x8038550a},
		{"USBDEVFS_DISCARDURB", ioctlUSBDevFSDiscardURB, 0x0000550b},
		{"USBDEVFS_REAPURB", ioctlUSBDevFSReapURB, 0x4008550c},
		{"USBDEVFS_REAPURBNDELAY", ioctlUSBDevFSReapURBNDelay, 0x4008550d},
		{"USBDEVFS_CLAIMINTERFACE", ioctlUSBDevFSClaimInterface, 0x8004550f},
		{"USBDEVFS_RELEASEINTERFACE", ioctlUSBDevFSReleaseInterface, 0x80045510},
		{"USBDEVFS_IOCTL", ioctlUSBDevFSIoctl, 0xc0105512},
		{"USBDEVFS_DISCONNECT", ioctlUSBDevFSDisconnect, 0x00005516},
		{"USBDEVFS_CONNECT", ioctlUSBDevFSConnect, 0x00005517},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = 0x%08x, want 0x%08x", c.name, c.got, c.want)
		}
	}
}
