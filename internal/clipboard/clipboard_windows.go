package clipboard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfUnicodeText = 13
	cfDIB         = 8
	gmemMoveable  = 0x0002
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")

	procGlobalAlloc   = kernel32.NewProc("GlobalAlloc")
	procGlobalFree    = kernel32.NewProc("GlobalFree")
	procGlobalLock    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock  = kernel32.NewProc("GlobalUnlock")
	procGlobalSize    = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

var cfPNG uintptr

func init() {
	name, _ := syscall.UTF16PtrFromString("PNG")
	r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	cfPNG = r
}

func SequenceNumber() uint32 {
	r, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(r)
}

func openClipboardWithRetry() error {
	for i := 0; i < 10; i++ {
		r, _, _ := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return errors.New("无法打开 Windows 剪贴板（可能正被其他程序占用）")
}

func closeClipboard() {
	_, _, _ = procCloseClipboard.Call()
}

func hasFormat(format uintptr) bool {
	if format == 0 {
		return false
	}
	r, _, _ := procIsClipboardFormatAvailable.Call(format)
	return r != 0
}

// ReadText reads Unicode text from the Windows clipboard.
func ReadText() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if !hasFormat(cfUnicodeText) {
		return "", nil
	}
	if err := openClipboardWithRetry(); err != nil {
		return "", err
	}
	defer closeClipboard()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return "", errors.New("GlobalLock failed")
	}
	defer procGlobalUnlock.Call(h)

	sizeBytes, _, _ := procGlobalSize.Call(h)
	maxWords := int(sizeBytes / 2)
	if maxWords <= 0 {
		return "", nil
	}
	if maxWords > 16*1024*1024 {
		maxWords = 16 * 1024 * 1024
	}
	buf := make([]uint16, maxWords)
	_, _, _ = procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), ptr, uintptr(maxWords*2))
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(utf16.Decode(buf[:n])), nil
}

// WriteText writes Unicode text to the Windows clipboard.
func WriteText(text string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	u16 := utf16.Encode([]rune(text))
	u16 = append(u16, 0)
	byteLen := len(u16) * 2

	if err := openClipboardWithRetry(); err != nil {
		return err
	}
	defer closeClipboard()

	_, _, _ = procEmptyClipboard.Call()

	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(byteLen))
	if h == 0 {
		return errors.New("GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return errors.New("GlobalLock failed")
	}
	_, _, _ = procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&u16[0])), uintptr(byteLen))
	_, _, _ = procGlobalUnlock.Call(h)

	r, _, _ := procSetClipboardData.Call(cfUnicodeText, h)
	if r == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return errors.New("SetClipboardData failed")
	}
	return nil
}

// ReadImagePNG reads a PNG or DIB bitmap (e.g. Win+Shift+S screenshot) from the Windows clipboard as PNG bytes.
func ReadImagePNG() ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	wantPNG := hasFormat(cfPNG)
	wantDIB := hasFormat(cfDIB)
	if !wantPNG && !wantDIB {
		return nil, nil
	}
	if err := openClipboardWithRetry(); err != nil {
		return nil, err
	}
	defer closeClipboard()

	if wantPNG {
		if data := readClipboardRawLocked(cfPNG); len(data) > 8 {
			return data, nil
		}
	}
	if wantDIB {
		if dib := readClipboardRawLocked(cfDIB); len(dib) >= 40 {
			return dibToPNG(dib)
		}
	}
	return nil, nil
}

func readClipboardRawLocked(format uintptr) []byte {
	h, _, _ := procGetClipboardData.Call(format)
	if h == 0 {
		return nil
	}
	sizeBytes, _, _ := procGlobalSize.Call(h)
	if sizeBytes == 0 || sizeBytes > 64*1024*1024 {
		return nil
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil
	}
	defer procGlobalUnlock.Call(h)

	out := make([]byte, int(sizeBytes))
	_, _, _ = procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&out[0])), ptr, sizeBytes)
	return out
}

// WriteImage decodes PNG/JPEG/GIF bytes and puts both CF_DIB and PNG formats onto Windows clipboard.
func WriteImage(imgBytes []byte) error {
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return err
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return err
	}
	dibBytes := imageToDIB(img)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := openClipboardWithRetry(); err != nil {
		return err
	}
	defer closeClipboard()

	_, _, _ = procEmptyClipboard.Call()
	if err := setClipboardBytesLocked(cfDIB, dibBytes); err != nil {
		return err
	}
	if cfPNG != 0 {
		_ = setClipboardBytesLocked(cfPNG, pngBuf.Bytes())
	}
	return nil
}

func setClipboardBytesLocked(format uintptr, data []byte) error {
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return errors.New("GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return errors.New("GlobalLock failed")
	}
	if len(data) > 0 {
		_, _, _ = procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	}
	_, _, _ = procGlobalUnlock.Call(h)

	r, _, _ := procSetClipboardData.Call(format, h)
	if r == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return errors.New("SetClipboardData failed")
	}
	return nil
}

// dibToPNG converts standard 24-bit or 32-bit Windows CF_DIB / BITMAPINFOHEADER into PNG bytes.
func dibToPNG(dib []byte) ([]byte, error) {
	if len(dib) < 40 {
		return nil, errors.New("invalid DIB header")
	}
	headerSize := int(binary.LittleEndian.Uint32(dib[0:4]))
	width := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	heightRaw := int(int32(binary.LittleEndian.Uint32(dib[8:12])))
	bpp := int(binary.LittleEndian.Uint16(dib[14:16]))
	compression := binary.LittleEndian.Uint32(dib[16:20])
	clrUsed := int(binary.LittleEndian.Uint32(dib[32:36]))

	if width <= 0 || heightRaw == 0 || width > 16384 || heightRaw > 16384 || heightRaw < -16384 {
		return nil, errors.New("unsupported DIB dimensions")
	}
	topDown := heightRaw < 0
	height := heightRaw
	if topDown {
		height = -height
	}

	pixelOffset := headerSize
	if headerSize == 40 && compression == 3 { // BI_BITFIELDS has 12 extra bytes
		pixelOffset += 12
	}
	if clrUsed > 0 {
		pixelOffset += clrUsed * 4
	}
	if pixelOffset >= len(dib) {
		return nil, errors.New("DIB pixel offset out of bounds")
	}
	if bpp != 24 && bpp != 32 {
		return nil, errors.New("unsupported DIB bit depth")
	}

	bytesPerPixel := bpp / 8
	stride := ((width*bytesPerPixel + 3) / 4) * 4
	if pixelOffset+stride*height > len(dib) {
		return nil, errors.New("DIB pixel buffer truncated")
	}

	rgba := image.NewNRGBA(image.Rect(0, 0, width, height))
	// Check if 32-bit DIB has any non-zero alpha (many Windows screenshots set alpha=0 on every pixel!)
	hasNonZeroAlpha := false
	if bpp == 32 {
		for y := 0; y < height && !hasNonZeroAlpha; y++ {
			rowOff := pixelOffset + y*stride
			for x := 0; x < width; x++ {
				if dib[rowOff+x*4+3] != 0 {
					hasNonZeroAlpha = true
					break
				}
			}
		}
	}

	for y := 0; y < height; y++ {
		srcY := height - 1 - y
		if topDown {
			srcY = y
		}
		rowOff := pixelOffset + srcY*stride
		for x := 0; x < width; x++ {
			p := rowOff + x*bytesPerPixel
			b := dib[p]
			g := dib[p+1]
			r := dib[p+2]
			a := byte(255)
			if bpp == 32 && hasNonZeroAlpha {
				a = dib[p+3]
			}
			rgba.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: a})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, rgba); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// imageToDIB encodes any Go image as a bottom-up 32-bit BGRA CF_DIB payload.
func imageToDIB(img image.Image) []byte {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	stride := w * 4
	imgSize := stride * h

	out := make([]byte, 40+imgSize)
	binary.LittleEndian.PutUint32(out[0:4], 40)
	binary.LittleEndian.PutUint32(out[4:8], uint32(int32(w)))
	binary.LittleEndian.PutUint32(out[8:12], uint32(int32(h))) // bottom-up
	binary.LittleEndian.PutUint16(out[12:14], 1)
	binary.LittleEndian.PutUint16(out[14:16], 32)
	binary.LittleEndian.PutUint32(out[16:20], 0) // BI_RGB
	binary.LittleEndian.PutUint32(out[20:24], uint32(imgSize))

	for y := 0; y < h; y++ {
		dstRow := 40 + (h-1-y)*stride
		for x := 0; x < w; x++ {
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			p := dstRow + x*4
			out[p] = byte(b >> 8)
			out[p+1] = byte(g >> 8)
			out[p+2] = byte(r >> 8)
			out[p+3] = byte(a >> 8)
		}
	}
	return out
}
