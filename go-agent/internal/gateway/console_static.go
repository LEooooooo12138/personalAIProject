package gateway

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

var consoleHashedAsset = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

var consoleAssetTypes = map[string]string{
	".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg",
	".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".ico": "image/x-icon", ".woff": "font/woff",
	".woff2": "font/woff2",
}

func registerConsoleStaticRoutes(r *gin.Engine, root, publicOrigin string) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = ""
	}
	csp := "default-src 'none'; base-uri 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; worker-src 'self'; connect-src 'self'"
	if u, err := url.Parse(publicOrigin); err == nil && u.User == nil && u.Host != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(u.Host, " \t\r\n;\"'") {
		switch u.Scheme {
		case "https":
			csp += " wss://" + u.Host
		case "http":
			csp += " ws://" + u.Host
		}
	}
	csp += "; form-action 'self'; frame-ancestors 'none'; object-src 'none'"
	handle := func(c *gin.Context) {
		c.Header("Content-Security-Policy", csp)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Header("Allow", "GET, HEAD")
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		if c.Request.URL.Path == "/app" {
			c.Redirect(http.StatusPermanentRedirect, "/app/")
			return
		}
		rel := strings.TrimPrefix(c.Request.URL.Path, "/app/")
		if !consoleSafeStaticPath(rel) {
			c.Status(http.StatusNotFound)
			return
		}
		page := rel == "" || rel == "chat" || rel == "account" || rel == "members" || rel == "knowledge" || rel == "automations" || rel == "admin/knowledge" || rel == "admin/collection" || consoleFamilyPage(rel)
		if strings.HasPrefix(rel, "areas") && c.Request.URL.RawPath != "" {
			c.Status(http.StatusNotFound)
			return
		}
		name := rel
		mime := consoleAssetTypes[strings.ToLower(filepath.Ext(rel))]
		if page {
			name, mime = "index.html", "text/html; charset=utf-8"
		} else if mime == "" || rel == "index.html" || (mime == "text/javascript; charset=utf-8" || mime == "text/css; charset=utf-8") && (!strings.HasPrefix(rel, "assets/") || !consoleHashedAsset.MatchString(filepath.Base(rel))) {
			c.Status(http.StatusNotFound)
			return
		}
		index, err := consoleOpenStatic(absRoot, "index.html")
		if err != nil {
			c.Header("Cache-Control", "no-store")
			c.String(http.StatusServiceUnavailable, "Console frontend is not built")
			return
		}
		defer index.Close()
		file := index
		if !page {
			file, err = consoleOpenStatic(absRoot, name)
			if err != nil {
				c.Status(http.StatusNotFound)
				return
			}
			defer file.Close()
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Content-Type", mime)
		if page || name == "assets/console-auth-worker.js" {
			c.Header("Cache-Control", "no-store")
		} else if consoleHashedAsset.MatchString(filepath.Base(name)) {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Header("Cache-Control", "no-cache")
		}
		http.ServeContent(c.Writer, c.Request, filepath.Base(name), info.ModTime(), file)
	}
	r.Any("/app", handle)
	r.Any("/app/*path", handle)
}

func consoleSafeStaticPath(rel string) bool {
	if strings.ContainsAny(rel, "\\\x00:") || strings.Contains(rel, "//") {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "." || part == ".." || strings.HasPrefix(part, ".") || (part == "" && rel != "") {
			return false
		}
	}
	return true
}

func consoleOpenStatic(root, rel string) (*os.File, error) {
	if root == "" || !consoleSafeStaticPath(rel) {
		return nil, os.ErrNotExist
	}
	// Anchor each lookup to an opened parent, then let os.Root resolve every
	// resource component. Root.Open cannot follow a swapped link outside it.
	parent, err := os.OpenRoot(filepath.Dir(root))
	if err != nil {
		return nil, os.ErrNotExist
	}
	defer parent.Close()
	base := filepath.Base(root)
	info, err := parent.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, os.ErrNotExist
	}
	openedRoot, err := parent.OpenRoot(base)
	if err != nil {
		return nil, os.ErrNotExist
	}
	defer openedRoot.Close()
	openedInfo, err := openedRoot.Stat(".")
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, os.ErrNotExist
	}
	file, err := openedRoot.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, os.ErrNotExist
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, os.ErrNotExist
	}
	return file, nil
}

func consoleFamilyPage(rel string) bool {
	parts := strings.Split(rel, "/")
	if parts[0] != "areas" {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	validID := func(id, prefix string) bool {
		if !strings.HasPrefix(id, prefix) {
			return false
		}
		value := strings.TrimPrefix(id, prefix)
		raw, err := base64.RawURLEncoding.DecodeString(value)
		return err == nil && len(raw) > 0 && utf8.Valid(raw) && base64.RawURLEncoding.EncodeToString(raw) == value
	}
	if parts[1] != "u_other" && !validID(parts[1], "a_") {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	return len(parts) == 4 && parts[2] == "devices" && (validID(parts[3], "d_") || validID(parts[3], "e_"))
}
