package nextcore

import (
	"strings"
)

// DetectedFeatures holds everything Caddy needs to know about the app
type DetectedFeatures struct {
	HasYouTube         bool
	HasGoogleFonts     bool
	HasGoogleAnalytics bool
	HasStripe          bool
	HasCloudinary      bool
	HasExternalImages  []string // all external image hostnames
	HasServerActions   bool
	HasI18n            bool
	UserDefinedCSP     bool     // user already set CSP in their next.config headers
	AllowedOrigins     []string // all trusted origins detected
	DistDir            string   // path to build output (default: .next)
	ExportDir          string   // path to export output (default: out)
}

// DetectFeatures inspects a NextConfig and returns what external services
// the app uses — this drives the dynamic CSP generation in Caddy
func DetectFeatures(config *NextConfig) *DetectedFeatures {
	if config == nil {
		return &DetectedFeatures{
			DistDir:   ".next",
			ExportDir: "out",
		}
	}

	f := &DetectedFeatures{}

	// --- image domains & remote patterns ---
	if config.Images != nil {
		for _, domain := range config.Images.Domains {
			f.AllowedOrigins = append(f.AllowedOrigins, domain)
			f.HasExternalImages = append(f.HasExternalImages, domain)
			detectWellKnownService(f, domain)
		}
		for _, pattern := range config.Images.RemotePatterns {
			host := pattern.Hostname
			f.AllowedOrigins = append(f.AllowedOrigins, host)
			f.HasExternalImages = append(f.HasExternalImages, host)
			detectWellKnownService(f, host)
		}
	}

	// --- experimental flags ---
	if config.Experimental != nil {
		if config.Experimental.ServerActions {
			f.HasServerActions = true
		}
	}

	// --- i18n ---
	if config.I18n != nil {
		f.HasI18n = true
	}

	// --- check if user already set a CSP in headers ---
	// if so, we should NOT override it in Caddy
	for _, h := range config.Headers {
		if hMap, ok := h.(map[string]interface{}); ok {
			if headers, ok := hMap["headers"].([]interface{}); ok {
				for _, header := range headers {
					if hItem, ok := header.(map[string]interface{}); ok {
						key, _ := hItem["key"].(string)
						if strings.EqualFold(key, "content-security-policy") {
							f.UserDefinedCSP = true
						}
					}
				}
			}
		}
	}

	// --- dynamic paths ---
	f.DistDir = ".next"
	if config.DistDir != "" {
		f.DistDir = config.DistDir
	}

	f.ExportDir = "out"
	// Note: ExportDir is usually not in next.config.mjs but we can support it if added
	// next export -o [dir] is the usual way

	return f
}

// detectWellKnownService checks a hostname against known services
// and sets the relevant feature flag
func detectWellKnownService(f *DetectedFeatures, host string) {
	h := strings.ToLower(host)

	switch {
	case strings.Contains(h, "youtube.com") || strings.Contains(h, "ytimg.com") || strings.Contains(h, "youtu.be"):
		f.HasYouTube = true

	case strings.Contains(h, "googleapis.com") || strings.Contains(h, "gstatic.com") || strings.Contains(h, "fonts.google.com"):
		f.HasGoogleFonts = true

	case strings.Contains(h, "google-analytics.com") || strings.Contains(h, "googletagmanager.com"):
		f.HasGoogleAnalytics = true

	case strings.Contains(h, "stripe.com") || strings.Contains(h, "stripe.network"):
		f.HasStripe = true

	case strings.Contains(h, "cloudinary.com") || strings.Contains(h, "res.cloudinary.com"):
		f.HasCloudinary = true
	}
}

// BuildCSP generates a Content-Security-Policy header value
// based on what the app actually uses
func BuildCSP(f *DetectedFeatures) string {
	if f == nil || f.UserDefinedCSP {
		return defaultCSP()
	}

	defaultSrc := []string{"'self'"}
	scriptSrc := []string{"'self'", "'unsafe-inline'", "'unsafe-eval'"}
	styleSrc := []string{"'self'", "'unsafe-inline'"}
	imgSrc := []string{"'self'", "data:"}
	frameSrc := []string{"'self'"}
	connectSrc := []string{"'self'"}
	fontSrc := []string{"'self'"}
	mediaSrc := []string{"'self'"}

	// YouTube
	if f.HasYouTube {
		frameSrc = append(frameSrc, "https://www.youtube.com", "https://www.youtube-nocookie.com")
		imgSrc = append(imgSrc, "https://i.ytimg.com", "https://img.youtube.com")
		scriptSrc = append(scriptSrc, "https://www.youtube.com")
		connectSrc = append(connectSrc, "https://www.youtube.com")
		mediaSrc = append(mediaSrc, "https://www.youtube.com")
	}

	// Google Fonts
	if f.HasGoogleFonts {
		styleSrc = append(styleSrc, "https://fonts.googleapis.com")
		fontSrc = append(fontSrc, "https://fonts.gstatic.com")
	}

	// Google Analytics
	if f.HasGoogleAnalytics {
		scriptSrc = append(scriptSrc, "https://www.googletagmanager.com", "https://www.google-analytics.com")
		connectSrc = append(connectSrc, "https://www.google-analytics.com", "https://analytics.google.com")
		imgSrc = append(imgSrc, "https://www.google-analytics.com")
	}

	// Stripe
	if f.HasStripe {
		scriptSrc = append(scriptSrc, "https://js.stripe.com")
		frameSrc = append(frameSrc, "https://js.stripe.com", "https://hooks.stripe.com")
		connectSrc = append(connectSrc, "https://api.stripe.com")
	}

	// Cloudinary
	if f.HasCloudinary {
		imgSrc = append(imgSrc, "https://res.cloudinary.com")
		connectSrc = append(connectSrc, "https://res.cloudinary.com")
	}

	// any other external image hosts
	for _, origin := range f.HasExternalImages {
		host := "https://" + origin
		// avoid duplicates from well known services
		if !contains(imgSrc, host) {
			imgSrc = append(imgSrc, host)
			connectSrc = append(connectSrc, host)
		}
	}

	return strings.Join([]string{
		"default-src " + strings.Join(defaultSrc, " "),
		"script-src " + strings.Join(scriptSrc, " "),
		"style-src " + strings.Join(styleSrc, " "),
		"img-src " + strings.Join(imgSrc, " "),
		"frame-src " + strings.Join(frameSrc, " "),
		"connect-src " + strings.Join(connectSrc, " "),
		"font-src " + strings.Join(fontSrc, " "),
		"media-src " + strings.Join(mediaSrc, " "),
	}, "; ") + ";"
}

func defaultCSP() string {
	return "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self';"
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
