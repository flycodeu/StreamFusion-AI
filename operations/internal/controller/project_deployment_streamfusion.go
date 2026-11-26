package controller

import (
	"errors"
	"strings"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// A StreamFusion site has a known browser contract. Generic static sites
// continue to use their configured routes and the standard release marker.
func configureStreamFusionProjectSite(p projectDeploymentContext, site *deploy.ProjectSite) error {
	backend := streamFusionPlatformAPIModule(p.module, p.project.Modules)
	if backend == "" {
		return nil
	}
	if site.ServerName == "_" || site.ServerName == "" {
		return errors.New("PROJECT_STREAMFUSION_SITE_NAME_REQUIRED")
	}
	api := false
	for _, rule := range p.draft.Nginx.ProxyRules {
		location := strings.TrimSuffix(rule.Location, "/")
		switch {
		case location == "/api":
			if api || rule.TargetModuleID != backend || !rule.StripPrefix {
				return errors.New("PROJECT_STREAMFUSION_API_ROUTE_REQUIRED")
			}
			api = true
		case reservedStreamFusionSitePath(location):
			return errors.New("PROJECT_STREAMFUSION_API_ROUTE_REQUIRED")
		}
	}
	if !site.SPA || !api {
		return errors.New("PROJECT_STREAMFUSION_API_ROUTE_REQUIRED")
	}
	site.HealthPath = deploy.StreamFusionSiteHealthPath
	return nil
}

func reservedStreamFusionSitePath(location string) bool {
	segment, _, _ := strings.Cut(strings.TrimPrefix(location, "/"), "/")
	switch segment {
	case "api", "assets", "index.html", "api-docs.html", "api-docs.css", "api-docs.js", "streamfusion.svg",
		"login", "home", "profile", "change-password", "forbidden", "unavailable",
		"system", "monitor", "camera", "preview", "storage":
		return true
	default:
		return false
	}
}

func checkStreamFusionSiteRollback(q applicationReader, p projectDeploymentContext, site *deploy.ProjectSite) error {
	backend := streamFusionPlatformAPIModule(p.module, p.project.Modules)
	if backend == "" {
		return nil
	}
	invalid := errors.New("PROJECT_STREAMFUSION_ROLLBACK_CONTRACT_CHANGED")
	if site == nil || site.HealthPath != deploy.StreamFusionSiteHealthPath || !site.SPA ||
		site.ServerName == "" || site.ServerName == "_" {
		return invalid
	}
	var frozen string
	for _, rule := range site.Proxies {
		if strings.TrimSuffix(rule.Location, "/") != "/api" {
			continue
		}
		if frozen != "" || !rule.StripPrefix || rule.URL == "" {
			return invalid
		}
		frozen = rule.URL
	}
	if frozen == "" {
		return invalid
	}
	upstream, err := projectModuleUpstream(q, p, backend)
	if err != nil {
		return err
	}
	if frozen != upstream {
		return errors.New("PROJECT_STREAMFUSION_ROLLBACK_UPSTREAM_CHANGED")
	}
	return nil
}
