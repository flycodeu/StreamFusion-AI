package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerCacheControlHeaders(t *testing.T) {
	handler := Handler()
	for _, route := range []string{"/", "/index.html", "/software-instances"} {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d want 200", route, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
			t.Fatalf("GET %s Cache-Control=%q want no-cache, no-store, must-revalidate", route, got)
		}
	}

	entries, err := fs.ReadDir(content, "dist/assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("ReadDir(dist/assets) err=%v len=%d", err, len(entries))
	}
	assetReq := httptest.NewRequest(http.MethodGet, "/assets/"+entries[0].Name(), nil)
	assetRec := httptest.NewRecorder()
	handler.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("GET asset status=%d want 200", assetRec.Code)
	}
	if got := assetRec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("GET asset Cache-Control=%q want immutable cache header", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/missing-chunk.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset status=%d want 404", rec.Code)
	}
}
