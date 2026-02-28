package nextcore

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

func extractConfigObject(content string) (map[string]interface{}, error) {
	if configStr, err := extractExplicitConfig(content); err == nil {
		return parseConfigString(configStr)
	}
	if configStr, err := extractExportedConfig(content); err == nil {
		return parseConfigString(configStr)
	}

	return nil, fmt.Errorf("could not find valid config object in content")
}

func extractExplicitConfig(content string) (string, error) {
	start := strings.Index(content, "const nextConfig =")
	if start == -1 {
		return "", fmt.Errorf("nextConfig declaration not found")
	}
	openBrace := strings.Index(content[start:], "{")
	if openBrace == -1 {
		return "", fmt.Errorf("config object not properly formatted")
	}
	openBrace += start
	configContent, err := extractBalancedBraces(content[openBrace:])
	if err != nil {
		NextCoreLogger.Error("Failed to extract balanced braces from config content: %s", err)
		return "", fmt.Errorf("failed to extract config object: %w", err)
	}

	return normalizeToJSON(configContent), nil
}

func extractExportedConfig(content string) (string, error) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`module\.exports\s*=\s*({[\s\S]*?})\s*;`),
		regexp.MustCompile(`export\s+default\s*({[\s\S]*?})\s*;`),
	}

	for _, re := range patterns {
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			return normalizeToJSON(matches[1]), nil
		}
	}

	return "", fmt.Errorf("no exported config found")
}

func extractBalancedBraces(content string) (string, error) {
	braceCount := 1
	closeBrace := 1
	for ; closeBrace < len(content) && braceCount > 0; closeBrace++ {
		switch content[closeBrace] {
		case '{':
			braceCount++
		case '}':
			braceCount--
		}
	}

	if braceCount != 0 {
		return "", fmt.Errorf("unbalanced braces in config object")
	}

	return content[:closeBrace], nil
}

func normalizeToJSON(js string) string {
	js = strings.ReplaceAll(js, "\r\n", "\n")
	js = strings.ReplaceAll(js, "\t", " ")
	js = strings.ReplaceAll(js, "\n", "\\n")
	js = stripComments(js)
	js = strings.ReplaceAll(js, "`", `"`)
	js = strings.ReplaceAll(js, `'`, `"`)
	js = regexp.MustCompile(`,\s*([}\]])`).ReplaceAllString(js, "$1")
	js = regexp.MustCompile(`([{\[,]\s*)([a-zA-Z_][a-zA-Z0-9_]*)\s*:`).ReplaceAllString(js, `$1"$2":`)
	js = escapeFunctions(js)

	return js
}

func stripComments(code string) string {
	code = regexp.MustCompile(`/\*[\s\S]*?\*/`).ReplaceAllString(code, "")
	code = regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(code, "")
	return code
}

func escapeFunctions(js string) string {
	arrowFn := regexp.MustCompile(`(\b(?:webpack|experimental|config)\b\s*:\s*)\([^)]*\)\s*=>\s*{[^}]*}`)
	js = arrowFn.ReplaceAllStringFunc(js, func(match string) string {
		parts := strings.SplitN(match, ":", 2)
		escaped := strings.ReplaceAll(parts[1], `"`, `\"`)
		return parts[0] + `: "` + strings.TrimSpace(escaped) + `"`
	})

	regFn := regexp.MustCompile(`(\b(?:webpack|experimental|config)\b\s*:\s*)function\s*\([^)]*\)\s*{[^}]*}`)
	js = regFn.ReplaceAllStringFunc(js, func(match string) string {
		parts := strings.SplitN(match, ":", 2)
		escaped := strings.ReplaceAll(parts[1], `"`, `\"`)
		return parts[0] + `: "` + strings.TrimSpace(escaped) + `"`
	})

	return js
}

func parseConfigString(configStr string) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(configStr), &result); err != nil {
		NextCoreLogger.Error("Failed to parse config JSON: %s", err)
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	for k, v := range result {
		if str, ok := v.(string); ok {
			if strings.Contains(str, "function") || strings.Contains(str, "=>") {
				unescaped := strings.ReplaceAll(str, "\\n", "\n")
				unescaped = strings.ReplaceAll(unescaped, "\\\"", "\"")
				result[k] = unescaped
			}
		}
	}

	return result, nil
}

func transpileTypeScriptConfig(content string) string {
	content = regexp.MustCompile(`(?m)^\s*\/\*\*.*?\*\/\s*$`).ReplaceAllString(content, "")
	content = regexp.MustCompile(`:\s*\w+\s*([,;}])`).ReplaceAllString(content, "$1")
	content = regexp.MustCompile(`(?m)^\s*import\s+.*?;\s*$`).ReplaceAllString(content, "")
	return content
}

func parseEdgeRegions(content string) []string {
	regionsRegex := regexp.MustCompile(`regions:\s*(\[[^\]]+\])`)
	matches := regionsRegex.FindStringSubmatch(content)
	if len(matches) > 1 {
		cleaned := strings.ReplaceAll(matches[1], "'", `"`)
		var regions []string
		if err := json.Unmarshal([]byte(cleaned), &regions); err == nil {
			return regions
		}
	}
	return nil
}

func parseConfigObject(config map[string]interface{}) (*NextConfig, error) {
	result := &NextConfig{
		Env:                 make(map[string]string),
		PublicRuntimeConfig: make(map[string]interface{}),
		ServerRuntimeConfig: make(map[string]interface{}),
	}

	getString := func(key string) string {
		if val, ok := config[key].(string); ok {
			return val
		}
		return ""
	}

	getBool := func(key string) bool {
		if val, ok := config[key].(bool); ok {
			return val
		}
		return false
	}
	if webpack, ok := config["webpack"]; ok {
		if webpackStr, ok := webpack.(string); ok && webpackStr == "webpack_function" {
			result.Webpack = nil
		} else {
			result.Webpack = webpack
		}
	}

	getStringSlice := func(key string) []string {
		if arr, ok := config[key].([]interface{}); ok {
			var result []string
			for _, v := range arr {
				if s, ok := v.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
		return nil
	}

	result.BasePath = getString("basePath")
	result.Output = getString("output")
	result.ReactStrictMode = getBool("reactStrictMode")
	result.PoweredByHeader = getBool("poweredByHeader")
	result.TrailingSlash = getBool("trailingSlash")
	result.PageExtensions = getStringSlice("pageExtensions")
	result.AssetPrefix = getString("assetPrefix")
	result.DistDir = getString("distDir")
	result.CleanDistDir = getBool("cleanDistDir")
	result.GenerateBuildId = config["generateBuildId"]
	result.OnDemandEntries = toMap(config["onDemandEntries"])
	result.CompileOptions = toMap(config["compileOptions"])
	result.SkipMiddlewareUrlNormalize = getBool("skipMiddlewareUrlNormalize")
	result.SkipTrailingSlashRedirect = getBool("skipTrailingSlashRedirect")
	result.Webpack5 = getBool("webpack5")
	result.AnalyticsId = getString("analyticsId")
	result.MdxRs = getBool("mdxRs")
	result.EdgeRuntime = getString("edgeRuntime")
	if images, ok := config["images"].(map[string]interface{}); ok {
		result.Images = &ImageConfig{
			Domains:               getStringSliceFromMap(images, "domains"),
			Formats:               getStringSliceFromMap(images, "formats"),
			DeviceSizes:           getIntSliceFromMap(images, "deviceSizes"),
			ImageSizes:            getIntSliceFromMap(images, "imageSizes"),
			Path:                  getStringFromMap(images, "path"),
			Loader:                getStringFromMap(images, "loader"),
			LoaderFile:            getStringFromMap(images, "loaderFile"),
			MinimumCacheTTL:       getIntFromMap(images, "minimumCacheTTL"),
			Unoptimized:           getBoolFromMap(images, "unoptimized"),
			ContentSecurityPolicy: getStringFromMap(images, "contentSecurityPolicy"),
		}
		if patterns, ok := images["remotePatterns"].([]interface{}); ok {
			for _, p := range patterns {
				if pattern, ok := p.(map[string]interface{}); ok {
					result.Images.RemotePatterns = append(result.Images.RemotePatterns, ImageRemotePattern{
						Protocol: getStringFromMap(pattern, "protocol"),
						Hostname: getStringFromMap(pattern, "hostname"),
						Port:     getStringFromMap(pattern, "port"),
						Pathname: getStringFromMap(pattern, "pathname"),
					})
				}
			}
		}
	}

	if experimental, ok := config["experimental"].(map[string]interface{}); ok {
		result.Experimental = &ExperimentalConfig{
			AppDir:                            getBoolFromMap(experimental, "appDir"),
			CaseSensitiveRoutes:               getBoolFromMap(experimental, "caseSensitiveRoutes"),
			UseDeploymentId:                   getBoolFromMap(experimental, "useDeploymentId"),
			UseDeploymentIdServerActions:      getBoolFromMap(experimental, "useDeploymentIdServerActions"),
			DeploymentId:                      getStringFromMap(experimental, "deploymentId"),
			ServerComponents:                  getBoolFromMap(experimental, "serverComponents"),
			ServerActions:                     getBoolFromMap(experimental, "serverActions"),
			ServerActionsBodySizeLimit:        getIntFromMap(experimental, "serverActionsBodySizeLimit"),
			OptimizeCss:                       getBoolFromMap(experimental, "optimizeCss"),
			OptimisticClientCache:             getBoolFromMap(experimental, "optimisticClientCache"),
			ClientRouterFilter:                getBoolFromMap(experimental, "clientRouterFilter"),
			ClientRouterFilterRedirects:       getBoolFromMap(experimental, "clientRouterFilterRedirects"),
			ClientRouterFilterAllowedRate:     getFloat64FromMap(experimental, "clientRouterFilterAllowedRate"),
			ExternalDir:                       getStringFromMap(experimental, "externalDir"),
			ExternalMiddlewareRewritesResolve: getBoolFromMap(experimental, "externalMiddlewareRewritesResolve"),
			FallbackNodePolyfills:             getBoolFromMap(experimental, "fallbackNodePolyfills"),
			ForceSwcTransforms:                getBoolFromMap(experimental, "forceSwcTransforms"),
			FullySpecified:                    getBoolFromMap(experimental, "fullySpecified"),
			SwcFileReading:                    getBoolFromMap(experimental, "swcFileReading"),
			SwcMinify:                         getBoolFromMap(experimental, "swcMinify"),
			SwcPlugins:                        toSlice(experimental["swcPlugins"]),
			SwcTraceProfiling:                 getBoolFromMap(experimental, "swcTraceProfiling"),
			Turbo:                             toMap(experimental["turbo"]),
			Turbotrace:                        toMap(experimental["turbotrace"]),
			ScrollRestoration:                 getBoolFromMap(experimental, "scrollRestoration"),
			NewNextLinkBehavior:               getBoolFromMap(experimental, "newNextLinkBehavior"),
			ManualClientBasePath:              getBoolFromMap(experimental, "manualClientBasePath"),
			LegacyBrowsers:                    getBoolFromMap(experimental, "legacyBrowsers"),
			DisableOptimizedLoading:           getBoolFromMap(experimental, "disableOptimizedLoading"),
			GzipSize:                          getBoolFromMap(experimental, "gzipSize"),
			SharedPool:                        getBoolFromMap(experimental, "sharedPool"),
			WebVitalsAttribution:              getStringSliceFromMap(experimental, "webVitalsAttribution"),
			InstrumentationHook:               getStringFromMap(experimental, "instrumentationHook"),
		}
	}

	if i18n, ok := config["i18n"].(map[string]interface{}); ok {
		var domains []Domain
		if i18nDomains, ok := i18n["domains"].([]interface{}); ok {
			for _, d := range i18nDomains {
				if domain, ok := d.(map[string]interface{}); ok {
					domains = append(domains, Domain{
						Domain:        getStringFromMap(domain, "domain"),
						Locales:       getStringSliceFromMap(domain, "locales"),
						DefaultLocale: getStringFromMap(domain, "defaultLocale"),
					})
				}
			}
		}
		result.I18n = &I18nConfig{
			Locales:         getStringSliceFromMap(i18n, "locales"),
			DefaultLocale:   getStringFromMap(i18n, "defaultLocale"),
			Domains:         domains,
			LocaleDetection: getBoolFromMap(i18n, "localeDetection"),
		}
	}

	if env, ok := config["env"].(map[string]interface{}); ok {
		for k, v := range env {
			if s, ok := v.(string); ok {
				result.Env[k] = s
			}
		}
	}

	if headers, ok := config["headers"].([]interface{}); ok {
		result.Headers = headers
	}

	if redirects, ok := config["redirects"].([]interface{}); ok {
		result.Redirects = redirects
	}

	if rewrites, ok := config["rewrites"].([]interface{}); ok {
		result.Rewrites = rewrites
	}

	if publicRuntimeConfig, ok := config["publicRuntimeConfig"].(map[string]interface{}); ok {
		result.PublicRuntimeConfig = publicRuntimeConfig
	}

	if serverRuntimeConfig, ok := config["serverRuntimeConfig"].(map[string]interface{}); ok {
		result.ServerRuntimeConfig = serverRuntimeConfig
	}

	if webpack, ok := config["webpack"]; ok {
		result.Webpack = webpack
	}

	return result, nil
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getBoolFromMap(m map[string]interface{}, key string) bool {
	if val, ok := m[key].(bool); ok {
		return val
	}
	return false
}

func getIntFromMap(m map[string]interface{}, key string) int {
	if val, ok := m[key].(int); ok {
		return val
	}
	if val, ok := m[key].(float64); ok {
		return int(val)
	}
	return 0
}

func getFloat64FromMap(m map[string]interface{}, key string) float64 {
	if val, ok := m[key].(float64); ok {
		return val
	}
	return 0
}

func getStringSliceFromMap(m map[string]interface{}, key string) []string {
	if arr, ok := m[key].([]interface{}); ok {
		var result []string
		for _, v := range arr {
			if s, ok := v.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func getIntSliceFromMap(m map[string]interface{}, key string) []int {
	if arr, ok := m[key].([]interface{}); ok {
		var result []int
		for _, v := range arr {
			if i, ok := v.(int); ok {
				result = append(result, i)
			} else if f, ok := v.(float64); ok {
				result = append(result, int(f))
			}
		}
		return result
	}
	return nil
}

func toMap(value interface{}) map[string]interface{} {
	if m, ok := value.(map[string]interface{}); ok {
		return m
	}
	return make(map[string]interface{})
}

func toSlice(value interface{}) []interface{} {
	if s, ok := value.([]interface{}); ok {
		return s
	}
	return nil
}
