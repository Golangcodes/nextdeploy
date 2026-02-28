package nextcore

import (
	"path/filepath"
)

func getRoutesFromManifests(buildMeta *NextBuildMetadata) (*RouteInfo, error) {
	info := &RouteInfo{
		SSGRoutes:      make(map[string]string),
		ISRRoutes:      make(map[string]string),
		FallbackRoutes: make(map[string]string),
	}
	if routesManifest, ok := buildMeta.RoutesManifest.(map[string]interface{}); ok {
		if staticRoutes, ok := routesManifest["staticRoutes"].([]interface{}); ok {
			for _, route := range staticRoutes {
				if routeMap, ok := route.(map[string]interface{}); ok {
					if page, ok := routeMap["page"].(string); ok {
						info.StaticRoutes = append(info.StaticRoutes, page)
					}
				}
			}
		}
		if dynamicRoutes, ok := routesManifest["dynamicRoutes"].([]interface{}); ok {
			for _, route := range dynamicRoutes {
				if routeMap, ok := route.(map[string]interface{}); ok {
					if page, ok := routeMap["page"].(string); ok {
						info.DynamicRoutes = append(info.DynamicRoutes, page)
					}
				}
			}
		}
	}
	if prerenderManifest, ok := buildMeta.PrerenderManifest.(map[string]interface{}); ok {
		if routes, ok := prerenderManifest["routes"].(map[string]interface{}); ok {
			for route, details := range routes {
				if detailMap, ok := details.(map[string]interface{}); ok {
					if initialRevalidate, ok := detailMap["initialRevalidateSeconds"].(float64); ok {
						if initialRevalidate > 0 {
							info.ISRRoutes[route] = filepath.Join(".next", "server", detailMap["dataRoute"].(string))
						} else {
							info.SSGRoutes[route] = filepath.Join(".next", "server", "pages", route+".html")
						}
					}
				}
			}
		}
		if dynamicRoutes, ok := prerenderManifest["dynamicRoutes"].(map[string]interface{}); ok {
			for route, details := range dynamicRoutes {
				if detailMap, ok := details.(map[string]interface{}); ok {
					if fallback, ok := detailMap["fallback"].(string); ok && fallback != "" {
						info.FallbackRoutes[route] = fallback
					}
				}
			}
		}
	}

	if buildManifest, ok := buildMeta.BuildManifest.(map[string]interface{}); ok {
		if middleware, ok := buildManifest["middleware"].(map[string]interface{}); ok {
			for route := range middleware {
				info.MiddlewareRoutes = append(info.MiddlewareRoutes, route)
			}
		}
	}

	return info, nil
}
