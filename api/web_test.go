package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asemones/bibicontrol/api"
)

// TestServesWebIndex asserts that:
//  1. GET / returns 200 and serves the real SPA (not the old "bibid running" placeholder).
//  2. The body contains the expected DOM-id markers that downstream tickets (U11/U12/U13)
//     depend on.
//  3. GET /app.js and GET /api.js return 200 with a JavaScript content-type and
//     non-empty bodies — proving the go:embed picked up all new assets.
func TestServesWebIndex(t *testing.T) {
	d := api.New(t.TempDir(), "owner")
	defer func() { _ = d.Close() }()

	h := d.Handler()

	t.Run("root_returns_real_ui", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /: got status %d, want 200", rec.Code)
		}
		body := rec.Body.String()

		// Must NOT contain the old placeholder.
		if strings.Contains(body, "bibid running") {
			t.Error("GET /: body still contains old placeholder 'bibid running'")
		}

		// Must contain the Workspaces DOM marker (downstream contract).
		if !strings.Contains(body, `id="wsList"`) {
			t.Error(`GET /: body missing id="wsList"`)
		}

		// Must contain the daemon-up indicator ids.
		if !strings.Contains(body, `id="daemonDot"`) {
			t.Error(`GET /: body missing id="daemonDot"`)
		}

		// Must not load the old mockup asset files via link/script tags.
		if strings.Contains(body, `href="ui_mockup`) || strings.Contains(body, `src="ui_mockup`) {
			t.Error("GET /: body loads ui_mockup assets — should reference app.css/api.js/app.js")
		}

		// Must load api.js before app.js (shared contract for U11/U12/U13).
		apiPos := strings.Index(body, "api.js")
		appPos := strings.Index(body, "app.js")
		if apiPos < 0 || appPos < 0 {
			t.Errorf("GET /: missing script tags: api.js pos=%d app.js pos=%d", apiPos, appPos)
		} else if apiPos > appPos {
			t.Error("GET /: api.js must appear before app.js in the document")
		}
	})

	t.Run("index_links_cheatlist", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		body := rec.Body.String()
		if !strings.Contains(body, "cheatlist.html") {
			t.Error(`GET /: index.html does not link to cheatlist.html`)
		}
	})

	for _, asset := range []string{"/app.js", "/api.js", "/app.css", "/cheatlist.html"} {
		asset := asset
		safeName := strings.TrimPrefix(asset, "/")
		safeName = strings.ReplaceAll(safeName, ".", "_")
		t.Run("serves_"+safeName, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, asset, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: got status %d, want 200", asset, rec.Code)
			}
			if rec.Body.Len() == 0 {
				t.Fatalf("GET %s: empty body", asset)
			}
			ct := rec.Header().Get("Content-Type")
			if strings.HasSuffix(asset, ".js") {
				if !strings.Contains(ct, "javascript") && !strings.Contains(ct, "ecmascript") {
					t.Errorf("GET %s: Content-Type=%q, want a JS type", asset, ct)
				}
			}
			if strings.HasSuffix(asset, ".css") {
				if !strings.Contains(ct, "css") {
					t.Errorf("GET %s: Content-Type=%q, want text/css", asset, ct)
				}
			}
		})
	}
}
