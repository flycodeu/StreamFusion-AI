package projectconfig

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

var sensitiveName = regexp.MustCompile(`(?i)(password|passwd|secret|token|api_?key|private_?key|credential)`)
var connectionSecret = regexp.MustCompile(`(?i)(^|[;?&\s])(password|passwd|pwd|token|access_token|secret|api[_-]?key|authorization)\s*=\s*[^;\s&]+`)

// IsSensitiveName marks a likely credential field even when its value is unset.
func IsSensitiveName(name string) bool {
	return sensitiveName.MatchString(name)
}

// ContainsSecret detects values that must not be advertised as plain text.
// This is a heuristic for project configuration, not a general secret scanner.
func ContainsSecret(value string) bool {
	if connectionSecret.MatchString(value) || strings.Contains(value, "PRIVATE KEY-----") {
		return true
	}
	u, err := url.Parse(value)
	if err == nil && u.User != nil {
		password, present := u.User.Password()
		return present && password != ""
	}
	return false
}

// ValidRelativePath accepts an unset field and clean, slash-separated repo paths.
func ValidRelativePath(value string) bool {
	if value == "" {
		return true
	}
	return len(value) <= 240 && path.Clean(value) == value && !path.IsAbs(value) && value != ".." && !strings.HasPrefix(value, "../") && !strings.ContainsAny(value, "\\\r\n\x00:")
}

func ValidProfile(value string) bool {
	if value == "" || len(value) > 40 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
