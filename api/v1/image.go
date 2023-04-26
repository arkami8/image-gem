package v1

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/gorilla/mux"
)

const (
	// maxImageSize is the maximum allowed image size in bytes.
	maxImageSize = 5 * 1024 * 1024 // 5MB

	// maxImageHeight is the maximum allowed image height in pixels.
	maxImageHeight = 20000

	// maxImageHeight is the maximum allowed image width in pixels.
	maxImageWidth = 20000
)

// countingReader is a struct that wraps an io.Reader and counts the number of bytes read,
// checking if it exceeds the maximum allowed image size.
type countingReader struct {
	reader       io.Reader
	bytesRead    int64
	maxImageSize int64
}

// Read reads from the underlying reader and increments the byte counter,
// returning an error if the image size limit is exceeded.
func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.reader.Read(p)
	cr.bytesRead += int64(n)
	if cr.bytesRead > cr.maxImageSize {
		return n, fmt.Errorf("image size exceeds the allowed limit")
	}
	return n, err
}

// ImageGet is an HTTP handler function for processing and transforming images based on URL query parameters.
// It supports image resizing, rotation, blurring, sharpening, and format conversion, as well as stripping metadata.
func ImageGet(w http.ResponseWriter, r *http.Request) {
	slugs := mux.Vars(r)
	targetUrl, err := normalizeURL(slugs["url"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	height, width, err := parseDimensions(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rotation, err := parseRotation(r)
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

	sharpenAmount, err := parseSharpen(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	blurAmount, err := parseBlur(r)
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

	var img *vips.ImageRef
	if contentType == "image/gif" {
		data, err := io.ReadAll(countingReader)
		if err != nil {
			http.Error(w, "Failed to decode image", http.StatusBadRequest)
			return
		}

		intSet := vips.IntParameter{}
		intSet.Set(-1)

		params := vips.NewImportParams()
		params.NumPages = intSet

		img, err = vips.LoadImageFromBuffer(data, params)
		if err != nil {
			http.Error(w, "Failed to decode image", http.StatusBadRequest)
			return
		}
		targetFormat = vips.ImageTypeGIF
	} else {
		img, err = vips.NewImageFromReader(countingReader)
		if err != nil {
			http.Error(w, "Failed to decode image", http.StatusBadRequest)
			return
		}
	}
	defer img.Close()

	if rotation != 0 {
		// Check if the image has an alpha channel and add one if it's missing
		if !img.HasAlpha() {
			err := img.BandJoinConst([]float64{255})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Rotate the image
		err := img.Similarity(1.0, float64(rotation), &vips.ColorRGBA{R: 0, G: 0, B: 0, A: 0}, 0, 0, 0, 0)
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

	if stripMetadata {
		err := img.RemoveMetadata()
		if err != nil {
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

// Helper functions for checking supported image formats, normalizing URLs,
// parsing dimensions, rotations, quality, sharpening, blurring, and converting images.

func isSupportedImageFormat(contentType string) bool {
	supportedFormats := map[string]bool{
		"image/jpeg":    true,
		"image/png":     true,
		"image/gif":     true,
		"image/svg+xml": true,
		"image/webp":    true,
		"image/heic":    true,
		"image/heif":    true,
		"image/tiff":    true,
		"image/tif":     true,
		"image/avif":    true,
		"image/jp2":     true,
		"image/j2k":     true,
	}

	return supportedFormats[contentType]
}

func normalizeURL(inputURL string) (string, error) {
	// Add the scheme if it's missing
	if !strings.HasPrefix(inputURL, "http://") && !strings.HasPrefix(inputURL, "https://") {
		inputURL = "https://" + inputURL
	}

	// Parse the URL
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return "", err
	}

	// Make sure the URL has a valid scheme
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme: %s", parsedURL.Scheme)
	}

	return parsedURL.String(), nil
}

func parseDimensions(r *http.Request) (int, int, error) {
	height, err := parseIntQueryParam(r, 0, maxImageHeight, "h", "height")
	if err != nil {
		return 0, 0, err
	}
	width, err := parseIntQueryParam(r, 0, maxImageWidth, "w", "width")
	if err != nil {
		return 0, 0, err
	}
	return height, width, nil
}

func parseRotation(r *http.Request) (int, error) {
	rotation, err := parseIntQueryParam(r, 0, 360, "rotate", "r")
	if err != nil {
		return 0, err
	}
	return rotation, nil
}

func parseQuality(r *http.Request) (int, error) {
	quality, err := parseIntQueryParam(r, 1, 100, "q", "quality")
	if err != nil {
		return 0, err
	}
	return quality, nil
}

func parseIntQueryParam(r *http.Request, min, max int, keys ...string) (int, error) {
	for _, key := range keys {
		value := r.URL.Query().Get(key)
		if value != "" {
			num, err := strconv.Atoi(value)
			if err != nil {
				return 0, fmt.Errorf("invalid value for %s: %v (input: %s)", key, err, value)
			}
			if num < min || num > max {
				return 0, fmt.Errorf("value for %s must be between %d and %d (input: %d)", key, min, max, num)
			}
			return num, nil
		}
	}
	return 0, nil
}

func parseSharpen(r *http.Request) (float64, error) {
	return parseFloatQueryParam(r, 0, 1, "sharpen", "s")
}

func parseBlur(r *http.Request) (float64, error) {
	return parseFloatQueryParam(r, 0, 1, "blur", "b")
}

func parseFloatQueryParam(r *http.Request, min, max float64, keys ...string) (float64, error) {
	for _, key := range keys {
		value := r.URL.Query().Get(key)
		if value != "" {
			num, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid value for %s: %v (input: %s)", key, err, value)
			}
			if num < min || num > max {
				return 0, fmt.Errorf("value for %s must be between %f and %f (input: %f)", key, min, max, num)
			}
			return num, nil
		}
	}
	return 0, nil
}

func convertImageToWebP(r *http.Request) bool {
	if r.URL.Query().Get("webp") != "auto" {
		return false
	}

	return strings.Contains(r.Header.Get("Accept"), "image/webp")
}

func resizeImage(img *vips.ImageRef, width, height int, upscale bool) (*vips.ImageRef, error) {
	if width == 0 && height == 0 {
		return img, nil
	}

	scale := -1.0
	if width == 0 && height != 0 {
		scale = float64(height) / float64(img.PageHeight())
	}
	if height == 0 && width != 0 {
		scale = float64(width) / float64(img.Width())
	}

	if (upscale || scale <= 1) && scale != -1.0 {
		err := img.Resize(scale, vips.KernelAuto)
		if err != nil {
			return nil, err
		}
		return img, nil
	}

	hScale := float64(width) / float64(img.Width())
	vScale := float64(height) / float64(img.PageHeight())
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
