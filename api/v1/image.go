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

type countingReader struct {
	reader       io.Reader
	bytesRead    int64
	maxImageSize int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.reader.Read(p)
	cr.bytesRead += int64(n)
	if cr.bytesRead > cr.maxImageSize {
		return n, fmt.Errorf("image size exceeds the allowed limit")
	}
	return n, err
}

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

	targetFormat, err := parseImageFormat(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sharpenAmount, blurAmount, err := parseSharpenBlur(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	upscale := r.URL.Query().Get("up") == "true"
	stripMetadata := r.URL.Query().Get("strip") == "true"

	convertToWebP := convertImageToWebP(r)

	client := &http.Client{}
	req, err := http.NewRequest("GET", targetUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.Header.Set("User-Agent", "image-gem/v1.0")
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Check for HTTP status code
	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("Received a %d status code from the server", resp.StatusCode), resp.StatusCode)
		return
	}

	// Check for the content type
	contentType := resp.Header.Get("Content-Type")
	if !isSupportedImageFormat(contentType) {
		http.Error(w, "Unsupported image format", http.StatusBadRequest)
		return
	}

	// Limit the size of the input image
	countingReader := &countingReader{reader: resp.Body, maxImageSize: maxImageSize}

	// Check if there are any query parameters
	hasQueryParams := len(r.URL.RawQuery) > 0

	// If there are no query parameters, write the original image data directly to the response and return
	// If the content type is SVG, write it directly to the response and return. SVGs should be handled in HTML or CSS, not here
	if !hasQueryParams || contentType == "image/svg+xml" {
		w.Header().Set("Content-Type", contentType)
		_, err := io.Copy(w, countingReader)
		if err != nil {
			http.Error(w, "Failed to process image", http.StatusInternalServerError)
			return
		}
		return
	}

	img, err := vips.NewImageFromReader(countingReader)
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
		"image/svg+xml",
		"image/webp",
		"image/heic",
		"image/heif",
		"image/tiff",
		"image/tif",
		"image/avif",
		"image/jp2",
		"image/j2k",
	}

	for _, format := range supportedFormats {
		if contentType == format {
			return true
		}
	}
	return false
}

func normalizeURL(url string) string {
	url = html.EscapeString(url)
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

func parseImageFormat(r *http.Request) (vips.ImageType, error) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = r.URL.Query().Get("f")
	}

	switch strings.ToLower(format) {
	case "":
		return vips.ImageTypeUnknown, nil
	case "jpeg", "jpg":
		return vips.ImageTypeJPEG, nil
	case "png":
		return vips.ImageTypePNG, nil
	case "webp":
		return vips.ImageTypeWEBP, nil
	case "heif", "heic":
		return vips.ImageTypeHEIF, nil
	case "tiff", "tif":
		return vips.ImageTypeTIFF, nil
	case "avif":
		return vips.ImageTypeAVIF, nil
	case "jp2k", "j2k":
		return vips.ImageTypeJP2K, nil
	case "gif":
		return vips.ImageTypeGIF, nil
	default:
		return vips.ImageTypeUnknown, fmt.Errorf("unsupported image format: %s", format)
	}
}
