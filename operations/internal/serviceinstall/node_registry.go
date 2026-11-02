package serviceinstall

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// A registry URL is configuration, never a command or an automatic network probe.
func ValidateNodeRegistryURL(value string) error {
	if len(value) == 0 || len(value) > 2048 || strings.TrimSpace(value) != value {
		return errors.New("NODE_REGISTRY_URL_INVALID")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("NODE_REGISTRY_URL_INVALID")
	}
	for _, part := range []string{value, parsed.Path, parsed.Host} {
		for _, r := range part {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				return errors.New("NODE_REGISTRY_URL_INVALID")
			}
		}
	}
	if strings.ContainsAny(value, "\\#") || strings.Contains(value, "${") || strings.Contains(parsed.Path, "${") {
		return errors.New("NODE_REGISTRY_URL_INVALID")
	}
	if port := parsed.Port(); port != "" {
		number, e := strconv.Atoi(port)
		if e != nil || number < 1 || number > 65535 {
			return errors.New("NODE_REGISTRY_URL_INVALID")
		}
	} else if strings.HasSuffix(parsed.Host, ":") {
		return errors.New("NODE_REGISTRY_URL_INVALID")
	}
	return nil
}
