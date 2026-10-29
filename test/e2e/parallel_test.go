//go:build integration

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTwoPHPVersionsInParallel is the spike's acceptance test: two sites on
// different PHP versions served at the same time by one Caddy.
func TestTwoPHPVersionsInParallel(t *testing.T) {
	h := newHarness(t)
	urls := map[string]string{}
	for _, s := range []struct{ name, php string }{{"demo", "8.4"}, {"legacy", "8.2"}} {
		var added struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(h.mustCovo("site", "add", s.name, "--php", s.php, "--path", newSite(t)), &added); err != nil {
			t.Fatal(err)
		}
		urls[s.php] = added.URL
	}
	h.mustCovo("start")

	st := h.status()
	var names []string
	for _, svc := range st.Services {
		if !svc.Running || svc.PID == 0 {
			t.Errorf("service %s not running", svc.Name)
		}
		names = append(names, svc.Name)
	}
	if got := strings.Join(names, ","); got != "caddy,php-8.2,php-8.4" {
		t.Fatalf("services = %s, want caddy,php-8.2,php-8.4", got)
	}

	for php, url := range urls {
		if body := h.get(url, st.CA.CertPath); !strings.HasPrefix(body, php+".") {
			t.Errorf("%s served %q, want PHP %s.x", url, body, php)
		}
	}
}
