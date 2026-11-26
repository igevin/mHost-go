package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

func trayIcon() []byte {
	icon := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := image.NewUniform(color.NRGBA{A: 255})
	for _, rectangle := range []image.Rectangle{
		image.Rect(4, 4, 28, 8),
		image.Rect(4, 14, 28, 18),
		image.Rect(4, 24, 28, 28),
	} {
		draw.Draw(icon, rectangle, ink, image.Point{}, draw.Src)
	}
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, icon)
	return encoded.Bytes()
}
