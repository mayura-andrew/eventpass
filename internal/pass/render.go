// SPDX-License-Identifier: Apache-2.0

package pass

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
	"strings"
	"sync"

	"github.com/go-pdf/fpdf"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Card is what a rendered pass shows. Lines are the fields marked "public on
// pass", already filtered by the caller.
type Card struct {
	ProductName string
	EventName   string
	When        string // formatted in the event's time zone, with the zone
	Venue       string
	FullName    string
	PassID      string
	Token       string
	Lines       []Line
	Primary     string // #RRGGBB
	// Inactive passes (cancelled, rejected) show StatusLabel in a red band
	// instead of looking valid.
	Inactive    bool
	StatusLabel string
}

// Line is one label/value pair on the pass.
type Line struct{ Label, Value string }

// QRPNG renders the token as a QR code. Error correction M scans well from
// a phone screen; size is the image width in pixels.
func QRPNG(token string, size int) ([]byte, error) {
	b, err := qrcode.Encode(token, qrcode.Medium, size)
	if err != nil {
		return nil, fmt.Errorf("render QR: %w", err)
	}
	return b, nil
}

func parseHex(s string, fallback color.RGBA) color.RGBA {
	if len(s) != 7 || s[0] != '#' {
		return fallback
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return fallback
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255} //nolint:gosec // G115: masked to 8 bits by the conversion
}

func inkOn(c color.RGBA) color.RGBA {
	// Relative luminance approximation; good enough to pick black or white.
	l := 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
	if l > 160 {
		return color.RGBA{A: 255}
	}
	return color.RGBA{R: 255, G: 255, B: 255, A: 255}
}

var (
	fontsOnce sync.Once
	fontReg   *opentype.Font
	fontBold  *opentype.Font
	errFonts  error
)

func loadFonts() error {
	fontsOnce.Do(func() {
		if fontReg, errFonts = opentype.Parse(goregular.TTF); errFonts != nil {
			return
		}
		fontBold, errFonts = opentype.Parse(gobold.TTF)
	})
	return errFonts
}

func face(f *opentype.Font, size float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// fit shortens s with an ellipsis until it fits maxWidth.
func fit(f font.Face, s string, maxWidth int) string {
	if font.MeasureString(f, s).Ceil() <= maxWidth {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && font.MeasureString(f, string(r)+"…").Ceil() > maxWidth {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// textWidth is the usable width of the 600 px card.
const textWidth = 600 - 64

func text(dst draw.Image, f font.Face, s string, x, y int, c color.Color, center bool) {
	s = fit(f, s, textWidth)
	if center {
		x -= font.MeasureString(f, s).Ceil() / 2
	}
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: f, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

// CardPNG renders the pass as a 600×900 PNG.
func CardPNG(c Card) ([]byte, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	const w, h = 600, 900
	primary := parseHex(c.Primary, color.RGBA{R: 0x1b, G: 0x4d, B: 0xdb, A: 255})
	ink := inkOn(primary)
	dark := color.RGBA{R: 0x1a, G: 0x1d, B: 0x23, A: 255}
	muted := color.RGBA{R: 0x4d, G: 0x54, B: 0x62, A: 255}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, w, 150), image.NewUniform(primary), image.Point{}, draw.Src)

	small, err := face(fontReg, 20)
	if err != nil {
		return nil, err
	}
	title, err := face(fontBold, 32)
	if err != nil {
		return nil, err
	}
	name, err := face(fontBold, 36)
	if err != nil {
		return nil, err
	}
	mono, err := face(fontBold, 30)
	if err != nil {
		return nil, err
	}
	text(img, small, c.ProductName, 32, 48, ink, false)
	text(img, title, c.EventName, 32, 100, ink, false)
	if c.When != "" {
		text(img, small, c.When, 32, 134, ink, false)
	}

	qrPNG, err := QRPNG(c.Token, 400)
	if err != nil {
		return nil, err
	}
	qr, err := png.Decode(bytes.NewReader(qrPNG))
	if err != nil {
		return nil, err
	}
	draw.Draw(img, image.Rect(100, 170, 500, 570), qr, image.Point{}, draw.Src)

	text(img, mono, c.PassID, w/2, 610, dark, true)
	text(img, name, c.FullName, w/2, 670, dark, true)
	y := 715
	if c.Venue != "" {
		text(img, small, c.Venue, w/2, y, muted, true)
		y += 34
	}
	for _, l := range c.Lines {
		if y > h-40 {
			break
		}
		text(img, small, l.Label+": "+l.Value, w/2, y, muted, true)
		y += 30
	}
	if c.Inactive {
		red := color.RGBA{R: 0x8a, G: 0x1c, B: 0x1c, A: 255}
		draw.Draw(img, image.Rect(0, 330, w, 410), image.NewUniform(red), image.Point{}, draw.Src)
		text(img, title, strings.ToUpper(c.StatusLabel), w/2, 382, color.White, true)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CardPDF renders the pass on an A6 page (105×148 mm), printable at home.
func CardPDF(c Card) ([]byte, error) {
	primary := parseHex(c.Primary, color.RGBA{R: 0x1b, G: 0x4d, B: 0xdb, A: 255})
	ink := inkOn(primary)
	pdf := fpdf.New("P", "mm", "A6", "")
	pdf.SetTitle(c.EventName+" pass", true)
	pdf.SetCreator("EventPass", true)
	pdf.SetMargins(8, 8, 8)
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddUTF8FontFromBytes("go", "", goregular.TTF)
	pdf.AddUTF8FontFromBytes("go", "B", gobold.TTF)
	pdf.AddPage()

	pdf.SetFillColor(int(primary.R), int(primary.G), int(primary.B))
	pdf.Rect(0, 0, 105, 26, "F")
	pdf.SetTextColor(int(ink.R), int(ink.G), int(ink.B))
	pdf.SetFont("go", "", 8)
	pdf.SetXY(8, 5)
	pdf.CellFormat(89, 4, c.ProductName, "", 2, "L", false, 0, "")
	pdf.SetFont("go", "B", 13)
	pdf.CellFormat(89, 7, c.EventName, "", 2, "L", false, 0, "")
	pdf.SetFont("go", "", 8)
	pdf.CellFormat(89, 4, c.When, "", 2, "L", false, 0, "")

	qrPNG, err := QRPNG(c.Token, 600)
	if err != nil {
		return nil, err
	}
	pdf.RegisterImageOptionsReader("qr", fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(qrPNG))
	pdf.ImageOptions("qr", 22.5, 30, 60, 60, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")

	pdf.SetTextColor(26, 29, 35)
	pdf.SetXY(8, 93)
	pdf.SetFont("go", "B", 13)
	pdf.CellFormat(89, 7, c.PassID, "", 2, "C", false, 0, "")
	pdf.SetFont("go", "B", 14)
	pdf.CellFormat(89, 8, c.FullName, "", 2, "C", false, 0, "")
	pdf.SetTextColor(77, 84, 98)
	pdf.SetFont("go", "", 9)
	if c.Venue != "" {
		pdf.CellFormat(89, 5, c.Venue, "", 2, "C", false, 0, "")
	}
	for _, l := range c.Lines {
		if pdf.GetY() > 140 {
			break
		}
		pdf.CellFormat(89, 5, l.Label+": "+l.Value, "", 2, "C", false, 0, "")
	}
	if c.Inactive {
		pdf.SetFillColor(138, 28, 28)
		pdf.Rect(0, 52, 105, 14, "F")
		pdf.SetTextColor(255, 255, 255)
		pdf.SetFont("go", "B", 14)
		pdf.SetXY(8, 55)
		pdf.CellFormat(89, 8, strings.ToUpper(c.StatusLabel), "", 0, "C", false, 0, "")
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
