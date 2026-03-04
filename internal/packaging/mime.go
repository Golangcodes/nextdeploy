package packaging

import (
	"path/filepath"
	"strings"
)

var mimeTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "application/javascript; charset=utf-8",
	".mjs":   "application/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".svg":   "image/svg+xml",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".txt":   "text/plain; charset=utf-8",
	".xml":   "text/xml; charset=utf-8",
	".rsc":   "text/x-component",
}

func mimeForExt(ext string) string {
	if mime, ok := mimeTypes[strings.ToLower(ext)]; ok {
		return mime
	}
	return "application/octet-stream"
}

func cacheControlForPublicFile(filename string) string {
	if isContentHashed(filename) {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600, must-revalidate"
}

func isContentHashed(filename string) bool {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	parts := strings.Split(base, ".")
	if len(parts) < 2 {
		return false
	}
	last := parts[len(parts)-1]
	if len(last) < 8 {
		return false
	}
	for _, c := range last {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
