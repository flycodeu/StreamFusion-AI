package controller

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type materialOrigin struct {
	Kind         string `json:"kind"`
	Verification string `json:"verification"`
	ImportedAt   string `json:"importedAt,omitempty"`
	FileName     string `json:"fileName,omitempty"`
}

type materialSource struct {
	Requirements []string `json:"requirements,omitempty"`
	Name         string   `json:"name"`
	DisplayName  string   `json:"displayName"`
	Version      string   `json:"version"`
	Publisher    string   `json:"publisher"`
	URL          string   `json:"url"`
	SHA256       string   `json:"sha256"`
	ByteSize     int64    `json:"byteSize"`
	Role         string   `json:"role"`
	Packaging    string   `json:"packaging"`
}

func materialVerification(m installationMaterial) string {
	if m.Status != "AVAILABLE" {
		return "PENDING"
	}
	if m.RuntimeMode == "NATIVE" {
		return "RECIPE_SHA256"
	}
	return "PINNED_IMAGE"
}

func (s *Store) materialOriginPath(id string) string {
	return filepath.Join(s.Dir, "installation-materials", id+".origin.json")
}

func (s *Store) installationMaterialOrigin(m *installationMaterial) {
	m.Origin = materialOrigin{Kind: "UNKNOWN", Verification: materialVerification(*m)}
	file := s.materialOriginPath(m.ID)
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return
	}
	raw, err := os.ReadFile(file)
	var origin materialOrigin
	if err != nil || deploy.StrictJSON(raw, &origin) != nil || (origin.Kind != "BUNDLED" && origin.Kind != "UPLOADED" && origin.Kind != "OFFICIAL_DOWNLOAD") {
		return
	}
	origin.Verification = materialVerification(*m)
	m.Origin = origin
}

func (s *Store) writeMaterialOrigin(id string, origin materialOrigin) error {
	if !deploy.ID.MatchString(id) {
		return errors.New("MATERIAL_ORIGIN_ID_INVALID")
	}
	dir := filepath.Dir(s.materialOriginPath(id))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := privatePath(dir, true); err != nil {
		return err
	}
	raw, err := json.Marshal(origin)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".origin-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(0600); err == nil {
		_, err = temp.Write(raw)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), s.materialOriginPath(id))
	}
	return err
}

func materialSources(d serviceinstall.Definition, mode string) []materialSource {
	out := []materialSource{}
	if mode != "NATIVE" || d.Native == nil {
		if d.Image != "" {
			out = append(out, materialSource{Name: d.Image, DisplayName: d.Name + " 官方镜像", Version: d.Version, Publisher: "镜像发布方（参见官方地址）", URL: d.OfficialURL, SHA256: strings.TrimPrefix(d.Digest, "sha256:"), Role: "MAIN", Packaging: "DOCKER_SAVE"})
		}
		return out
	}
	for i, p := range d.Native.Parts {
		source := materialSource{Name: p.Name, DisplayName: d.Name + " " + d.Version, Version: d.Version, URL: p.URL, SHA256: p.SHA256, ByteSize: p.ByteSize, Role: "MAIN", Packaging: "BINARY_ARCHIVE", Publisher: "配方指定发布方"}
		if i > 0 {
			source.Role = "DEPENDENCY"
			source.DisplayName = p.Name
		}
		if p.Format == "DEB" {
			fields := strings.Split(p.Name, "_")
			if len(fields) >= 3 {
				source.DisplayName, source.Version = fields[0]+"（Ubuntu 24.04 软件包）", fields[1]
			}
			source.Publisher, source.Packaging = "Ubuntu Archive / Canonical", "ISOLATED_EXTRACTION"
			source.Role = "DEPENDENCY"
			if strings.HasPrefix(p.Name, "redis-server_") || strings.HasPrefix(p.Name, "nginx_") || strings.HasPrefix(p.Name, "docker-ce_") {
				source.Role = "MAIN"
			}
			if d.Key == "docker" {
				source.Publisher, source.Packaging = "Docker Inc.", "OS_PACKAGE"
				if source.Role == "MAIN" {
					source.Requirements = []string{"目标主机须为 Ubuntu 24.04 amd64，且已具备 iptables、nftables、libseccomp2、libc6、libsystemd0 的兼容版本。", "此材料仅含 Docker 的五个固定软件包，不是完整操作系统离线镜像；不会联网补装基础依赖。"}
				}
			}
		} else if strings.Contains(p.URL, "adoptium/") {
			source.DisplayName, source.Publisher = "Eclipse Temurin JDK "+d.Version, "Eclipse Adoptium"
			if d.Key == "maven" {
				source.DisplayName, source.Version = "Eclipse Temurin JDK 21", "21.0.12.1+1"
			}
			if d.Version == "8" {
				source.Version = "8u504-b01"
			}
		} else if strings.Contains(p.URL, "apache.org/") {
			source.Publisher = "Apache Software Foundation"
		} else if d.Key == "redis" {
			source.Publisher = "Redis"
		} else if d.Key == "nginx" {
			source.Publisher = "F5 / NGINX"
		} else if d.Key == "python" {
			source.Publisher = "Python Software Foundation"
		}
		out = append(out, source)
	}
	return out
}
