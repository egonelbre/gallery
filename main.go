package main

import (
	"bytes"
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
	"sort"
	"strings"

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

const thumbsize = 256

var T = template.Must(template.ParseGlob("*.html"))

func main() {
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

		for _, image := range gallery.Images {
			m, err := LoadImage(image.Path)
			if err != nil {
				log.Println(err)
				continue
			}

			t := CreateThumbnail(m)
			image.Thumb = filepath.Join("thumbs", ReplaceExt(image.Unbound, ".png"))
			SavePNG(t, filepath.Join("public", image.Thumb))

			CreatePage(ReplaceExt(image.Unbound, ".html"), "image.html", map[string]interface{}{
				"Title":   image.Name,
				"Gallery": gallery,
				"Image":   image,
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

	log.Println(CopyDir(imagesDir, filepath.Join("public", imagesDir)))
	log.Println(CopyDir("css", filepath.Join("public", "css")))

	if err != nil {
		log.Fatal(err)
	}
}

func LoadImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	m, _, err := image.Decode(file)
	return m, err
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

func CreateThumbnail(m image.Image) image.Image {
	targetSize := image.Point{0, thumbsize}
	targetSize.X = m.Bounds().Dx() * thumbsize / m.Bounds().Dy()
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
	return jpeg.Encode(file, m, &jpeg.Options{Quality: 90})
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
