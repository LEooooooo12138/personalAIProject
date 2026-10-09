package core

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type ConsoleConfig struct {
	Enabled           bool   `mapstructure:"enabled"`
	DataDir           string `mapstructure:"data_dir"`
	PublicOrigin      string `mapstructure:"public_origin"`
	AllowInsecureHTTP bool   `mapstructure:"allow_insecure_http"`
}

// ValidateConsole is also used by directly constructed gateways to fail closed.
func (cfg *Config) ValidateConsole() error {
	if addr := cfg.Server.ListenAddress; addr != "" {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || strings.ContainsAny(host, "/\\ \t\r\n") || !validNetworkPort(port) {
			return fmt.Errorf("config: invalid server.listen_address")
		}
	}
	if !cfg.Console.Enabled {
		return nil
	}
	key := cfg.Server.InternalKey
	if strings.TrimSpace(key) == "" || strings.Contains(key, "${") {
		return fmt.Errorf("config: console requires a valid server.internal_key")
	}
	origin := cfg.Console.PublicOrigin
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(origin, "?#\\ \t\r\n") || strings.HasSuffix(u.Host, ":") || u.Scheme+"://"+u.Host != origin {
		return consoleOriginConfigError()
	}
	if u.Port() != "" && !validNetworkPort(u.Port()) {
		return consoleOriginConfigError()
	}
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") || !canonicalConsoleHost(u) {
		return consoleOriginConfigError()
	}
	if u.Scheme == "http" && !cfg.Console.AllowInsecureHTTP {
		return fmt.Errorf("config: HTTP console requires console.allow_insecure_http")
	}
	if strings.TrimSpace(cfg.Console.DataDir) == "" {
		return fmt.Errorf("config: console.data_dir is required")
	}
	data, err := resolvedConsolePath(cfg.Console.DataDir)
	if err != nil {
		return fmt.Errorf("config: cannot resolve console.data_dir: %w", err)
	}
	for _, root := range []string{cfg.Vaults.Personal, cfg.Vaults.Agent, "./static"} {
		if root == "" {
			continue
		}
		resolved, err := resolvedConsolePath(root)
		if err != nil {
			return fmt.Errorf("config: cannot resolve protected directory: %w", err)
		}
		if consolePathContains(resolved, data) || consolePathContains(data, resolved) {
			return fmt.Errorf("config: console.data_dir must be separate from Vault and static directories")
		}
	}
	return nil
}

func consoleOriginConfigError() error {
	return fmt.Errorf("config: console.public_origin must equal browser location.origin: use a lowercase ASCII DNS hostname (no IDN/punycode), canonical dotted IPv4, or compressed bracketed IPv6 (no IPv4 mapping/zone); omit default HTTP :80 / HTTPS :443 ports and any path")
}

// Accept a deliberately narrow, already serialized host syntax. Do not rewrite
// URLs using net/url: its normalization rules differ from browser WHATWG URLs.
func canonicalConsoleHost(u *url.URL) bool {
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") {
		address, err := netip.ParseAddr(host)
		return err == nil && address.Is6() && !address.Is4In6() && address.Zone() == "" && address.String() == host
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return address.Is4() && address.String() == host
	}
	if len(host) > 253 || host != strings.ToLower(host) {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' || strings.HasPrefix(label, "xn--") {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	// Browser hosts ending in a numeric label enter the IPv4 parser, which can
	// expand short/octal/hex forms or reject the URL. Only netip's canonical IPv4
	// branch above may accept those hosts.
	last := labels[len(labels)-1]
	decimal := true
	for _, ch := range last {
		if ch < '0' || ch > '9' {
			decimal = false
		}
	}
	hexadecimal := strings.HasPrefix(last, "0x")
	if hexadecimal {
		for _, ch := range last[2:] {
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
				hexadecimal = false
			}
		}
	}
	return !decimal && !hexadecimal
}

func validNetworkPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535 && strconv.Itoa(n) == port
}

// Resolve existing ancestors as well, so a not-yet-created child of a symlink
// cannot bypass the Vault/static boundary.
func resolvedConsolePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := absolute
	tail := []string{}
	for {
		_, err = os.Lstat(current)
		if err == nil {
			base, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				base = filepath.Join(base, tail[i])
			}
			return filepath.Clean(base), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		tail = append(tail, filepath.Base(current))
		current = parent
	}
}

func consolePathContains(root, path string) bool {
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		path = strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
