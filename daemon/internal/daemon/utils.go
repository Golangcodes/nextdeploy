package daemon

import (
	"fmt"
	"log"
	"os/exec"
	"regexp"
)

func resolveTool(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		log.Printf("[daemon] Warning: could not resolve tool %s: %v", name, err)
		// Fallback to absolute paths commonly used
		switch name {
		case "tar":
			return "/usr/bin/tar"
		case "find":
			return "/usr/bin/find"
		case "chown":
			return "/usr/bin/chown"
		case "chmod":
			return "/usr/bin/chmod"
		case "systemctl":
			return "/usr/bin/systemctl"
		case "caddy":
			return "/usr/bin/caddy"
		}
		return name
	}
	return path
}

func validateAppName(name string) error {
	if matched, _ := regexp.MatchString(`^[a-z0-9-]+$`, name); !matched {
		return fmt.Errorf("invalid app name: must contain only lowercase alphanumeric and hyphens")
	}
	if len(name) < 3 || len(name) > 63 {
		return fmt.Errorf("invalid app name length (3-63 chars)")
	}
	return nil
}

func validateDomain(domain string) error {
	if domain == "localhost" {
		return nil
	}
	// Basic RFC 1035 label validation
	if matched, _ := regexp.MatchString(`^([a-z0-9]+(-[a-z0-9]+)*\.)+[a-z]{2,}$`, domain); !matched {
		return fmt.Errorf("invalid domain name format")
	}
	return nil
}

func StringArg(args map[string]interface{}, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func Coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
