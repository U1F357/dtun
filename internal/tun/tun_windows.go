//go:build windows

package tun

import (
	"crypto/sha256"
	"dtun/internal/proto"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

// Device owns a transient adapter and session. The lock keeps End from racing
// with packet memory access or a wait on the session-owned receive event.
type Device struct {
	adapter  *wintun.Adapter
	session  wintun.Session
	stop     windows.Handle
	mu       sync.RWMutex
	closing  atomic.Bool
	once     sync.Once
	closeErr error
}

func Open(name, address string) (*Device, error) { return OpenWithMTU(name, address, 1500) }

func OpenWithMTU(name, address string, mtu int) (*Device, error) {
	prefix, err := netip.ParsePrefix(address)
	if err != nil || prefix.Addr().Is4In6() {
		return nil, fmt.Errorf("invalid TUN address %q", address)
	}
	if mtu < 576 || mtu > proto.MaxInner || (prefix.Addr().Is6() && mtu < 1280) {
		return nil, fmt.Errorf("TUN MTU must be 576..%d (IPv6 >=1280)", proto.MaxInner)
	}
	// Enumerate through the OS rather than taking a second Wintun handle.
	// Reject all name collisions, including adapters owned by other software.
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("enumerate network adapters: %w", err)
	}
	for _, iface := range interfaces {
		if strings.EqualFold(iface.Name, name) {
			return nil, fmt.Errorf("adapter %q already exists; choose another --tun name", name)
		}
	}
	sum := sha256.Sum256([]byte("dtun/wintun/" + name))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	guid, err := windows.GUIDFromString(fmt.Sprintf("{%x-%x-%x-%x-%x}", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16]))
	if err != nil {
		return nil, err
	}
	adapter, err := wintun.CreateAdapter(name, "dtun", &guid)
	if err != nil {
		return nil, fmt.Errorf("create Wintun adapter (administrator and adjacent wintun.dll required): %w", err)
	}
	session, err := adapter.StartSession(4 * 1024 * 1024)
	if err != nil {
		adapter.Close()
		return nil, err
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		session.End()
		adapter.Close()
		return nil, err
	}
	d := &Device{adapter: adapter, session: session, stop: stop}
	if err := configureAdapter(adapter.LUID(), prefix, mtu); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func configureAdapter(luid uint64, prefix netip.Prefix, mtu int) error {
	var index uint32
	proc := windows.NewLazySystemDLL("iphlpapi.dll").NewProc("ConvertInterfaceLuidToIndex")
	if err := proc.Find(); err != nil {
		return err
	}
	result, _, _ := proc.Call(uintptr(unsafe.Pointer(&luid)), uintptr(unsafe.Pointer(&index)))
	if result != 0 {
		return windows.Errno(result)
	}
	// Validated values travel through environment variables, never script interpolation.
	script := `$ErrorActionPreference = 'Stop'
$i = [uint32]$env:DTUN_IFINDEX
$m = [uint32]$env:DTUN_MTU
Set-NetIPInterface -InterfaceIndex $i -AddressFamily IPv4 -Dhcp Disabled -NlMtuBytes $m -PolicyStore ActiveStore
Set-NetIPInterface -InterfaceIndex $i -AddressFamily IPv6 -NlMtuBytes ([Math]::Max(1280, $m)) -PolicyStore ActiveStore
New-NetIPAddress -InterfaceIndex $i -IPAddress $env:DTUN_IP -PrefixLength ([byte]$env:DTUN_PREFIX) -PolicyStore ActiveStore | Out-Null
$deadline = (Get-Date).AddSeconds(15)
do {
 $address = Get-NetIPAddress -InterfaceIndex $i -IPAddress $env:DTUN_IP -ErrorAction Stop
 if ($address.AddressState -eq 'Preferred') { exit 0 }
 if ($address.AddressState -eq 'Duplicate' -or $address.AddressState -eq 'Invalid') {
  throw "TUN address is $($address.AddressState)"
 }
 Start-Sleep -Milliseconds 100
} while ((Get-Date) -lt $deadline)
throw "TUN address did not become usable: $($address.AddressState)"`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "DTUN_IFINDEX="+strconv.FormatUint(uint64(index), 10), "DTUN_MTU="+strconv.Itoa(mtu), "DTUN_IP="+prefix.Addr().String(), "DTUN_PREFIX="+strconv.Itoa(prefix.Bits()))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("configure Wintun: %w: %s", err, out)
	}
	return nil
}

func (d *Device) Read(b []byte) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for {
		if d.closing.Load() {
			return 0, os.ErrClosed
		}
		packet, err := d.session.ReceivePacket()
		if err == nil {
			n := copy(b, packet)
			size := len(packet)
			d.session.ReleaseReceivePacket(packet)
			if n < size {
				return n, io.ErrShortBuffer
			}
			return n, nil
		}
		if !errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return 0, err
		}
		if _, err := windows.WaitForMultipleObjects([]windows.Handle{d.stop, d.session.ReadWaitEvent()}, false, windows.INFINITE); err != nil {
			return 0, err
		}
	}
}

func (d *Device) Write(b []byte) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	deadline := time.Now().Add(100 * time.Millisecond)
	if len(b) == 0 || len(b) > proto.MaxInner {
		return 0, fmt.Errorf("invalid IP packet size %d", len(b))
	}
	for {
		if d.closing.Load() {
			return 0, os.ErrClosed
		}
		packet, err := d.session.AllocateSendPacket(len(b))
		if err == nil {
			copy(packet, b)
			d.session.SendPacket(packet)
			return len(b), nil
		}
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			return 0, err
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("Wintun receive ring full: %w", err)
		}
		// Backpressure without holding an allocated ring packet; Close wakes this wait.
		if _, err := windows.WaitForSingleObject(d.stop, 1); err != nil {
			return 0, err
		}
	}
}

func (d *Device) Close() error {
	d.once.Do(func() {
		d.closing.Store(true)
		windows.SetEvent(d.stop)
		d.mu.Lock()
		defer d.mu.Unlock()
		d.session.End()
		d.closeErr = d.adapter.Close()
		windows.CloseHandle(d.stop)
	})
	return d.closeErr
}
