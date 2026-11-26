package deploy

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	projectActuatorHealthPath  = "/actuator/health"
	StreamFusionSiteHealthPath = "/api" + projectActuatorHealthPath
)

func streamFusionAPIUpstream(site *ProjectSite) string {
	if site.HealthPath != StreamFusionSiteHealthPath || !site.SPA {
		return ""
	}
	for _, rule := range site.Proxies {
		if strings.TrimSuffix(rule.Location, "/") != "/api" || !rule.StripPrefix {
			continue
		}
		u, err := url.Parse(rule.URL)
		if err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") &&
			u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
			(u.Path == "" || u.Path == "/") {
			return strings.TrimRight(u.String(), "/") + projectActuatorHealthPath
		}
	}
	return ""
}

func projectHealthClient() *http.Client {
	return &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func projectSiteUpstreamHealth(ctx context.Context, site *ProjectSite) bool {
	endpoint := streamFusionAPIUpstream(site)
	return endpoint != "" && projectJSONHealth(ctx, projectHealthClient(), endpoint, "")
}

func projectLocalSite(p ProjectDeployment) string {
	return "http://127.0.0.1:" + strconv.Itoa(p.Port)
}

func projectJavaEndpointHealth(ctx context.Context, base, path string) bool {
	client := projectHealthClient()
	if path == projectActuatorHealthPath {
		return projectJSONHealth(ctx, client, base+path, "")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	return err == nil && len(data) <= 4096
}

func projectStaticReleaseServed(ctx context.Context, p ProjectDeployment) bool {
	if p.Site == nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, projectLocalSite(p)+"/__sfops_health", nil)
	if err != nil {
		return false
	}
	req.Host = p.Site.ServerName
	resp, err := projectHealthClient().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK && resp.Header.Get("X-StreamFusion-Release") == p.ReleaseID && resp.ContentLength != 0
}

func projectJSONHealth(ctx context.Context, c *http.Client, endpoint, host string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	req.Host = host
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType != "application/json" && !(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")) {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<10)+1))
	var health struct {
		Status string `json:"status"`
	}
	return err == nil && len(data) <= 8<<10 && json.Unmarshal(data, &health) == nil && health.Status == "UP"
}
