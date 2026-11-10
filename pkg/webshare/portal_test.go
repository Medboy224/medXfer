package webshare

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

func asset(t *testing.T, name string) string {
	t.Helper()
	b, err := assets.ReadFile("assets/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// WEB-05, #157 (3): no inline script, style or handler in the page; the script never writes
// HTML, never evaluates text.
func TestPortalHasNoUnsafeCode(t *testing.T) {
	page := asset(t, "index.html")
	if m := regexp.MustCompile(`(?is)<script[^>]*>\s*\S.*?</script>`).FindString(page); m != "" {
		t.Errorf("inline script: %s", m)
	}
	for _, bad := range []string{"<style", " style=", "javascript:"} {
		if strings.Contains(page, bad) {
			t.Errorf("the page has %q", bad)
		}
	}
	if m := regexp.MustCompile(`\son[a-z]+\s*=`).FindString(page); m != "" {
		t.Errorf("an inline handler: %q", m)
	}
	js := asset(t, "app.js")
	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "setAttribute('style'", "setAttribute('on", ".cookie"} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js uses %s", bad)
		}
	}
	if !strings.Contains(js, "history.replaceState") || !strings.Contains(js, "'Bearer ' + token") {
		t.Error("app.js no longer takes the token out of the address, or no longer sends it in Authorization")
	}
}

// Every text the page and the script ask for exists in French and in English; every element
// the script looks for is in the page.
func TestPortalTextsAndElements(t *testing.T) {
	page, js := asset(t, "index.html"), asset(t, "app.js")
	block := func(lang string) map[string]bool {
		start := strings.Index(js, "    "+lang+": {")
		end := strings.Index(js[start:], "\n    },")
		if start < 0 || end < 0 {
			t.Fatalf("no %s texts", lang)
		}
		keys := map[string]bool{}
		for _, m := range regexp.MustCompile(`(?m)^      (\w+):`).FindAllStringSubmatch(js[start:start+end], -1) {
			keys[m[1]] = true
		}
		return keys
	}
	fr, en := block("fr"), block("en")
	var missing []string
	for k := range fr {
		if !en[k] {
			missing = append(missing, "en."+k)
		}
	}
	for k := range en {
		if !fr[k] {
			missing = append(missing, "fr."+k)
		}
	}
	used := regexp.MustCompile(`data-i18n="(\w+)"`).FindAllStringSubmatch(page, -1)
	used = append(used, regexp.MustCompile(`\bt\('(\w+)'`).FindAllStringSubmatch(js, -1)...)
	for _, m := range regexp.MustCompile(`(\d+): '(\w+)'`).FindAllStringSubmatch(js, -1) {
		used = append(used, []string{m[0], m[2]})
	}
	for _, m := range used {
		if !fr[m[1]] {
			missing = append(missing, m[1])
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("texts missing: %v", missing)
	}
	for _, m := range regexp.MustCompile(`\$\('([\w-]+)'\)`).FindAllStringSubmatch(js, -1) {
		if !strings.Contains(page, `id="`+m[1]+`"`) {
			t.Errorf("app.js looks for #%s, not in the page", m[1])
		}
	}
	for _, s := range []string{"pin", "closed", "main"} {
		if !strings.Contains(page, `id="`+s+`"`) {
			t.Errorf("no #%s", s)
		}
	}
}

// A download link: asked with the token, a key of its own (never the token, WEB-04), for one
// item, valid LinkTTL, usable again to resume, gone with the session.
func TestDownloadLink(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(file, []byte("contenu"), 0o644)
	s, g, _ := startGuest(t, Options{Paths: []string{file}})
	if resp := g.do("POST", "/api/items/0/link", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without the token: %d", resp.StatusCode)
	}
	var m map[string]string
	_ = json.NewDecoder(g.do("POST", "/api/items/0/link", s.Token(), nil).Body).Decode(&m)
	url := m["url"]
	if !regexp.MustCompile(`^/dl/[0-9a-f]{32}$`).MatchString(url) || strings.Contains(url, s.Token()) {
		t.Fatalf("link %q", url)
	}
	for i := 0; i < 2; i++ { // twice: a download resumed
		resp := g.do("GET", url, "", nil)
		if body := readAll(t, resp.Body); resp.StatusCode != http.StatusOK || string(body) != "contenu" {
			t.Fatalf("download %d: %d %q", i, resp.StatusCode, body)
		}
		if resp.Header.Get("Content-Security-Policy") != CSP {
			t.Fatal("a download without the headers")
		}
	}
	if resp := g.do("GET", "/dl/"+strings.Repeat("a", 32), "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown key: %d", resp.StatusCode)
	}
	if resp := g.do("POST", "/api/items/7/link", s.Token(), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown item: %d", resp.StatusCode)
	}
	s.mu.Lock()
	s.now = func() time.Time { return time.Now().Add(LinkTTL + time.Minute) }
	s.mu.Unlock()
	if resp := g.do("GET", url, "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an expired link: %d", resp.StatusCode)
	}
}
