package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func get(t *testing.T, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler("pt-BR", "9.9.9").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

func TestServesFilesWithSecurityHeaders(t *testing.T) {
	for _, p := range []string{"/dashboard/", "/dashboard/app.js", "/dashboard/i18n.js", "/dashboard/style.css", "/dashboard/favicon.svg"} {
		res := get(t, p)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", p, res.StatusCode)
		}
		csp := res.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("%s: weak CSP %q", p, csp)
		}
		if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: missing headers %v", p, res.Header)
		}
	}
}

func TestConfig(t *testing.T) {
	res := get(t, "/dashboard/config.json")
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"defaultLanguage":"pt-BR"`) || !strings.Contains(string(body), `"version":"9.9.9"`) {
		t.Fatalf("config: %s", body)
	}
}

func TestNoTraversal(t *testing.T) {
	if res := get(t, "/dashboard/../dashboard.go"); res.StatusCode == http.StatusOK {
		t.Fatal("path traversal served a file")
	}
}

// Every text key must exist in both languages, and the page must not use inline scripts or
// innerHTML (the CSP would block the first; the second could open an XSS hole).
func TestTextsAndSafety(t *testing.T) {
	src, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(src), `"pt-BR": {`, 2)
	if len(parts) != 2 {
		t.Fatal("pt-BR block not found")
	}
	keyRe := regexp.MustCompile(`(?m)^\s{4}([A-Za-z_]+):`)
	keys := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, k := range keyRe.FindAllStringSubmatch(s, -1) {
			m[k[1]] = true
		}
		return m
	}
	en, pt := keys(parts[0]), keys(parts[1])
	for k := range en {
		if !pt[k] {
			t.Errorf("missing in pt-BR: %s", k)
		}
	}
	for k := range pt {
		if !en[k] {
			t.Errorf("missing in en: %s", k)
		}
	}
	app, _ := os.ReadFile("web/app.js")
	html, _ := os.ReadFile("web/index.html")
	// \bt\( so that select("qr") is not read as t("qr"); keys ending in "_" are prefixes.
	for _, used := range regexp.MustCompile(`\bt\("([A-Za-z_]+)"`).FindAllStringSubmatch(string(app)+string(html), -1) {
		if !strings.HasSuffix(used[1], "_") && !en[used[1]] {
			t.Errorf("app.js uses unknown text key %q", used[1])
		}
	}
	// Status texts are built from the instance status (t("status_" + status)).
	for _, st := range []string{"connected", "connecting", "qr", "disconnected", "logged_out"} {
		if !en["status_"+st] {
			t.Errorf("missing text for status %s", st)
		}
	}
	for _, used := range regexp.MustCompile(`data-i18n="([A-Za-z_]+)"`).FindAllStringSubmatch(string(html), -1) {
		if !en[used[1]] {
			t.Errorf("index.html uses unknown text key %q", used[1])
		}
	}
	if strings.Contains(string(app), "innerHTML") || strings.Contains(string(app), "insertAdjacentHTML") {
		t.Error("app.js must not use innerHTML")
	}
	if regexp.MustCompile(`<script>|<script [^>]*>[^<]`).Match(html) || strings.Contains(string(html), " style=") || regexp.MustCompile(` on[a-z]+=`).Match(html) {
		t.Error("index.html must not have inline scripts, styles or handlers")
	}
}
