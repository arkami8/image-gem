package v1

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/gorilla/mux"
)

const (
	maxImageSize = 5 * 1024 * 1024 // 5MB
)

func ImageGet(w http.ResponseWriter, r *http.Request) {
	slugs := mux.Vars(r)
	targetUrl := normalizeURL(slugs["url"])

	height, width, err := parseDimensions(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	quality, err := parseQuality(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sharpenAmount, blurAmount, err := parseSharpenBlur(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	upscale := r.URL.Query().Get("upscale") == "true"
	stripMetadata := r.URL.Query().Get("strip") == "true"

	convertToWebP := convertImageToWebP(r)

	resp, err := http.Get(targetUrl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if !isSupportedImageFormat(contentType) {
		http.Error(w, "Unsupported image format", http.StatusBadRequest)
		return
	}

	// Limit the size of the input image
	limitedReader := io.LimitReader(resp.Body, maxImageSize)

	img, err := vips.NewImageFromReader(limitedReader)
	if err != nil {
		http.Error(w, "Failed to decode image", http.StatusBadRequest)
		return
	}
	defer img.Close()

	if stripMetadata {
		err := img.RemoveMetadata()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if blurAmount > 0 {
		if err := img.GaussianBlur(blurAmount); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if height > 0 || width > 0 {
		img, err = resizeImage(img, width, height, upscale)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if sharpenAmount > 0 {
		if err := img.Sharpen(sharpenAmount, 0.6, 1.0); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	targetFormat := vips.ImageTypeUnknown
	if convertToWebP {
		targetFormat = vips.ImageTypeWEBP
	}
	imgBytes, _, err := ExportImage(img, quality, targetFormat)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(imgBytes)
}

func isSupportedImageFormat(contentType string) bool {
	supportedFormats := []string{
		"image/jpeg",
		"image/png",
		"image/gif",
	}

	for _, format := range supportedFormats {
		if contentType == format {
			return true
		}
	}
	return false
}

func normalizeURL(url string) string {
	url = strings.ToLower(html.EscapeString(url))
	url = strings.ReplaceAll(url, "http:/", "http://")
	url = strings.ReplaceAll(url, "https:/", "https://")
	if !strings.Contains(url, "http:") && !strings.Contains(url, "https:") {
		url = "https://" + url
	}
	return url
}

func parseDimensions(r *http.Request) (int, int, error) {
	height, err := parseIntQueryParam(r, "h", "height")
	if err != nil {
		return 0, 0, err
	}
	width, err := parseIntQueryParam(r, "w", "width")
	if err != nil {
		return 0, 0, err
	}
	return height, width, nil
}

func parseQuality(r *http.Request) (int, error) {
	quality, err := parseIntQueryParam(r, "q", "quality")
	if err != nil {
		return 0, err
	}

	if quality < 0 || quality > 100 {
		return 0, fmt.Errorf("quality must be between 1 and 100")
	}

	return quality, nil
}

func parseSharpenBlur(r *http.Request) (float64, float64, error) {
	sharpen, err := parseFloatQueryParam(r, "sharpen", "s")
	if err != nil {
		return 0, 0, err
	}
	blur, err := parseFloatQueryParam(r, "blur", "b")
	if err != nil {
		return 0, 0, err
	}
	return sharpen, blur, nil
}

func parseIntQueryParam(r *http.Request, keys ...string) (int, error) {
	for _, key := range keys {
		value := r.URL.Query().Get(key)
		if value != "" {
			num, err := strconv.Atoi(value)
			if err != nil {
				return 0, fmt.Errorf("invalid value for %s: %v", key, err)
			}
			return num, nil
		}
	}
	return 0, nil
}

func parseFloatQueryParam(r *http.Request, keys ...string) (float64, error) {
	for _, key := range keys {
		value := r.URL.Query().Get(key)
		if value != "" {
			num, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid value for %s: %v", key, err)
			}
			return num, nil
		}
	}
	return 0, nil
}

func convertImageToWebP(r *http.Request) bool {
	webpQueryParam := r.URL.Query().Get("webp")

	if webpQueryParam == "force" {
		return true
	}

	if webpQueryParam == "auto" {
		return isWebPSupported(r.Header.Get("Accept"))
	}

	return false
}

func isWebPSupported(acceptHeader string) bool {
	return strings.Contains(acceptHeader, "image/webp")
}

func resizeImage(img *vips.ImageRef, width, height int, upscale bool) (*vips.ImageRef, error) {
	if width == 0 && height == 0 {
		return img, nil
	}

	if width == 0 {
		scale := float64(height) / float64(img.Height())
		if upscale || scale <= 1 {
			err := img.Resize(scale, vips.KernelAuto)
			if err != nil {
				return nil, err
			}
		}
		return img, nil
	}

	if height == 0 {
		scale := float64(width) / float64(img.Width())
		if upscale || scale <= 1 {
			err := img.Resize(scale, vips.KernelAuto)
			if err != nil {
				return nil, err
			}
		}
		return img, nil
	}

	hScale := float64(width) / float64(img.Width())
	vScale := float64(height) / float64(img.Height())
	if upscale || (hScale <= 1 && vScale <= 1) {
		err := img.ResizeWithVScale(hScale, vScale, vips.KernelAuto)
		if err != nil {
			return nil, err
		}
	}

	return img, nil
}

func ExportImage(img *vips.ImageRef, quality int, formats ...vips.ImageType) ([]byte, *vips.ImageMetadata, error) {
	format := img.Format()
	if len(formats) > 0 {
		format = formats[0]
	}

	switch format {
	case vips.ImageTypeJPEG:
		params := vips.NewJpegExportParams()
		if quality >= 1 && quality <= 100 {
			params.Quality = quality
		}
		return img.ExportJpeg(params)
	case vips.ImageTypePNG:
		return img.ExportPng(vips.NewPngExportParams())
	case vips.ImageTypeWEBP:
		params := vips.NewWebpExportParams()
		if quality >= 1 && quality <= 100 {
			params.Quality = quality
		}
		return img.ExportWebp(params)
	case vips.ImageTypeHEIF:
		params := vips.NewHeifExportParams()
		if quality >= 1 && quality <= 100 {
			params.Quality = quality
		}
		return img.ExportHeif(params)
	case vips.ImageTypeTIFF:
		return img.ExportTiff(vips.NewTiffExportParams())
	case vips.ImageTypeAVIF:
		params := vips.NewAvifExportParams()
		if quality >= 1 && quality <= 100 {
			params.Quality = quality
		}
		return img.ExportAvif(params)
	case vips.ImageTypeJP2K:
		params := vips.NewJp2kExportParams()
		if quality >= 1 && quality <= 100 {
			params.Quality = quality
		}
		return img.ExportJp2k(params)
	case vips.ImageTypeGIF:
		params := vips.NewGifExportParams()
		if quality >= 1 && quality <= 100 {
			params.Quality = quality
		}
		return img.ExportGIF(params)
	default:
		return img.ExportNative()
	}
}