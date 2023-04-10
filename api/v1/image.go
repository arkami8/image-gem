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

	sharpenAmount, blurAmount, err := parseSharpenBlur(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	convertToWebP := convertImageToWebP(r)

	resp, err := http.Get(targetUrl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Limit the size of the input image
	limitedReader := io.LimitReader(resp.Body, maxImageSize)

	img, err := vips.NewImageFromReader(limitedReader)
	if err != nil {
		http.Error(w, "Failed to decode image", http.StatusBadRequest)
		return
	}
	defer img.Close()

	if blurAmount > 0 {
		if err := img.GaussianBlur(blurAmount); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if height > 0 || width > 0 {
		img, err = resizeImage(img, width, height)
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

	// Export the correct image format
	var imgBytes []byte
	if convertToWebP {
		w.Header().Set("Content-Type", "image/webp")
		imgBytes, _, err = img.ExportWebp(vips.NewWebpExportParams())
	} else {
		w.Header().Set("Content-Type", "image/"+strings.TrimPrefix(img.Format().FileExt(), "."))
		imgBytes, _, err = img.ExportNative()
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, _ = w.Write(imgBytes)
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

func resizeImage(img *vips.ImageRef, width, height int) (*vips.ImageRef, error) {
	if width == 0 && height == 0 {
		return img, nil
	}

	if width == 0 {
		scale := float64(height) / float64(img.Height())
		err := img.Resize(scale, vips.KernelAuto)
		if err != nil {
			return nil, err
		}
		return img, nil
	}

	if height == 0 {
		scale := float64(width) / float64(img.Width())
		err := img.Resize(scale, vips.KernelAuto)
		if err != nil {
			return nil, err
		}
		return img, nil
	}

	if img.Width() > width || img.Height() > height {
		hScale := float64(width) / float64(img.Width())
		vScale := float64(height) / float64(img.Height())
		err := img.ResizeWithVScale(hScale, vScale, vips.KernelAuto)
		if err != nil {
			return nil, err
		}
	}

	return img, nil
}
