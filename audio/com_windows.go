//go:build windows

package audio

import (
	"fmt"
	"syscall"
	"unsafe"

	"whisprgo/internal/win"
)

// The COM vtable plumbing WASAPI needs, hand-written because whisprgo has no
// dependencies and cgo is what makes the macOS build impossible to compile
// anywhere but a Mac. Keeping the Windows half pure Go is what lets it be
// cross-compiled and vetted from anywhere.
//
// Each interface is a struct whose single field is a pointer to its vtable,
// which is exactly how a COM object is laid out in memory. Declaring them
// this way means the out-parameters are typed Go pointers all the way down,
// so no uintptr is ever turned back into a pointer.

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidMMDeviceEnumerator = guid{0xBCDE0395, 0xE52F, 0x467C, [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator  = guid{0xA95664D2, 0x9614, 0x4F35, [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioClient         = guid{0x1CB9AD4C, 0xDBFA, 0x4C32, [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidIAudioCaptureClient  = guid{0xC8ADBD64, 0xE71E, 0x48A0, [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}

	subtypePCM   = guid{0x00000001, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}}
	subtypeFloat = guid{0x00000003, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}}
)

func (g guid) equals(o guid) bool {
	return g.Data1 == o.Data1 && g.Data2 == o.Data2 && g.Data3 == o.Data3 && g.Data4 == o.Data4
}

const (
	sOK     = 0
	sFalse  = 1
	clsctx  = 0x17 // CLSCTX_ALL
	eRender = 0
	// eCapture selects the input side of the endpoint enumerator, and
	// eConsole the device Windows itself would use — the one the user set as
	// their default microphone.
	eCapture = 1
	eConsole = 0

	coinitMultithreaded = 0x0

	waveFormatPCM           = 0x0001
	waveFormatIEEEFloat     = 0x0003
	waveFormatExtensibleTag = 0xFFFE

	audclntShareModeShared = 0

	streamflagsEventCallback     = 0x00040000
	streamflagsAutoConvertPCM    = 0x80000000
	streamflagsSRCDefaultQuality = 0x08000000

	bufferflagsSilent = 0x2

	audclntSBufferEmpty = 0x08890001

	// Errors worth translating into something a person can act on.
	audclntEUnsupportedFormat        = 0x88890008
	audclntEDeviceInUse              = 0x8889000A
	audclntEDeviceInvalidated        = 0x88890004
	audclntEServiceNotRunning        = 0x88890010
	errAccessDenied                  = 0x80070005
	errNotFound                      = 0x80070490
	rpcEChangedMode           uint32 = 0x80010106
)

var (
	coInitializeEx   = win.Proc("ole32.dll", "CoInitializeEx")
	coUninitialize   = win.Proc("ole32.dll", "CoUninitialize")
	coCreateInstance = win.Proc("ole32.dll", "CoCreateInstance")
	coTaskMemFree    = win.Proc("ole32.dll", "CoTaskMemFree")
)

// hresult turns an HRESULT into an error, naming the ones a user can do
// something about. The bare hex is kept in every message: it is what a search
// finds, and these failures are only ever reported from a machine we cannot
// debug on.
func hresult(hr uintptr, what string) error {
	code := uint32(hr)
	if int32(code) >= 0 {
		return nil
	}
	switch code {
	case errAccessDenied:
		return fmt.Errorf("%s: Windows blocked microphone access (0x%08X). Turn on "+
			"Settings > Privacy & security > Microphone, and \"Let desktop apps access your microphone\"", what, code)
	case errNotFound:
		return fmt.Errorf("%s: no microphone is set as the default recording device (0x%08X)", what, code)
	case audclntEDeviceInUse:
		return fmt.Errorf("%s: another program has the microphone open in exclusive mode (0x%08X)", what, code)
	case audclntEDeviceInvalidated:
		return fmt.Errorf("%s: the microphone was unplugged or changed (0x%08X)", what, code)
	case audclntEServiceNotRunning:
		return fmt.Errorf("%s: the Windows Audio service is not running (0x%08X)", what, code)
	case audclntEUnsupportedFormat:
		return fmt.Errorf("%s: the microphone rejected the requested format (0x%08X)", what, code)
	}
	return fmt.Errorf("%s: 0x%08X", what, code)
}

type iUnknownVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

type iUnknown struct {
	vtbl *iUnknownVtbl
}

func (u *iUnknown) Release() {
	if u != nil {
		syscall.SyscallN(u.vtbl.Release, uintptr(unsafe.Pointer(u)))
	}
}

type iMMDeviceEnumeratorVtbl struct {
	iUnknownVtbl
	EnumAudioEndpoints                     uintptr
	GetDefaultAudioEndpoint                uintptr
	GetDevice                              uintptr
	RegisterEndpointNotificationCallback   uintptr
	UnregisterEndpointNotificationCallback uintptr
}

type iMMDeviceEnumerator struct {
	vtbl *iMMDeviceEnumeratorVtbl
}

func (e *iMMDeviceEnumerator) Release() { (*iUnknown)(unsafe.Pointer(e)).Release() }

func (e *iMMDeviceEnumerator) GetDefaultAudioEndpoint(flow, role uint32) (*iMMDevice, error) {
	var dev *iMMDevice
	hr, _, _ := syscall.SyscallN(e.vtbl.GetDefaultAudioEndpoint,
		uintptr(unsafe.Pointer(e)), uintptr(flow), uintptr(role), uintptr(unsafe.Pointer(&dev)))
	if err := hresult(hr, "find the default microphone"); err != nil {
		return nil, err
	}
	return dev, nil
}

type iMMDeviceVtbl struct {
	iUnknownVtbl
	Activate          uintptr
	OpenPropertyStore uintptr
	GetId             uintptr
	GetState          uintptr
}

type iMMDevice struct {
	vtbl *iMMDeviceVtbl
}

func (d *iMMDevice) Release() { (*iUnknown)(unsafe.Pointer(d)).Release() }

func (d *iMMDevice) ActivateAudioClient() (*iAudioClient, error) {
	var client *iAudioClient
	hr, _, _ := syscall.SyscallN(d.vtbl.Activate,
		uintptr(unsafe.Pointer(d)),
		uintptr(unsafe.Pointer(&iidIAudioClient)),
		clsctx,
		0,
		uintptr(unsafe.Pointer(&client)))
	if err := hresult(hr, "open the microphone"); err != nil {
		return nil, err
	}
	return client, nil
}

type iAudioClientVtbl struct {
	iUnknownVtbl
	Initialize        uintptr
	GetBufferSize     uintptr
	GetStreamLatency  uintptr
	GetCurrentPadding uintptr
	IsFormatSupported uintptr
	GetMixFormat      uintptr
	GetDevicePeriod   uintptr
	Start             uintptr
	Stop              uintptr
	Reset             uintptr
	SetEventHandle    uintptr
	GetService        uintptr
}

type iAudioClient struct {
	vtbl *iAudioClientVtbl
}

func (c *iAudioClient) Release() { (*iUnknown)(unsafe.Pointer(c)).Release() }

func (c *iAudioClient) Initialize(flags uint32, bufferDuration int64, format *waveFormatEx) error {
	hr, _, _ := syscall.SyscallN(c.vtbl.Initialize,
		uintptr(unsafe.Pointer(c)),
		audclntShareModeShared,
		uintptr(flags),
		uintptr(bufferDuration),
		0, // periodicity must be zero in shared mode
		uintptr(unsafe.Pointer(format)),
		0) // no audio session GUID
	return hresult(hr, "configure the microphone stream")
}

func (c *iAudioClient) GetMixFormat() (*waveFormatEx, error) {
	var f *waveFormatEx
	hr, _, _ := syscall.SyscallN(c.vtbl.GetMixFormat,
		uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&f)))
	if err := hresult(hr, "read the microphone's format"); err != nil {
		return nil, err
	}
	return f, nil
}

func (c *iAudioClient) SetEventHandle(h syscall.Handle) error {
	hr, _, _ := syscall.SyscallN(c.vtbl.SetEventHandle,
		uintptr(unsafe.Pointer(c)), uintptr(h))
	return hresult(hr, "attach the capture event")
}

func (c *iAudioClient) Start() error {
	hr, _, _ := syscall.SyscallN(c.vtbl.Start, uintptr(unsafe.Pointer(c)))
	return hresult(hr, "start the microphone")
}

func (c *iAudioClient) Stop() error {
	hr, _, _ := syscall.SyscallN(c.vtbl.Stop, uintptr(unsafe.Pointer(c)))
	return hresult(hr, "stop the microphone")
}

func (c *iAudioClient) Reset() error {
	hr, _, _ := syscall.SyscallN(c.vtbl.Reset, uintptr(unsafe.Pointer(c)))
	return hresult(hr, "reset the microphone stream")
}

func (c *iAudioClient) GetCaptureClient() (*iAudioCaptureClient, error) {
	var cc *iAudioCaptureClient
	hr, _, _ := syscall.SyscallN(c.vtbl.GetService,
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(&iidIAudioCaptureClient)),
		uintptr(unsafe.Pointer(&cc)))
	if err := hresult(hr, "open the capture stream"); err != nil {
		return nil, err
	}
	return cc, nil
}

type iAudioCaptureClientVtbl struct {
	iUnknownVtbl
	GetBuffer         uintptr
	ReleaseBuffer     uintptr
	GetNextPacketSize uintptr
}

type iAudioCaptureClient struct {
	vtbl *iAudioCaptureClientVtbl
}

func (c *iAudioCaptureClient) Release() { (*iUnknown)(unsafe.Pointer(c)).Release() }

func (c *iAudioCaptureClient) GetNextPacketSize() (uint32, error) {
	var n uint32
	hr, _, _ := syscall.SyscallN(c.vtbl.GetNextPacketSize,
		uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&n)))
	return n, hresult(hr, "size the next capture packet")
}

// GetBuffer hands back a window into the endpoint buffer. The data belongs to
// WASAPI until ReleaseBuffer, so callers must consume it before then.
func (c *iAudioCaptureClient) GetBuffer() (data *byte, frames uint32, flags uint32, empty bool, err error) {
	hr, _, _ := syscall.SyscallN(c.vtbl.GetBuffer,
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(&data)),
		uintptr(unsafe.Pointer(&frames)),
		uintptr(unsafe.Pointer(&flags)),
		0, 0) // device position and QPC timestamp are not used
	if uint32(hr) == audclntSBufferEmpty {
		return nil, 0, 0, true, nil
	}
	return data, frames, flags, false, hresult(hr, "read the capture buffer")
}

func (c *iAudioCaptureClient) ReleaseBuffer(frames uint32) error {
	hr, _, _ := syscall.SyscallN(c.vtbl.ReleaseBuffer,
		uintptr(unsafe.Pointer(c)), uintptr(frames))
	return hresult(hr, "release the capture buffer")
}

// waveFormatEx is WAVEFORMATEX. Go pads the tail to 20 bytes where C packs it
// to 18, which is harmless: every field sits at the same offset, and cbSize
// says there is nothing after them.
type waveFormatEx struct {
	wFormatTag      uint16
	nChannels       uint16
	nSamplesPerSec  uint32
	nAvgBytesPerSec uint32
	nBlockAlign     uint16
	wBitsPerSample  uint16
	cbSize          uint16
}

// waveFormatExtensible is WAVEFORMATEXTENSIBLE, spelled out flat rather than
// as an embedded waveFormatEx. Embedding would put samples at offset 20,
// because Go pads the inner struct; written out like this every field lands
// where the 40-byte C layout puts it.
type waveFormatExtensible struct {
	wFormatTag      uint16 // 0
	nChannels       uint16 // 2
	nSamplesPerSec  uint32 // 4
	nAvgBytesPerSec uint32 // 8
	nBlockAlign     uint16 // 12
	wBitsPerSample  uint16 // 14
	cbSize          uint16 // 16
	samples         uint16 // 18
	dwChannelMask   uint32 // 20
	subFormat       guid   // 24
}

var _ = [1]struct{}{}[unsafe.Sizeof(waveFormatExtensible{})-40]

// describe works out what Convert will be fed. A shared-mode mix format is
// usually WAVE_FORMAT_EXTENSIBLE, where the real sample type is a GUID rather
// than the tag.
func (f *waveFormatEx) describe() (pcmFormat, error) {
	out := pcmFormat{
		rate:     int(f.nSamplesPerSec),
		channels: int(f.nChannels),
		bits:     int(f.wBitsPerSample),
	}
	switch f.wFormatTag {
	case waveFormatPCM:
	case waveFormatIEEEFloat:
		out.isFloat = true
	case waveFormatExtensibleTag:
		if f.cbSize < 22 {
			return out, fmt.Errorf("extensible capture format is missing its sub-format")
		}
		ext := (*waveFormatExtensible)(unsafe.Pointer(f))
		switch {
		case ext.subFormat.equals(subtypeFloat):
			out.isFloat = true
		case ext.subFormat.equals(subtypePCM):
		default:
			return out, fmt.Errorf("unsupported capture sub-format %08X", ext.subFormat.Data1)
		}
	default:
		return out, fmt.Errorf("unsupported capture format tag %04X", f.wFormatTag)
	}
	return out, out.valid()
}

// coInitialize joins the multi-threaded apartment. S_FALSE means this thread
// was already initialized, and RPC_E_CHANGED_MODE means something put it in a
// single-threaded apartment first; both leave COM usable here.
func coInitialize() error {
	hr, _, _ := coInitializeEx.Call(0, coinitMultithreaded)
	if hr == sOK || hr == sFalse || uint32(hr) == rpcEChangedMode {
		return nil
	}
	return hresult(hr, "initialise COM")
}

func createDeviceEnumerator() (*iMMDeviceEnumerator, error) {
	var e *iMMDeviceEnumerator
	hr, _, _ := coCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)),
		0,
		clsctx,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&e)))
	if err := hresult(hr, "list the audio devices"); err != nil {
		return nil, err
	}
	return e, nil
}
