package projectinspect

import (
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/streamfusion-ai/operations/internal/projectconfig"
)

var (
	privateKeyBlockPattern   = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	inlineSecretTokenPattern = regexp.MustCompile(`\b(ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9]{20,})\b`)
)

func sanitizeConfigTemplateContent(raw string) string {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	text = privateKeyBlockPattern.ReplaceAllString(text, "-----BEGIN PRIVATE KEY-----\n# [REDACTED]\n-----END PRIVATE KEY-----")
	text = inlineSecretTokenPattern.ReplaceAllString(text, "${SFOPS_CRED:token}")
	return text
}

func collectDirectoryConfigTemplates(root *os.Root, directory string, filenames []string) []projectconfig.ConfigTemplate {
	out := make([]projectconfig.ConfigTemplate, 0, len(filenames))
	for _, name := range filenames {
		if len(out) >= 4 {
			break
		}
		rel := path.Join(directory, name)
		raw := projectSourceText(root, rel)
		if len(raw) == 0 || len(raw) > 16<<10 {
			continue
		}
		format := "env"
		if ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), "."); ext == "yml" || ext == "yaml" || ext == "json" || ext == "properties" {
			format = ext
		}
		profile := ""
		if strings.HasPrefix(name, ".env.") && name != ".env.example" {
			profile = strings.TrimPrefix(name, ".env.")
		}
		out = append(out, projectconfig.ConfigTemplate{
			Filename: name,
			Profile:  profile,
			Format:   format,
			Content:  sanitizeConfigTemplateContent(string(raw)),
		})
	}
	return out
}
