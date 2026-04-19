package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/disintegration/imaging"
)

type ImageOptConfig struct {
	AllowedDomains []string  `json:"allowed_domains"`
	RemotePatterns []Pattern `json:"remote_patterns"`
	DeviceSizes    []int     `json:"device_sizes"`
	ImageSizes     []int     `json:"image_sizes"`
	Formats        []string  `json:"formats"`
}

type Pattern struct {
	Protocol string `json:"protocol"`
	Hostname string `json:"hostname"`
	Port     string `json:"port"`
	Pathname string `json:"pathname"`
}

var (
	sourceBucket string
	imageConfig  ImageOptConfig
	s3Client     *s3.Client
)

func init() {
	sourceBucket = os.Getenv("SOURCE_BUCKET")

	// Parse image config from metadata
	if configJSON := os.Getenv("IMAGE_CONFIG_JSON"); configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), &imageConfig); err != nil {
			fmt.Printf("Warning: failed to parse IMAGE_CONFIG_JSON: %v\n", err)
		}
	}

	// Fallback to legacy ALLOWED_DOMAINS
	if len(imageConfig.AllowedDomains) == 0 {
		if dm := os.Getenv("ALLOWED_DOMAINS"); dm != "" {
			imageConfig.AllowedDomains = strings.Split(dm, ",")
		}
	}

	// Default device sizes if not configured
	if len(imageConfig.DeviceSizes) == 0 {
		imageConfig.DeviceSizes = []int{640, 750, 828, 1080, 1200, 1920, 2048, 3840}
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err == nil {
		s3Client = s3.NewFromConfig(cfg)
	} else {
		fmt.Printf("Warning: failed to initialize AWS config: %v\n", err)
	}
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	params := req.QueryStringParameters
	rawURL := params["url"]
	width, _ := strconv.Atoi(params["w"])
	quality, _ := strconv.Atoi(params["q"])

	if quality == 0 {
		quality = 75
	}
	if rawURL == "" {
		return errResponse(400, "Missing 'url' parameter"), nil
	}

	// Validate width against configured device sizes
	if width > 0 && len(imageConfig.DeviceSizes) > 0 {
		validWidth := false
		for _, size := range imageConfig.DeviceSizes {
			if width == size {
				validWidth = true
				break
			}
		}
		if !validWidth {
			return errResponse(400, fmt.Sprintf("Invalid width %d", width)), nil
		}
	}

	// 1. Fetch source image
	src, format, err := fetchImage(ctx, rawURL)
	if err != nil {
		fmt.Printf("Error fetching image %s: %v\n", rawURL, err)
		return errResponse(404, "could not fetch image"), nil
	}

	// 2. Resize to requested width, preserve aspect ratio
	if width > 0 {
		src = imaging.Resize(src, width, 0, imaging.Lanczos)
	}

	// 3. Encode (fallback to JPEG/PNG since pure go webp encoding is complex for this scope)
	var buf bytes.Buffer
	contentType := "image/jpeg"

	// Fast safe pure go fallback.
	if format == "png" {
		contentType = "image/png"
		err = png.Encode(&buf, src)
	} else {
		err = jpeg.Encode(&buf, src, &jpeg.Options{Quality: quality})
	}

	if err != nil {
		fmt.Printf("Error encoding image: %v\n", err)
		return errResponse(500, "could not encode image"), nil
	}

	// 4. Return base64 body
	return events.APIGatewayV2HTTPResponse{
		StatusCode:      200,
		IsBase64Encoded: true,
		Headers: map[string]string{
			"Content-Type":  contentType,
			"Cache-Control": "public, max-age=31536000, immutable",
		},
		Body: base64.StdEncoding.EncodeToString(buf.Bytes()),
	}, nil
}

func fetchImage(ctx context.Context, rawURL string) (image.Image, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", err
	}

	if parsed.Host == "" {
		// It's a relative URL, fetch directly from S3 using SDK instead of public HTTP
		if s3Client == nil {
			return nil, "", fmt.Errorf("s3 client is not initialized")
		}

		objectKey := strings.TrimPrefix(rawURL, "/")
		resp, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(sourceBucket),
			Key:    aws.String(objectKey),
		})
		if err != nil {
			return nil, "", fmt.Errorf("failed to get S3 object: %w", err)
		}
		defer resp.Body.Close()
		return image.Decode(resp.Body)
	}

	// It's an external URL, validate against allowed domains and remote patterns
	if !isAllowedDomain(parsed.Host, parsed.Scheme) {
		return nil, "", fmt.Errorf("domain %s is not permitted", parsed.Host)
	}

	resp, err := http.Get(rawURL)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	img, format, err := image.Decode(resp.Body)
	return img, format, err
}

func isAllowedDomain(host, scheme string) bool {
	// Check allowed domains list
	for _, d := range imageConfig.AllowedDomains {
		if host == d || host == strings.TrimSpace(d) {
			return true
		}
	}

	// Check remote patterns
	for _, pattern := range imageConfig.RemotePatterns {
		if pattern.Protocol != "" && pattern.Protocol != scheme {
			continue
		}
		if pattern.Hostname != "" && !matchHostname(host, pattern.Hostname) {
			continue
		}
		return true
	}

	// If no config, deny by default
	return len(imageConfig.AllowedDomains) == 0 && len(imageConfig.RemotePatterns) == 0
}

func matchHostname(host, pattern string) bool {
	// Support wildcards like *.example.com
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // .example.com
		return strings.HasSuffix(host, suffix)
	}
	return host == pattern
}

func errResponse(code int, msg string) events.APIGatewayV2HTTPResponse {
	return events.APIGatewayV2HTTPResponse{
		StatusCode: code,
		Headers: map[string]string{
			"Content-Type": "text/plain",
		},
		Body: msg,
	}
}

func main() {
	lambda.Start(handler)
}
