package serviceinstall

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// WebAccessDefinition describes a browser link. Ops does not proxy this URL or
// pass its credentials to the application.
type WebAccessDefinition struct {
	Scheme       string `json:"scheme"`
	PortKey      string `json:"portKey,omitempty"`
	Path         string `json:"path,omitempty"`
	Embed        bool   `json:"embed,omitempty"`
	URLParameter string `json:"urlParameter,omitempty"`
}

func unsafeWebText(value string) bool {
	return strings.Contains(value, "\\") || strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0
}

func ValidWebURL(value string) bool {
	if value == "" || len(value) > 2048 || unsafeWebText(value) || strings.ContainsAny(value, "?#") {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Opaque != "" || u.User != nil || u.Hostname() == "" || (strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https") || unsafeWebText(u.Hostname()) || strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return !unsafeWebText(u.Path)
}

func (d Definition) validateWebAccess() error {
	if d.DefaultBindAddress != "" {
		if !validListenAddress(d.DefaultBindAddress, d.Web != nil) {
			return errors.New("SERVICE_BIND_DEFAULT_INVALID")
		}
	}
	w := d.Web
	if w == nil {
		return nil
	}
	if w.Scheme != "http" && w.Scheme != "https" || d.Kind == "TOOL" {
		return errors.New("SERVICE_WEB_ACCESS_INVALID")
	}
	if w.Path != "" {
		u, err := url.Parse(w.Path)
		if err != nil || len(w.Path) > 512 || !strings.HasPrefix(w.Path, "/") || strings.HasPrefix(w.Path, "//") || strings.ContainsAny(w.Path, "?#") || unsafeWebText(w.Path) || u.Scheme != "" || u.Host != "" || strings.HasPrefix(u.Path, "//") || unsafeWebText(u.Path) {
			return errors.New("SERVICE_WEB_ACCESS_INVALID")
		}
	}
	portKey := w.PortKey
	if portKey == "" {
		portKey = "main"
	}
	found := portKey == "main" && d.Port > 0
	for _, port := range d.Ports {
		if port.Key == portKey {
			found = port.Protocol == "TCP"
			break
		}
	}
	if !found {
		return errors.New("SERVICE_WEB_ACCESS_INVALID")
	}
	if w.URLParameter != "" {
		for _, field := range d.Parameters {
			if field.Key == w.URLParameter && field.Type == "TEXT" {
				if field.Default != "" && !ValidWebURL(field.Default) {
					return errors.New("SERVICE_WEB_URL_INVALID")
				}
				return nil
			}
		}
		return errors.New("SERVICE_WEB_ACCESS_INVALID")
	}
	return nil
}

func validListenAddress(address string, allowWildcard bool) bool {
	ip := net.ParseIP(address)
	return ip != nil && ip.To4() != nil && !ip.IsMulticast() && (allowWildcard || !ip.IsUnspecified())
}

func (c Config) validateWebURL(d Definition) error {
	if d.Web == nil || d.Web.URLParameter == "" {
		return nil
	}
	values, err := ResolveParameters(d.Parameters, c.Parameters, c.ParameterCredentials)
	if err != nil {
		return err
	}
	value := values[d.Web.URLParameter]
	if value != "" && !ValidWebURL(value) {
		return errors.New("SERVICE_WEB_URL_INVALID")
	}
	return nil
}
