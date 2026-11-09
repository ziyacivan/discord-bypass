// winassets, Windows sürümü için simgeleri ve .exe kaynaklarını üretir:
//
//	assets/tray-*.ico           sistem tepsisi simgeleri (durum başına bir tane)
//	rsrc_windows_*.syso         .exe simgesi, manifest ve sürüm bilgisi
//
// Kullanım (depo kökünden):  cd tools/winassets && go run . -out ../.. [-preview klasör]
// Çıktılar depoya eklenir; Windows'u derlemek için bu aracı çalıştırmak gerekmez.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
	"golang.org/x/image/vector"
)

type pt struct{ x, y float32 }

// Kalkan ve şimşek, 0..1 aralığında (y aşağı doğru).
var bolt = []pt{
	{0.542, 0.125}, {0.583, 0.125}, {0.542, 0.417}, {0.6875, 0.417},
	{0.458, 0.875}, {0.417, 0.875}, {0.458, 0.583}, {0.3125, 0.583},
}

func shield(r *vector.Rasterizer, s, ox, oy, scale float32) {
	p := func(x, y float32) (float32, float32) { return ox + x*scale*s, oy + y*scale*s }
	r.MoveTo(p(0.50, 0.06))
	r.LineTo(p(0.88, 0.19))
	r.LineTo(p(0.88, 0.47))
	x1, y1 := p(0.88, 0.73)
	x2, y2 := p(0.68, 0.87)
	x3, y3 := p(0.50, 0.96)
	r.CubeTo(x1, y1, x2, y2, x3, y3)
	x1, y1 = p(0.32, 0.87)
	x2, y2 = p(0.12, 0.73)
	x3, y3 = p(0.12, 0.47)
	r.CubeTo(x1, y1, x2, y2, x3, y3)
	r.LineTo(p(0.12, 0.19))
	r.ClosePath()
}

func boltPath(r *vector.Rasterizer, s, ox, oy, scale float32) {
	for i, q := range bolt {
		// Şimşeği kalkanın ortasına küçült.
		x := 0.5 + (q.x-0.5)*0.62
		y := 0.51 + (q.y-0.5)*0.62
		if i == 0 {
			r.MoveTo(ox+x*scale*s, oy+y*scale*s)
		} else {
			r.LineTo(ox+x*scale*s, oy+y*scale*s)
		}
	}
	r.ClosePath()
}

func fill(dst *image.RGBA, src image.Image, path func(*vector.Rasterizer)) {
	b := dst.Bounds()
	r := vector.NewRasterizer(b.Dx(), b.Dy())
	path(r)
	r.Draw(dst, b, src, image.Point{})
}

// trayIcon: durum renginde kalkan, beyaz şimşek.
func trayIcon(size int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float32(size)
	fill(img, image.NewUniform(c), func(r *vector.Rasterizer) { shield(r, s, 0, 0, 1) })
	fill(img, image.NewUniform(color.White), func(r *vector.Rasterizer) { boltPath(r, s, 0, 0, 1) })
	return img
}

// gradient, dikey renk geçişi.
type gradient struct {
	h        int
	top, bot color.RGBA
}

func (g gradient) ColorModel() color.Model { return color.RGBAModel }
func (g gradient) Bounds() image.Rectangle { return image.Rect(-1e6, -1e6, 1e6, 1e6) }
func (g gradient) At(_, y int) color.Color {
	t := float64(y) / float64(g.h)
	t = max(0, min(1, t))
	mix := func(a, b uint8) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t) }
	return color.RGBA{mix(g.top.R, g.bot.R), mix(g.top.G, g.bot.G), mix(g.top.B, g.bot.B), 255}
}

func roundedRect(r *vector.Rasterizer, x0, y0, x1, y1, rad float32) {
	k := rad * 0.5523 // dörtte bir daire için kübik yaklaşım
	r.MoveTo(x0+rad, y0)
	r.LineTo(x1-rad, y0)
	r.CubeTo(x1-rad+k, y0, x1, y0+rad-k, x1, y0+rad)
	r.LineTo(x1, y1-rad)
	r.CubeTo(x1, y1-rad+k, x1-rad+k, y1, x1-rad, y1)
	r.LineTo(x0+rad, y1)
	r.CubeTo(x0+rad-k, y1, x0, y1-rad+k, x0, y1-rad)
	r.LineTo(x0, y0+rad)
	r.CubeTo(x0, y0+rad-k, x0+rad-k, y0, x0+rad, y0)
	r.ClosePath()
}

// appIcon: macOS simgesiyle aynı tasarım; yeşil kare, beyaz kalkan, yeşil şimşek.
func appIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float32(size)
	m := s * 0.06
	g := gradient{h: size, top: color.RGBA{41, 184, 140, 255}, bot: color.RGBA{18, 107, 102, 255}}
	fill(img, g, func(r *vector.Rasterizer) { roundedRect(r, m, m, s-m, s-m, s*0.2) })
	inner := float32(0.62)
	off := s * (1 - inner) / 2
	fill(img, image.NewUniform(color.White), func(r *vector.Rasterizer) { shield(r, s, off, off, inner) })
	fill(img, image.NewUniform(color.RGBA{23, 128, 112, 255}), func(r *vector.Rasterizer) { boltPath(r, s, off, off, inner) })
	return img
}

func images(f func(int) image.Image, sizes ...int) []image.Image {
	out := make([]image.Image, len(sizes))
	for i, n := range sizes {
		out[i] = f(n)
	}
	return out
}

func writeICO(path string, imgs []image.Image) {
	icon, err := winres.NewIconFromImages(imgs)
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := icon.SaveICO(f); err != nil {
		log.Fatal(err)
	}
	fmt.Println("yazıldı:", path)
}

func writePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

func main() {
	out := flag.String("out", "../..", "depo kökü")
	preview := flag.String("preview", "", "önizleme PNG'leri için klasör")
	ver := flag.String("version", "0.0.0", "sürüm (1.2.3)")
	flag.Parse()

	states := map[string]color.RGBA{
		"on":       {34, 160, 107, 255},
		"starting": {230, 150, 30, 255},
		"off":      {138, 143, 152, 255},
		"error":    {229, 72, 77, 255},
	}
	trayDir := filepath.Join(*out, "assets")
	os.MkdirAll(trayDir, 0o755)
	for name, c := range states {
		writeICO(filepath.Join(trayDir, "tray-"+name+".ico"),
			images(func(n int) image.Image { return trayIcon(n, c) }, 16, 20, 24, 32, 40, 48, 64))
		if *preview != "" {
			writePNG(filepath.Join(*preview, "tray-"+name+".png"), trayIcon(64, c))
		}
	}

	app := images(appIcon, 16, 20, 24, 32, 40, 48, 64, 128, 256)
	if *preview != "" {
		writePNG(filepath.Join(*preview, "app.png"), appIcon(256))
		bg := image.NewRGBA(image.Rect(0, 0, 32, 32))
		draw.Draw(bg, bg.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		writePNG(filepath.Join(*preview, "app-32.png"), appIcon(32))
	}
	icon, err := winres.NewIconFromImages(app)
	if err != nil {
		log.Fatal(err)
	}

	rs := winres.ResourceSet{}
	rs.SetIcon(winres.Name("APPICON"), icon)
	rs.SetManifest(winres.AppManifest{
		Description:         "Discord Bypass",
		ExecutionLevel:      winres.AsInvoker,
		DPIAwareness:        winres.DPIPerMonitorV2,
		UseCommonControlsV6: true,
	})
	vi := version.Info{}
	vi.SetFileVersion(*ver)
	vi.SetProductVersion(*ver)
	vi.Set(version.LangDefault, version.ProductName, "Discord Bypass")
	vi.Set(version.LangDefault, version.FileDescription, "Discord Bypass")
	vi.Set(version.LangDefault, version.LegalCopyright, "github.com/ziyacivan/discord-bypass")
	rs.SetVersionInfo(vi)

	for arch, a := range map[string]winres.Arch{"amd64": winres.ArchAMD64, "arm64": winres.ArchARM64} {
		path := filepath.Join(*out, "rsrc_windows_"+arch+".syso")
		f, err := os.Create(path)
		if err != nil {
			log.Fatal(err)
		}
		if err := rs.WriteObject(f, a); err != nil {
			log.Fatal(err)
		}
		f.Close()
		fmt.Println("yazıldı:", path)
	}
}
