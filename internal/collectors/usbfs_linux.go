//go:build linux

package collectors

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Minimal usbfs (/dev/bus/usb) access for vendor-specific devices that have
// no kernel driver, without cgo or libusb.

const (
	usbdevfsControl          = 0xc0185500 // _IOWR('U', 0, struct usbdevfs_ctrltransfer)
	usbdevfsBulk             = 0xc0185502 // _IOWR('U', 2, struct usbdevfs_bulktransfer)
	usbdevfsClaimInterface   = 0x8004550f // _IOR('U', 15, unsigned int)
	usbdevfsReleaseInterface = 0x80045510 // _IOR('U', 16, unsigned int)
	usbdevfsIoctl            = 0xc0105512 // _IOWR('U', 18, struct usbdevfs_ioctl)
	usbdevfsDisconnect       = 0x5516     // _IO('U', 22)
)

type usbdevfsCtrlTransfer struct {
	requestType uint8
	request     uint8
	value       uint16
	index       uint16
	length      uint16
	timeout     uint32
	data        unsafe.Pointer
}

type usbdevfsBulkTransfer struct {
	ep      uint32
	length  uint32
	timeout uint32
	data    unsafe.Pointer
}

type usbdevfsIoctlArg struct {
	ifno      int32
	ioctlCode int32
	data      unsafe.Pointer
}

type usbDevice struct {
	f     *os.File
	iface uint32
}

// findUSBDevice returns the /dev/bus/usb node of the first device matching
// vendor:product.
func findUSBDevice(sysRoot string, vendor, product uint16) (string, error) {
	dirs, err := filepath.Glob(filepath.Join(sysRoot, "*"))
	if err != nil {
		return "", err
	}
	want := [2]string{fmt.Sprintf("%04x", vendor), fmt.Sprintf("%04x", product)}
	for _, dir := range dirs {
		if readTrim(filepath.Join(dir, "idVendor")) != want[0] || readTrim(filepath.Join(dir, "idProduct")) != want[1] {
			continue
		}
		bus, err1 := strconv.Atoi(readTrim(filepath.Join(dir, "busnum")))
		dev, err2 := strconv.Atoi(readTrim(filepath.Join(dir, "devnum")))
		if err1 != nil || err2 != nil {
			continue
		}
		return fmt.Sprintf("/dev/bus/usb/%03d/%03d", bus, dev), nil
	}
	return "", os.ErrNotExist
}

func openUSBDevice(path string, iface uint32) (*usbDevice, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	d := &usbDevice{f: f, iface: iface}
	// Detach a kernel driver if one is bound; ENODATA means none was.
	arg := usbdevfsIoctlArg{ifno: int32(iface), ioctlCode: usbdevfsDisconnect}
	if err := d.ioctl(usbdevfsIoctl, unsafe.Pointer(&arg)); err != nil && !errors.Is(err, unix.ENODATA) {
		_ = f.Close()
		return nil, fmt.Errorf("detaching kernel driver: %w", err)
	}
	if err := d.ioctl(usbdevfsClaimInterface, unsafe.Pointer(&iface)); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("claiming interface %d: %w", iface, err)
	}
	return d, nil
}

func (d *usbDevice) Close() error {
	iface := d.iface
	_ = d.ioctl(usbdevfsReleaseInterface, unsafe.Pointer(&iface))
	return d.f.Close()
}

func (d *usbDevice) ioctl(req uintptr, arg unsafe.Pointer) error {
	conn, err := d.f.SyscallConn()
	if err != nil {
		return err
	}
	var errno unix.Errno
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, fd, req, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

func (d *usbDevice) control(requestType, request uint8, value, index uint16, timeout time.Duration) error {
	ctrl := usbdevfsCtrlTransfer{
		requestType: requestType,
		request:     request,
		value:       value,
		index:       index,
		timeout:     uint32(timeout.Milliseconds()),
	}
	return d.ioctl(usbdevfsControl, unsafe.Pointer(&ctrl))
}

// bulk transfers buf to or from ep and returns the byte count. The direction
// comes from bit 7 of ep.
func (d *usbDevice) bulk(ep uint8, buf []byte, timeout time.Duration) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	xfer := usbdevfsBulkTransfer{
		ep:      uint32(ep),
		length:  uint32(len(buf)),
		timeout: uint32(timeout.Milliseconds()),
		data:    unsafe.Pointer(&buf[0]),
	}
	conn, err := d.f.SyscallConn()
	if err != nil {
		return 0, err
	}
	var n uintptr
	var errno unix.Errno
	if err := conn.Control(func(fd uintptr) {
		n, _, errno = unix.Syscall(unix.SYS_IOCTL, fd, usbdevfsBulk, uintptr(unsafe.Pointer(&xfer)))
	}); err != nil {
		return 0, err
	}
	runtime.KeepAlive(buf)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}
