package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/egonelbre/async"
	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"
)

type Gallery struct {
	Name    string
	Path    string
	Unbound string
	Images  []*Image
}

func (gallery *Gallery) PageLink() string {
	return path.Join("/", filepath.ToSlash(gallery.Unbound))
}

func (gallery *Gallery) FirstImages(n int) []*Image {
	if n > len(gallery.Images) {
		n = len(gallery.Images)
	}
	return gallery.Images[:n]
}

type Image struct {
	Name    string
	Raw     string
	Path    string
	Thumb   string
	Unbound string
	Info    os.FileInfo
}

func (image *Image) PageLink() string {
	return path.Join("/", ReplaceExt(filepath.ToSlash(image.Unbound), ".html"))
}

func (image *Image) ImageLink() string {
	return path.Join("/", filepath.ToSlash(image.Path))
}

func (image *Image) ThumbLink() string {
	return path.Join("/", filepath.ToSlash(image.Thumb))
}

const (
	largesize = 1024
	thumbsize = 256
)

var T = template.Must(template.ParseGlob("*.html"))
var pagesonly = flag.Bool("pages", false, "generate only pages")
var regenerate = flag.Bool("regenerate", false, "generate only pages")

func main() {
	flag.Parse()

	galleries := map[string]*Gallery{}

	imagesDir := "images"

	err := filepath.Walk(imagesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(info.Name()))
		if ext != ".jpeg" && ext != ".jpg" && ext != ".png" {
			return nil
		}

		galleryPath := strings.ToLower(filepath.Dir(path))
		gallery, ok := galleries[galleryPath]
		if !ok {
			gallery = &Gallery{}
			gallery.Name = filepath.Base(filepath.Dir(path))
			gallery.Path = filepath.Dir(path)
			gallery.Unbound = strings.TrimPrefix(gallery.Path, imagesDir+string(filepath.Separator))
			galleries[galleryPath] = gallery
		}

		gallery.Images = append(gallery.Images, &Image{
			Name:    ReplaceExt(filepath.Base(path), ""),
			Raw:     path,
			Path:    path,
			Unbound: strings.TrimPrefix(path, imagesDir+string(filepath.Separator)),
			Info:    info,
		})

		return nil
	})

	for _, gallery := range galleries {
		sort.Slice(gallery.Images, func(i, k int) bool {
			return gallery.Images[k].Info.ModTime().Before(gallery.Images[i].Info.ModTime())
		})

		// update paths
		for _, image := range gallery.Images {
			image.Thumb = filepath.Join("thumbs", ReplaceExt(image.Unbound, ".png"))
			image.Path = ReplaceExt(image.Path, ".jpg")
		}

		// generate images
		if !*pagesonly {
			async.Iter(len(gallery.Images), runtime.GOMAXPROCS(-1), func(i int) {
				image := gallery.Images[i]

				fmt.Println("Downscaling ", gallery.Name, image.Name)

				thumbname := filepath.Join("public", image.Thumb)
				imagename := filepath.Join("public", image.Path)

				if !*regenerate && FileExists(thumbname) && FileExists(imagename) {
					return
				}

				m, err := LoadImage(image.Raw)
				if err != nil {
					log.Println(err)
					return
				}

				thumb := Downscale(m, thumbsize)
				if *regenerate || !FileExists(thumbname) {
					SavePNG(thumb, thumbname)
				}

				large := Downscale(m, largesize)
				if *regenerate || !FileExists(imagename) {
					SaveJPG(large, imagename)
				}
			})
		}

		// generate pages
		for i, image := range gallery.Images {
			var prev, next string
			if i > 0 {
				prev = gallery.Images[i-1].PageLink()
			}
			if i+1 < len(gallery.Images) {
				next = gallery.Images[i+1].PageLink()
			}

			CreatePage(ReplaceExt(image.Unbound, ".html"), "image.html", map[string]interface{}{
				"Title":   image.Name,
				"Gallery": gallery,
				"Image":   image,
				"Prev":    prev,
				"Next":    next,
			})
		}

		CreatePage(filepath.Join(gallery.Unbound, "index.html"), "gallery.html", map[string]interface{}{
			"Title":   gallery.Name,
			"Gallery": gallery,
		})
	}

	CreatePage("index.html", "index.html", map[string]interface{}{
		"Title":     "Galleries",
		"Galleries": galleries,
	})

	log.Println(CopyDir("css", filepath.Join("public", "css")))

	if err != nil {
		log.Fatal(err)
	}
}

func FileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func LoadImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	m, _, err := image.Decode(file)
	file.Close()

	orientation := ExifOrientation(path)
	rm := reorient(m, orientation)
	return rm, err
}

func CreatePage(name string, template string, data interface{}) {
	name = filepath.Join("public", name)

	var buffer bytes.Buffer
	err := T.ExecuteTemplate(&buffer, template, data)
	if err != nil {
		log.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(name), 0755)
	ioutil.WriteFile(name, buffer.Bytes(), 0755)
}

func Downscale(m image.Image, maxwidth int) image.Image {
	if m.Bounds().Dx() <= maxwidth {
		return m
	}

	targetSize := image.Point{0, maxwidth}
	targetSize.X = m.Bounds().Dx() * maxwidth / m.Bounds().Dy()
	inner := image.Rectangle{image.ZP, targetSize}
	rgba := image.NewRGBA(inner)
	draw.CatmullRom.Scale(rgba, rgba.Bounds(), m, m.Bounds(), draw.Over, nil)
	return rgba
}

func SaveJPG(m image.Image, path string) error {
	os.MkdirAll(filepath.Dir(path), 0755)
	path = ReplaceExt(path, ".jpg")

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return jpeg.Encode(file, m, &jpeg.Options{Quality: 93})
}

func SavePNG(m image.Image, path string) error {
	os.MkdirAll(filepath.Dir(path), 0755)
	path = ReplaceExt(path, ".png")

	path = path[:len(path)-len(filepath.Ext(path))] + ".png"
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return png.Encode(file, m)
}

func ReplaceExt(path, ext string) string {
	return path[:len(path)-len(filepath.Ext(path))] + ext
}

func CopyDir(src string, dst string) (err error) {
	srcinfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	err = os.MkdirAll(dst, srcinfo.Mode())
	if err != nil {
		return err
	}

	dir, _ := os.Open(src)
	infos, err := dir.Readdir(-1)
	if err != nil {
		return err
	}

	for _, info := range infos {
		srcname := filepath.Join(src, info.Name())
		dstname := filepath.Join(dst, info.Name())

		if info.IsDir() {
			err = CopyDir(srcname, dstname)
			if err != nil {
				return err
			}
		} else {
			err = CopyFile(srcname, dstname)
			if err != nil {
				return err
			}
		}
	}
	return
}

func CopyFile(src, dst string) (err error) {
	srcf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcf.Close()

	dstf, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstf.Close()

	_, err = io.Copy(dstf, srcf)
	if err == nil {
		srcinfo, err := os.Stat(src)
		if err != nil {
			err = os.Chmod(dst, srcinfo.Mode())
		}
	}
	return
}

func ExifOrientation(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return topLeftSide
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil || x == nil {
		return topLeftSide
	}

	orient, err := x.Get(exif.Orientation)
	if err != nil || orient == nil {
		return topLeftSide
	}

	v, err := orient.Int(0)
	if err != nil {
		return topLeftSide
	}

	return v
}

// Exif Orientation Tag values
// http://sylvana.net/jpegcrop/exif_orientation.html
const (
	topLeftSide     = 1
	topRightSide    = 2
	bottomRightSide = 3
	bottomLeftSide  = 4
	leftSideTop     = 5
	rightSideTop    = 6
	rightSideBottom = 7
	leftSideBottom  = 8
)

func reorient(img image.Image, orient int) *image.NRGBA {
	switch orient {
	case topLeftSide:
		return imaging.Clone(img)
	case topRightSide:
		return imaging.FlipV(img)
	case bottomRightSide:
		return imaging.Rotate180(img)
	case bottomLeftSide:
		return imaging.Rotate180(imaging.FlipV(img))
	case leftSideTop:
		return imaging.Rotate270(imaging.FlipV(img))
	case rightSideTop:
		return imaging.Rotate270(img)
	case rightSideBottom:
		return imaging.Rotate90(imaging.FlipV(img))
	case leftSideBottom:
		return imaging.Rotate90(img)
	}
	return imaging.Clone(img)
}
