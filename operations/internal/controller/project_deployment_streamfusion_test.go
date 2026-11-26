package controller

import (
	"net/url"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestStreamFusionSiteRequiresProjectBackendAndKeepsSPARoutes(t *testing.T) {
	web := applicationModule{ID: "web", Directory: "platform-web", RepositoryID: "repo"}
	api := applicationModule{ID: "api", Directory: "platform-api", RepositoryID: "repo"}
	p := projectDeploymentContext{module: web, project: applicationProject{Modules: []applicationModule{web, api}}}
	p.draft.Nginx = &applicationNginxDeploy{}
	for _, tt := range []struct {
		name     string
		spa      bool
		path     string
		dest     string
		strip    bool
		conflict string
		ok       bool
	}{
		{name: "primary", spa: true, path: "/api", dest: "api", strip: true, ok: true},
		{name: "with trailing slash", spa: true, path: "/api/", dest: "api", strip: true, ok: true},
		{name: "generic target", spa: true, path: "/api", dest: "other", strip: true},
		{name: "root pages disabled", path: "/api", dest: "api", strip: true},
		{name: "prefix retained", spa: true, path: "/api", dest: "api"},
		{name: "swallowed SPA route", spa: true, path: "/api", dest: "api", strip: true, conflict: "/preview"},
		{name: "swallowed login", spa: true, path: "/api", dest: "api", strip: true, conflict: "/login"},
		{name: "swallowed static asset", spa: true, path: "/api", dest: "api", strip: true, conflict: "/assets/app.js"},
		{name: "separate service", spa: true, path: "/api", dest: "api", strip: true, conflict: "/extra-service", ok: true},
		{name: "overlapping API", spa: true, path: "/api", dest: "api", strip: true, conflict: "/api/actuator"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p.draft.Nginx.ProxyRules = []applicationNginxProxyRule{{Location: tt.path, TargetModuleID: tt.dest, StripPrefix: tt.strip}}
			if tt.conflict != "" {
				p.draft.Nginx.ProxyRules = append(p.draft.Nginx.ProxyRules, applicationNginxProxyRule{Location: tt.conflict})
			}
			site := deploy.ProjectSite{SPA: tt.spa, ServerName: "platform.example.test"}
			if err := configureStreamFusionProjectSite(p, &site); (err == nil) != tt.ok {
				t.Fatalf("configure = %v", err)
			}
			if tt.ok && site.HealthPath != deploy.StreamFusionSiteHealthPath {
				t.Fatalf("site health path = %q", site.HealthPath)
			}
		})
	}
	p.draft.Nginx.ProxyRules = []applicationNginxProxyRule{{Location: "/api", TargetModuleID: "api", StripPrefix: true}}
	if err := configureStreamFusionProjectSite(p, &deploy.ProjectSite{SPA: true, ServerName: "_"}); err == nil || err.Error() != "PROJECT_STREAMFUSION_SITE_NAME_REQUIRED" {
		t.Fatalf("expected a concrete server name, got %v", err)
	}
	p.module.Directory = "admin-web"
	if err := configureStreamFusionProjectSite(p, &deploy.ProjectSite{}); err != nil {
		t.Fatalf("generic website inherited StreamFusion validation: %v", err)
	}
}

func TestDecommissionDependencyMatchesDeployedBackendEndpoint(t *testing.T) {
	for _, tt := range []struct {
		target   string
		bind     string
		sameHost bool
		want     bool
	}{
		{"http://127.0.0.1:8080", "0.0.0.0", true, true},
		{"http://127.0.0.1:8080", "0.0.0.0", false, false},
		{"http://backend.example:8080", "0.0.0.0", false, true},
		{"http://backend.example:8081", "0.0.0.0", false, false},
		{"http://192.0.2.20:8080", "192.0.2.20", false, true},
	} {
		u, err := url.Parse(tt.target)
		if err != nil {
			t.Fatal(err)
		}
		if got := projectProxyUsesBackend(u, "backend.example", tt.sameHost, deploy.ProjectDeployment{Address: tt.bind, Port: 8080}); got != tt.want {
			t.Errorf("%s, same host=%t: got %t, want %t", tt.target, tt.sameHost, got, tt.want)
		}
	}
}
