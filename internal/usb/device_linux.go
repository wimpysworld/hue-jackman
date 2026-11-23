//go:build linux

package usb

import (
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/wimpysworld/hue-jackman/internal/protocol"
)

const (
	iocNone      uintptr = 0
	iocWrite     uintptr = 1
	iocRead      uintptr = 2
	iocReadWrite uintptr = 3

	usbdevfsMagic uintptr = 'U'

	usbdevfsURBTypeInterrupt uint8 = 1
	usbdevfsMaxDriverName    int   = 255
)

func usbdevfsIOC(dir, nr, size uintptr) uintptr {
	return (dir << 30) | (size << 16) | (usbdevfsMagic << 8) | nr
}

// Kernel ABI structs matching <linux/usbdevice_fs.h> on 64-bit Linux.
type usbdevfsGetDriver struct {
	Interface uint32
	Driver    [usbdevfsMaxDriverName + 1]byte
}

type usbdevfsIoctl struct {
	Ifno      int32
	IoctlCode int32
	Data      unsafe.Pointer
}

type usbdevfsURB struct {
	Type            uint8
	Endpoint        uint8
	_               [2]byte
	Status          int32
	Flags           uint32
	_               [4]byte
	Buffer          unsafe.Pointer
	BufferLength    int32
	ActualLength    int32
	StartFrame      int32
	NumberOfPackets int32
	ErrorCount      int32
	Signr           uint32
	UserContext     unsafe.Pointer
}

var (
	ioctlUSBDevFSGetDriver        = usbdevfsIOC(iocWrite, 8, unsafe.Sizeof(usbdevfsGetDriver{}))
	ioctlUSBDevFSSubmitURB        = usbdevfsIOC(iocRead, 10, unsafe.Sizeof(usbdevfsURB{}))
	ioctlUSBDevFSDiscardURB       = usbdevfsIOC(iocNone, 11, 0)
	ioctlUSBDevFSReapURB          = usbdevfsIOC(iocWrite, 12, unsafe.Sizeof(uintptr(0)))
	ioctlUSBDevFSReapURBNDelay    = usbdevfsIOC(iocWrite, 13, unsafe.Sizeof(uintptr(0)))
	ioctlUSBDevFSClaimInterface   = usbdevfsIOC(iocRead, 15, unsafe.Sizeof(uint32(0)))
	ioctlUSBDevFSReleaseInterface = usbdevfsIOC(iocRead, 16, unsafe.Sizeof(uint32(0)))
	ioctlUSBDevFSIoctl            = usbdevfsIOC(iocReadWrite, 18, unsafe.Sizeof(usbdevfsIoctl{}))
	ioctlUSBDevFSDisconnect       = usbdevfsIOC(iocNone, 22, 0)
	ioctlUSBDevFSConnect          = usbdevfsIOC(iocNone, 23, 0)
)

// Options configures how a Linux usbdevfs Device is opened and used.
type Options struct {
	DevicePath   string
	Timeout      time.Duration
	KeepDetached bool
	DebugWriter  io.Writer
}

// Device communicates with a Razer Wolverine V2 Chroma over Linux usbdevfs.
type Device struct {
	fd             int
	epollFd        int
	iface          uint32
	detached       bool
	detachedDriver string
	keepDetached   bool
	timeout        time.Duration
	seq            uint8
	debug          io.Writer
}

// Open opens /dev/bus/usb/BBB/DDD, detaches any bound kernel driver (such as xpad)
// on interface 0, and claims interface 0 for interrupt transfers.
func Open(opts Options) (*Device, error) {
	if opts.DevicePath == "" {
		info, err := FindDevice("/sys", protocol.VendorID, protocol.ProductID)
		if err != nil {
			return nil, err
		}
		opts.DevicePath = info.DevicePath
	}
	if opts.Timeout <= 0 {
		opts.Timeout = time.Second
	}

	fd, err := syscall.Open(opts.DevicePath, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			return nil, fmt.Errorf("permission denied opening %s: run with sudo or install a udev rule granting uaccess to %04x:%04x (%w)",
				opts.DevicePath, protocol.VendorID, protocol.ProductID, err)
		}
		return nil, fmt.Errorf("open %s: %w", opts.DevicePath, err)
	}

	epollFd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("epoll_create1: %w", err)
	}

	if fd < 0 || fd > math.MaxInt32 {
		_ = syscall.Close(epollFd)
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("file descriptor %d out of int32 range", fd)
	}

	ev := syscall.EpollEvent{
		Events: syscall.EPOLLOUT | syscall.EPOLLERR | syscall.EPOLLHUP,
		Fd:     int32(fd), //nolint:gosec // bounded to 0..math.MaxInt32 above
	}
	if err := syscall.EpollCtl(epollFd, syscall.EPOLL_CTL_ADD, fd, &ev); err != nil {
		_ = syscall.Close(epollFd)
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("epoll_ctl add: %w", err)
	}

	d := &Device{
		fd:           fd,
		epollFd:      epollFd,
		iface:        protocol.InterfaceNum,
		keepDetached: opts.KeepDetached,
		timeout:      opts.Timeout,
		debug:        opts.DebugWriter,
	}

	if drv, ok, err := d.boundDriver(); err != nil {
		_ = d.closeFDs()
		return nil, err
	} else if ok {
		d.logf("detaching kernel driver %q from interface %d", drv, d.iface)
		if err := d.driverIoctl(ioctlUSBDevFSDisconnect); err != nil {
			_ = d.closeFDs()
			return nil, fmt.Errorf("detach driver %q on interface %d: %w", drv, d.iface, err)
		}
		d.detached = true
		d.detachedDriver = drv
	}

	iface := d.iface
	if err := ioctl(d.fd, ioctlUSBDevFSClaimInterface, uintptr(unsafe.Pointer(&iface))); err != nil {
		if d.detached && !d.keepDetached {
			_ = d.driverIoctl(ioctlUSBDevFSConnect)
		}
		_ = d.closeFDs()
		return nil, fmt.Errorf("claim interface %d: %w", d.iface, err)
	}

	return d, nil
}

// DetachedDriver reports the kernel driver name detached during Open, if any.
func (d *Device) DetachedDriver() string {
	return d.detachedDriver
}

// Close releases interface 0, reattaches any detached kernel driver unless KeepDetached
// was set, and closes the underlying file descriptors.
func (d *Device) Close() error {
	if d == nil || d.fd < 0 {
		return nil
	}

	var firstErr error
	iface := d.iface
	if err := ioctl(d.fd, ioctlUSBDevFSReleaseInterface, uintptr(unsafe.Pointer(&iface))); err != nil {
		firstErr = fmt.Errorf("release interface %d: %w", d.iface, err)
	}

	if d.detached && !d.keepDetached {
		d.logf("reattaching kernel driver on interface %d", d.iface)
		if err := d.driverIoctl(ioctlUSBDevFSConnect); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("reattach driver on interface %d: %w", d.iface, err)
		}
	}

	if err := d.closeFDs(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (d *Device) closeFDs() error {
	var firstErr error
	if d.epollFd >= 0 {
		if err := syscall.Close(d.epollFd); err != nil {
			firstErr = err
		}
		d.epollFd = -1
	}
	if d.fd >= 0 {
		if err := syscall.Close(d.fd); err != nil && firstErr == nil {
			firstErr = err
		}
		d.fd = -1
	}
	return firstErr
}

// Exchange encodes and sends a 64-byte GIP 0x0f request on endpoint 0x01 and reads
// from endpoint 0x81 until the matching 64-byte GIP 0x10 reply arrives or timeout expires.
// If a stale 0x10 reply from a prior transaction or xpad re-probe is drained and no matching
// reply follows within a short window, Exchange retransmits once with the next sequence byte.
func (d *Device) Exchange(req protocol.Request) (protocol.Response, error) {
	overallDeadline := time.Now().Add(d.timeout)
	const maxAttempts = 2

	for attempt := range maxAttempts {
		resp, stale, err := d.exchangeOnce(req, overallDeadline, attempt < maxAttempts-1)
		if err == nil {
			return resp, nil
		}
		if !stale || attempt+1 >= maxAttempts || time.Until(overallDeadline) <= 0 {
			return protocol.Response{}, err
		}
		d.logf("retrying class=0x%02x cmd=0x%02x after stale reply", req.CommandClass, req.CommandID)
	}
	return protocol.Response{}, fmt.Errorf("timed out waiting for 0x10 reply for class=0x%02x cmd=0x%02x", req.CommandClass, req.CommandID)
}

func (d *Device) exchangeOnce(req protocol.Request, overallDeadline time.Time, allowRetry bool) (protocol.Response, bool, error) {
	d.seq++
	if d.seq == 0 {
		d.seq = 1
	}
	seq := d.seq

	frame, err := protocol.EncodeRequest(seq, req)
	if err != nil {
		return protocol.Response{}, false, err
	}
	d.logf("TX seq=0x%02x class=0x%02x cmd=0x%02x: %x", seq, req.CommandClass, req.CommandID, frame)

	rem := time.Until(overallDeadline)
	if rem <= 0 {
		return protocol.Response{}, false, fmt.Errorf("timed out before TX seq=0x%02x", seq)
	}
	if _, err := d.transferInterrupt(protocol.EndpointOut, frame[:], rem); err != nil {
		return protocol.Response{}, false, fmt.Errorf("interrupt OUT 0x%02x: %w", protocol.EndpointOut, err)
	}

	var inBuf [protocol.FrameSize]byte
	sawStale := false
	rxDeadline := overallDeadline
	for {
		rem := time.Until(rxDeadline)
		if rem <= 0 {
			return protocol.Response{}, sawStale, fmt.Errorf("timed out waiting for 0x10 reply for seq=0x%02x", seq)
		}

		n, err := d.transferInterrupt(protocol.EndpointIn, inBuf[:], rem)
		if err != nil {
			return protocol.Response{}, sawStale, fmt.Errorf("interrupt IN 0x%02x: %w", protocol.EndpointIn, err)
		}
		if n == 0 {
			continue
		}

		// Skip routine Xbox GIP gamepad input (0x20), keepalive (0x03), or guide (0x07) packets.
		if inBuf[0] != protocol.MsgResponse || n != protocol.FrameSize {
			d.logf("RX skip len=%d msg=0x%02x", n, inBuf[0])
			continue
		}

		d.logf("RX len=%d: %x", n, inBuf[:n])
		resp, err := protocol.DecodeResponse(inBuf[:n])
		if err != nil {
			return protocol.Response{}, sawStale, err
		}
		if resp.Seq != seq || resp.CommandClass != req.CommandClass || resp.CommandID != req.CommandID {
			d.logf("RX stale seq=0x%02x class=0x%02x cmd=0x%02x (want seq=0x%02x class=0x%02x cmd=0x%02x)",
				resp.Seq, resp.CommandClass, resp.CommandID, seq, req.CommandClass, req.CommandID)
			sawStale = true
			if allowRetry {
				shortDeadline := time.Now().Add(150 * time.Millisecond)
				if shortDeadline.Before(rxDeadline) {
					rxDeadline = shortDeadline
				}
			}
			continue
		}
		return resp, false, nil
	}
}

func (d *Device) transferInterrupt(ep uint8, buf []byte, timeout time.Duration) (int, error) {
	if len(buf) == 0 {
		return 0, errors.New("empty USB transfer buffer")
	}
	if len(buf) > math.MaxInt32 {
		return 0, fmt.Errorf("USB transfer buffer length %d exceeds int32", len(buf))
	}

	urb := &usbdevfsURB{
		Type:         usbdevfsURBTypeInterrupt,
		Endpoint:     ep,
		Buffer:       unsafe.Pointer(&buf[0]),
		BufferLength: int32(len(buf)), //nolint:gosec // bounded to 1..math.MaxInt32 above
	}

	var pinner runtime.Pinner
	pinner.Pin(urb)
	pinner.Pin(&buf[0])
	defer pinner.Unpin()

	if err := ioctl(d.fd, ioctlUSBDevFSSubmitURB, uintptr(unsafe.Pointer(urb))); err != nil {
		return 0, fmt.Errorf("USBDEVFS_SUBMITURB: %w", err)
	}

	deadline := time.Now().Add(timeout)
	var events [1]syscall.EpollEvent
	for {
		reaped, err := d.reapNonBlocking()
		if err == nil {
			if reaped != uintptr(unsafe.Pointer(urb)) {
				return 0, errors.New("reaped unexpected URB pointer")
			}
			break
		}
		if !errors.Is(err, syscall.EAGAIN) {
			return 0, fmt.Errorf("USBDEVFS_REAPURBNDELAY: %w", err)
		}

		rem := time.Until(deadline)
		if rem <= 0 {
			d.cancelAndReap(urb)
			return 0, syscall.ETIMEDOUT
		}

		waitMs := int(rem.Milliseconds())
		if waitMs < 1 {
			waitMs = 1
		}
		_, err = syscall.EpollWait(d.epollFd, events[:], waitMs)
		if err != nil && !errors.Is(err, syscall.EINTR) {
			d.cancelAndReap(urb)
			return 0, fmt.Errorf("epoll_wait: %w", err)
		}
	}

	if urb.Status != 0 {
		errnoVal := urb.Status
		if errnoVal < 0 {
			errnoVal = -errnoVal
		}
		return int(urb.ActualLength), fmt.Errorf("URB completed with status %d (%w)", urb.Status, syscall.Errno(errnoVal)) //nolint:gosec // positive kernel errno
	}
	return int(urb.ActualLength), nil
}

func (d *Device) reapNonBlocking() (uintptr, error) {
	var reaped uintptr
	err := ioctl(d.fd, ioctlUSBDevFSReapURBNDelay, uintptr(unsafe.Pointer(&reaped)))
	if err != nil {
		return 0, err
	}
	return reaped, nil
}

// cancelAndReap ensures the kernel releases the URB before pinned buffers are unpinned.
func (d *Device) cancelAndReap(urb *usbdevfsURB) {
	_ = ioctl(d.fd, ioctlUSBDevFSDiscardURB, uintptr(unsafe.Pointer(urb)))
	for {
		var reaped uintptr
		err := ioctl(d.fd, ioctlUSBDevFSReapURB, uintptr(unsafe.Pointer(&reaped)))
		if err == nil || !errors.Is(err, syscall.EINTR) {
			return
		}
	}
}

func (d *Device) boundDriver() (string, bool, error) {
	gd := usbdevfsGetDriver{Interface: d.iface}
	err := ioctl(d.fd, ioctlUSBDevFSGetDriver, uintptr(unsafe.Pointer(&gd)))
	if err != nil {
		if errors.Is(err, syscall.ENODATA) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("USBDEVFS_GETDRIVER interface %d: %w", d.iface, err)
	}
	name := strings.TrimRight(string(gd.Driver[:]), "\x00")
	return name, true, nil
}

func (d *Device) driverIoctl(code uintptr) error {
	if d.iface > math.MaxInt32 {
		return fmt.Errorf("interface %d exceeds int32", d.iface)
	}
	if code > math.MaxInt32 {
		return fmt.Errorf("ioctl code 0x%x exceeds int32", code)
	}
	req := usbdevfsIoctl{
		Ifno:      int32(d.iface), //nolint:gosec // bounded above
		IoctlCode: int32(code),    //nolint:gosec // bounded above
	}
	return ioctl(d.fd, ioctlUSBDevFSIoctl, uintptr(unsafe.Pointer(&req)))
}

func (d *Device) logf(format string, args ...any) {
	if d.debug != nil {
		_, _ = fmt.Fprintf(d.debug, "[usb] "+format+"\n", args...)
	}
}

func ioctl(fd int, req, arg uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, arg)
	if errno != 0 {
		return errno
	}
	return nil
}
