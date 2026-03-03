package nextcore

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ParseNextConfigFile reads next.config.mjs/js and returns a parsed NextConfig
// It uses a JS runtime (bun/node) to evaluate the config accurately.
func ParseNextConfigFile(configPath string) (*NextConfig, error) {
	paths := []string{
		configPath,
		strings.Replace(configPath, ".mjs", ".js", 1),
		strings.Replace(configPath, ".mjs", ".ts", 1),
	}

	var usedPath string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			usedPath = p
			break
		}
	}

	if usedPath == "" {
		return nil, fmt.Errorf("could not find next.config file at %s", configPath)
	}

	NextCoreLogger.Info("Evaluating Next.js config via JS runtime: %s", usedPath)
	configObj, err := evaluateConfigViaRuntime(usedPath)
	if err != nil {
		NextCoreLogger.Error("Failed to evaluate config via runtime: %v", err)
		return nil, fmt.Errorf("failed to evaluate config object: %w", err)
	}

	return parseConfigObject(configObj)
}

func parseEdgeRegions(content string) []string {
	// Simple fallback search for edge regions
	// We keep this purely for raw file scraping if needed
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

	getStringSlice := func(key string) []string {
		if arr, ok := config[key].([]interface{}); ok {
			var res []string
			for _, v := range arr {
				if s, ok := v.(string); ok {
					res = append(res, s)
				}
			}
			return res
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

	if val, ok := config["generateBuildId"]; ok {
		result.GenerateBuildId = val
	}

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
		// Just store a stub or exact representation, avoiding duplicates
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
	if num, ok := m[key].(json.Number); ok {
		if val, err := num.Int64(); err == nil {
			return int(val)
		}
	}
	if val, ok := m[key].(float64); ok {
		return int(val)
	}
	if val, ok := m[key].(int); ok {
		return val
	}
	return 0
}

func getFloat64FromMap(m map[string]interface{}, key string) float64 {
	if num, ok := m[key].(json.Number); ok {
		if val, err := num.Float64(); err == nil {
			return val
		}
	}
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
			if num, ok := v.(json.Number); ok {
				if i, err := num.Int64(); err == nil {
					result = append(result, int(i))
				}
			} else if i, ok := v.(int); ok {
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
