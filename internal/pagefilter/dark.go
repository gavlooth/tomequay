// Package pagefilter changes rendered page images without changing their dimensions.
package pagefilter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"
)

// DarkPaper is the packed RGB colour (0xRRGGBB) used for white paper.
// Its neutral near-black keeps grayscale pages neutral and preserves colour hues.
const DarkPaper = 0x101010

const lightText = 239

var shades = func() [256]byte {
	var table [256]byte
	for i := range table {
		table[i] = byte(16 + (i*(lightText-16)+127)/255)
	}
	return table
}()

type encoderBuffers struct{ sync.Pool }

func (p *encoderBuffers) Get() *png.EncoderBuffer {
	if buffer := p.Pool.Get(); buffer != nil {
		return buffer.(*png.EncoderBuffer)
	}
	return new(png.EncoderBuffer)
}

func (p *encoderBuffers) Put(buffer *png.EncoderBuffer) { p.Pool.Put(buffer) }

var pngEncoder = png.Encoder{
	CompressionLevel: png.BestSpeed,
	BufferPool:       new(encoderBuffers),
}

// Dark returns a new PNG with light text on near-black paper. It inverts HSL
// lightness while retaining hue and saturation, then fits channels to [16, 239]
// to soften contrast. Alpha and dimensions are unchanged. Calls may run together;
// neither the input bytes nor a previous result are changed by another call.
func Dark(data []byte) ([]byte, error) {
	page, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode page PNG: %w", err)
	}

	switch page := page.(type) {
	case *image.RGBA:
		darkPixels(page.Pix)
	case *image.NRGBA:
		darkPixels(page.Pix)
	case *image.Gray:
		for i, gray := range page.Pix {
			page.Pix[i] = shades[255-gray]
		}
	case *image.Paletted:
		for i, entry := range page.Palette {
			c := color.NRGBAModel.Convert(entry).(color.NRGBA)
			c.R, c.G, c.B = darkRGB(c.R, c.G, c.B)
			page.Palette[i] = c
		}
	case *image.RGBA64:
		darkPixels16(page.Pix)
	case *image.NRGBA64:
		darkPixels16(page.Pix)
	case *image.Gray16:
		for i := 0; i < len(page.Pix); i += 2 {
			gray := binary.BigEndian.Uint16(page.Pix[i:])
			binary.BigEndian.PutUint16(page.Pix[i:], shade16(65535-uint32(gray)))
		}
	}

	// The result owns this buffer. Pooling it would require copying its bytes or
	// could let a later call overwrite a PNG still being used by its caller.
	var output bytes.Buffer
	output.Grow(len(data))
	if err := pngEncoder.Encode(&output, page); err != nil {
		return nil, fmt.Errorf("encode dark page PNG: %w", err)
	}
	return output.Bytes(), nil
}

func darkPixels(pixels []byte) {
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+1], pixels[i+2] = darkRGB(pixels[i], pixels[i+1], pixels[i+2])
	}
}

func darkRGB(r, g, b byte) (byte, byte, byte) {
	if r == g && g == b {
		gray := shades[255-r]
		return gray, gray, gray
	}
	// Inverting HSL lightness leaves chroma unchanged. Thus every channel is
	// translated by 255 - max(R,G,B) - min(R,G,B), without floating-point HSL.
	shift := 255 - int(max(r, g, b)) - int(min(r, g, b))
	return shades[int(r)+shift], shades[int(g)+shift], shades[int(b)+shift]
}

func darkPixels16(pixels []byte) {
	for i := 0; i < len(pixels); i += 8 {
		r := uint32(binary.BigEndian.Uint16(pixels[i:]))
		g := uint32(binary.BigEndian.Uint16(pixels[i+2:]))
		b := uint32(binary.BigEndian.Uint16(pixels[i+4:]))
		shift := int32(65535) - int32(max(r, g, b)) - int32(min(r, g, b))
		binary.BigEndian.PutUint16(pixels[i:], shade16(uint32(int32(r)+shift)))
		binary.BigEndian.PutUint16(pixels[i+2:], shade16(uint32(int32(g)+shift)))
		binary.BigEndian.PutUint16(pixels[i+4:], shade16(uint32(int32(b)+shift)))
	}
}

func shade16(value uint32) uint16 {
	return uint16(16*257 + (value*(lightText-16)+127)/255)
}
