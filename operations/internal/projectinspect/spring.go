package projectinspect

import (
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/projectconfig"
)

var (
	springConfigFilePattern  = regexp.MustCompile(`^application(?:-([a-zA-Z0-9_-]{1,40}))?\.(yml|yaml|properties)$`)
	springEnvExprPattern     = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]{1,63})(?::([^}]*))?\}`)
	springPropPortPattern    = regexp.MustCompile(`^server\.port\s*[:=]\s*(.+)$`)
	springPropDefaultProfile = regexp.MustCompile(`^spring\.profiles\.default\s*[:=]\s*([a-zA-Z0-9_-]{1,40})\b`)
)

func parsePortSpec(raw string) int {
	raw = strings.TrimSpace(raw)
	if match := springEnvExprPattern.FindStringSubmatch(raw); len(match) == 3 {
		raw = strings.TrimSpace(match[2])
	}
	raw = strings.Trim(raw, `"'`)
	if raw == "" {
		return 0
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

type springConfigDetails struct {
	profiles       []string
	defaultProfile string
	defaultPort    int
	envHints       []projectconfig.EnvHint
	templates      []projectconfig.ConfigTemplate
	evidence       []string
}

func inspectSpringConfigFiles(root *os.Root, directory string) springConfigDetails {
	var out springConfigDetails
	resDir := path.Join(directory, "src/main/resources")
	dirFile, err := root.Open(resDir)
	if err != nil {
		return out
	}
	entries, _ := dirFile.ReadDir(128)
	dirFile.Close()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	type configEntry struct {
		relPath string
		profile string
		ext     string
		body    string
	}
	var configs []configEntry
	profileSet := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || len(configs) >= 12 {
			continue
		}
		match := springConfigFilePattern.FindStringSubmatch(entry.Name())
		if len(match) == 0 {
			continue
		}
		rel := path.Join(resDir, entry.Name())
		raw := projectSourceText(root, rel)
		if len(raw) == 0 {
			continue
		}
		profile := match[1]
		if profile != "" {
			profileSet[profile] = true
		}
		configs = append(configs, configEntry{
			relPath: rel,
			profile: profile,
			ext:     match[2],
			body:    strings.ReplaceAll(string(raw), "\r\n", "\n"),
		})
		out.evidence = append(out.evidence, rel)
	}
	for profile := range profileSet {
		out.profiles = append(out.profiles, profile)
	}
	sort.Strings(out.profiles)

	// Extract profile-specific files first (test > prod > dev > others > base) so test/prod defaults win over common fallback.
	profilePriority := func(p string) int {
		switch strings.ToLower(p) {
		case "test":
			return 0
		case "prod":
			return 1
		case "dev":
			return 2
		case "":
			return 4
		default:
			return 3
		}
	}
	sortedConfigs := append([]configEntry(nil), configs...)
	sort.SliceStable(sortedConfigs, func(i, j int) bool {
		pi, pj := profilePriority(sortedConfigs[i].profile), profilePriority(sortedConfigs[j].profile)
		if pi != pj {
			return pi < pj
		}
		return sortedConfigs[i].relPath < sortedConfigs[j].relPath
	})

	hintIndex := map[string]int{}
	for _, cfg := range sortedConfigs {
		if len(out.templates) < 8 && len(cfg.body) <= 32<<10 {
			out.templates = append(out.templates, projectconfig.ConfigTemplate{
				Filename: path.Base(cfg.relPath),
				Profile:  cfg.profile,
				Format:   strings.ToLower(cfg.ext),
				Content:  sanitizeConfigTemplateContent(cfg.body),
			})
		}
		inServer, serverIndent := false, -1
		inSpring, springIndent := false, -1
		inProfiles, profilesIndent := false, -1
		for line := range strings.SplitSeq(cfg.body, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if cfg.ext == "properties" {
				if m := springPropDefaultProfile.FindStringSubmatch(trimmed); len(m) == 2 && out.defaultProfile == "" {
					out.defaultProfile = m[1]
				}
				if m := springPropPortPattern.FindStringSubmatch(trimmed); len(m) == 2 && (out.defaultPort == 0 || cfg.profile == "test") {
					if port := parsePortSpec(m[1]); port > 0 {
						out.defaultPort = port
					}
				}
			} else {
				if indent <= serverIndent {
					inServer = false
				}
				if indent <= profilesIndent {
					inProfiles = false
				}
				if indent <= springIndent {
					inSpring, inProfiles = false, false
				}
				if key, val, ok := strings.Cut(trimmed, ":"); ok && !strings.HasPrefix(trimmed, "-") {
					k := strings.TrimSpace(key)
					v := strings.TrimSpace(strings.Split(val, " #")[0])
					switch {
					case indent == 0 && k == "server":
						inServer, serverIndent = true, indent
					case indent == 0 && k == "spring":
						inSpring, springIndent = true, indent
					case inSpring && indent > springIndent && k == "profiles":
						inProfiles, profilesIndent = true, indent
					case inProfiles && indent > profilesIndent && k == "default" && out.defaultProfile == "":
						cand := strings.Trim(v, `"'`)
						if projectconfig.ValidProfile(cand) {
							out.defaultProfile = cand
						}
					case inServer && indent > serverIndent && k == "port" && out.defaultPort == 0:
						if port := parsePortSpec(v); port > 0 {
							out.defaultPort = port
						}
					}
				}
			}
			for _, m := range springEnvExprPattern.FindAllStringSubmatch(line, 16) {
				name := m[1]
				defVal := strings.TrimSpace(m[2])
				sensitive := projectconfig.IsSensitiveName(name)
				if sensitive || projectconfig.ContainsSecret(defVal) || len(defVal) > 180 || strings.Contains(defVal, "${") || strings.ContainsAny(defVal, "\r\n\x00") {
					defVal = ""
				}
				if idx, exists := hintIndex[name]; exists {
					if out.envHints[idx].DefaultValue == "" && defVal != "" {
						out.envHints[idx].DefaultValue = defVal
					}
					if out.envHints[idx].Profile == "" && cfg.profile != "" {
						out.envHints[idx].Profile = cfg.profile
					}
					out.envHints[idx].Sensitive = out.envHints[idx].Sensitive || sensitive
					continue
				}
				if len(out.envHints) >= 40 {
					continue
				}
				hintIndex[name] = len(out.envHints)
				out.envHints = append(out.envHints, projectconfig.EnvHint{
					Name:         name,
					DefaultValue: defVal,
					Sensitive:    sensitive,
					Profile:      cfg.profile,
				})
			}
		}
	}
	if out.defaultProfile == "" && slices.Contains(out.profiles, "test") {
		out.defaultProfile = "test"
	} else if out.defaultProfile == "" && len(out.profiles) > 0 {
		out.defaultProfile = out.profiles[0]
	}
	return out
}
