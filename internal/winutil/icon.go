package winutil

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// EnsureAppIcon writes a crisp multi-resolution AirDropWin .ico & .png to dataDir
// matching the Swiss Precision Instrument palette (Carbon #141518 + Signal Vermilion #FF5E2B + Alabaster #F4F4F0).
func EnsureAppIcon(dataDir string) (icoPath string, pngPath string) {
	_ = os.MkdirAll(dataDir, 0o755)
	icoPath = filepath.Join(dataDir, "airdropwin.ico")
	pngPath = filepath.Join(dataDir, "airdropwin.png")

	png32 := renderIconPNG(32)
	png64 := renderIconPNG(64)
	png128 := renderIconPNG(128)

	_ = os.WriteFile(pngPath, png128, 0o644)
	icoData := buildICO([][]byte{png32, png64, png128}, []byte{32, 64, 128})
	_ = os.WriteFile(icoPath, icoData, 0o644)
	return icoPath, pngPath
}

func renderIconPNG(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	fs := float64(size)
	cx := fs * 0.5
	cy := fs * 0.53
	radius := fs * 0.18
	margin := fs * 0.04

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var ar, ag, ab, aa float64
			for sy := 0; sy < 2; sy++ {
				for sx := 0; sx < 2; sx++ {
					px := float64(x) + (float64(sx)+0.5)*0.5
					py := float64(y) + (float64(sy)+0.5)*0.5
					r, g, b, a := sampleIconPixel(px, py, fs, cx, cy, margin, radius)
					ar += r * a
					ag += g * a
					ab += b * a
					aa += a
				}
			}
			aa /= 4.0
			if aa <= 0.001 {
				continue
			}
			ar /= (4.0 * aa)
			ag /= (4.0 * aa)
			ab /= (4.0 * aa)
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(clamp01(ar) * 255),
				G: uint8(clamp01(ag) * 255),
				B: uint8(clamp01(ab) * 255),
				A: uint8(clamp01(aa) * 255),
			})
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func sampleIconPixel(px, py, size, cx, cy, margin, cornerR float64) (r, g, b, a float64) {
	half := size*0.5 - margin
	dx := math.Abs(px-size*0.5) - (half - cornerR)
	dy := math.Abs(py-size*0.5) - (half - cornerR)
	outside := math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0) - cornerR
	if outside > 0.5 {
		return 0, 0, 0, 0
	}
	alpha := clamp01(0.5 - outside)

	// Matte Carbon Graphite base (#141518)
	br, bg, bb := 0.078, 0.082, 0.094

	vx := px - cx
	vy := py - cy
	dist := math.Hypot(vx, vy) / size
	angle := math.Atan2(vy, vx)
	inCutout := math.Abs(angle-math.Pi/2) < 0.65

	// Signal Vermilion (#FF5E2B) emitter core
	if dist < 0.082 {
		return 1.0, 0.369, 0.169, alpha
	}

	// Stepped Alabaster (#F4F4F0) concentric arcs
	if !inCutout {
		type arcSpec struct {
			rad   float64
			halfW float64
			op    float64
		}
		arcs := []arcSpec{
			{0.185, 0.025, 0.96},
			{0.30, 0.023, 0.62},
			{0.41, 0.020, 0.32},
		}
		for _, arc := range arcs {
			if math.Abs(dist-arc.rad) < arc.halfW {
				fg := arc.op
				return br*(1-fg) + 0.957*fg, bg*(1-fg) + 0.957*fg, bb*(1-fg) + 0.941*fg, alpha
			}
		}
	}

	return br, bg, bb, alpha
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func buildICO(pngs [][]byte, sizes []byte) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(pngs)))

	offset := uint32(6 + 16*len(pngs))
	for i, p := range pngs {
		sz := sizes[i]
		buf.WriteByte(sz)
		buf.WriteByte(sz)
		buf.WriteByte(0)
		buf.WriteByte(0)
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(p)))
		_ = binary.Write(&buf, binary.LittleEndian, offset)
		offset += uint32(len(p))
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return buf.Bytes()
}
