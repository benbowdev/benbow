// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package importers reads the sites of other local development tools
// (Local, Laravel Herd, Laravel Valet, XAMPP) without changing anything:
// their files are only read, and every field is optional, since none of
// these formats is documented. MAMP PRO lives in package mamp.
package importers

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/mamp"
)

// Sources Covo imports from.
const (
	MAMP  = "mamp"
	Local = "local"
	Herd  = "herd"
	Valet = "valet"
	XAMPP = "xampp"
)

// Names lists the sources.
var Names = []string{MAMP, Local, Herd, Valet, XAMPP}

// Title is a source's display name.
func Title(source string) string {
	return map[string]string{MAMP: "MAMP PRO", Local: "Local", Herd: "Laravel Herd", Valet: "Laravel Valet", XAMPP: "XAMPP"}[source]
}

// maxFile caps any configuration file read.
const maxFile = 8 << 20

func readSmall(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err //nolint:wrapcheck // callers check os.ErrNotExist
	}
	if fi.Size() > maxFile {
		return nil, fmt.Errorf("%s is too large", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return b, nil
}

func notFound(source, path string) error {
	return errcode.Errorf(errcode.NotFound, fmt.Sprintf("%s not found (no %s)", Title(source), path))
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// ReadLocal reads Local's sites.json: one MySQL server per site, reachable
// only while that site runs in Local.
func ReadLocal(home string) ([]mamp.Host, error) {
	dir := filepath.Join(home, "Library", "Application Support", "Local")
	path := filepath.Join(dir, "sites.json")
	b, err := readSmall(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, notFound(Local, path)
	}
	if err != nil {
		return nil, err
	}
	var sites map[string]struct {
		Name     string `json:"name"`
		Domain   string `json:"domain"`
		Path     string `json:"path"`
		Services map[string]struct {
			Version string           `json:"version"`
			Ports   map[string][]int `json:"ports"`
		} `json:"services"`
		MySQL struct {
			Database string `json:"database"`
			User     string `json:"user"`
			Password string `json:"password"`
		} `json:"mysql"`
	}
	if err := json.Unmarshal(b, &sites); err != nil {
		return nil, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("cannot read %s: %v", path, err))
	}
	var out []mamp.Host
	for id, s := range sites {
		if s.Domain == "" || s.Path == "" {
			continue
		}
		root := expandHome(s.Path, home)
		h := mamp.Host{ServerName: s.Domain, DocumentRoot: filepath.Join(root, "app", "public"), Aliases: []string{}, Databases: []string{}}
		if _, err := os.Stat(h.DocumentRoot); err != nil {
			h.DocumentRoot = root
		}
		if php, ok := s.Services["php"]; ok {
			h.PHPVersion = php.Version
		}
		db := &mamp.DBServer{Host: "127.0.0.1", User: s.MySQL.User, Password: s.MySQL.Password,
			Socket: filepath.Join(dir, "run", id, "mysql", "mysqld.sock")}
		for _, svc := range []string{"mysql", "mariadb"} {
			if m, ok := s.Services[svc]; ok && len(m.Ports["MYSQL"]) > 0 {
				db.Port = m.Ports["MYSQL"][0]
			}
		}
		if db.User == "" {
			db.User, db.Password = "root", "root"
		}
		h.DB = db
		if s.MySQL.Database != "" {
			h.Databases = []string{s.MySQL.Database}
		}
		out = append(out, h)
	}
	sortHosts(out)
	return out, nil
}

func sortHosts(hs []mamp.Host) {
	slices.SortFunc(hs, func(a, b mamp.Host) int { return strings.Compare(a.ServerName, b.ServerName) })
}

// valetLike reads a Valet configuration folder (Valet, or Herd's copy of
// it): parked folders (every subfolder is a site) and linked sites
// (symbolic links in Sites/), with the PHP version an isolated site
// declares in its Nginx file.
func valetLike(source, dir string) ([]mamp.Host, error) {
	cfgPath := filepath.Join(dir, "config.json")
	b, err := readSmall(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, notFound(source, cfgPath)
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		TLD   string   `json:"tld"`
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("cannot read %s: %v", cfgPath, err))
	}
	if cfg.TLD == "" {
		cfg.TLD = "test"
	}
	seen := map[string]bool{}
	var out []mamp.Host
	add := func(name, folder string) {
		domain := strings.ToLower(name) + "." + cfg.TLD
		if seen[domain] {
			return
		}
		seen[domain] = true
		out = append(out, mamp.Host{ServerName: domain, DocumentRoot: folder, PHPVersion: isolatedPHP(filepath.Join(dir, "Nginx", domain)),
			Aliases: []string{}, Databases: []string{}, SSL: fileExists(filepath.Join(dir, "Certificates", domain+".crt"))})
	}
	// Linked sites win over parked ones with the same name.
	if entries, err := os.ReadDir(filepath.Join(dir, "Sites")); err == nil {
		for _, e := range entries {
			target, err := filepath.EvalSymlinks(filepath.Join(dir, "Sites", e.Name()))
			if err == nil {
				add(e.Name(), target)
			}
		}
	}
	for _, p := range cfg.Paths {
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				add(e.Name(), filepath.Join(p, e.Name()))
			}
		}
	}
	for i := range out {
		out[i].DB = &mamp.DBServer{Host: "127.0.0.1", Port: 3306, User: "root"}
	}
	sortHosts(out)
	return out, nil
}

var isolatedRe = regexp.MustCompile(`ISOLATED_PHP_VERSION=(?:php@)?(\d+\.\d+)`)

func isolatedPHP(nginxFile string) string {
	b, err := readSmall(nginxFile)
	if err != nil {
		return ""
	}
	if m := isolatedRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ReadValet reads Laravel Valet's ~/.config/valet.
func ReadValet(home string) ([]mamp.Host, error) {
	return valetLike(Valet, filepath.Join(home, ".config", "valet"))
}

// ReadHerd reads Laravel Herd's copy of the Valet configuration.
func ReadHerd(home string) ([]mamp.Host, error) {
	return valetLike(Herd, filepath.Join(home, "Library", "Application Support", "Herd", "config", "valet"))
}

var (
	includeVhostsRe = regexp.MustCompile(`(?m)^\s*Include\s+\S*httpd-vhosts\.conf`)
	phpConfigRe     = regexp.MustCompile(`(?m)^version="(\d+\.\d+)`)
)

// ReadXAMPP reads XAMPP's Apache virtual hosts (only if httpd.conf includes
// them); every site uses XAMPP's one PHP version.
func ReadXAMPP(root string) ([]mamp.Host, error) {
	dir := filepath.Join(root, "Applications", "XAMPP", "xamppfiles")
	conf, err := readSmall(filepath.Join(dir, "etc", "httpd.conf"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, notFound(XAMPP, filepath.Join(dir, "etc", "httpd.conf"))
	}
	if err != nil {
		return nil, err
	}
	php := ""
	if b, err := readSmall(filepath.Join(dir, "bin", "php-config")); err == nil {
		if m := phpConfigRe.FindSubmatch(b); m != nil {
			php = string(m[1])
		}
	}
	db := &mamp.DBServer{Host: "127.0.0.1", Port: 3306, User: "root", Socket: filepath.Join(dir, "var", "mysql", "mysql.sock")}
	var out []mamp.Host
	if !includeVhostsRe.Match(conf) {
		return out, nil
	}
	f, err := os.Open(filepath.Join(dir, "etc", "extra", "httpd-vhosts.conf"))
	if err != nil {
		return out, nil //nolint:nilerr // included but missing: no virtual hosts
	}
	defer f.Close()
	var cur *mamp.Host
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		key := strings.ToLower(fields[0])
		switch {
		case strings.HasPrefix(key, "<virtualhost"):
			cur = &mamp.Host{PHPVersion: php, Aliases: []string{}, Databases: []string{}, DB: db,
				SSL: strings.Contains(line, ":443")}
		case key == "</virtualhost>":
			if cur != nil && cur.ServerName != "" && cur.DocumentRoot != "" {
				out = append(out, *cur)
			}
			cur = nil
		case cur == nil || len(fields) < 2:
		case key == "servername":
			cur.ServerName = strings.ToLower(fields[1])
		case key == "serveralias":
			for _, a := range fields[1:] {
				cur.Aliases = append(cur.Aliases, strings.ToLower(a))
			}
		case key == "documentroot":
			cur.DocumentRoot = strings.Trim(strings.Join(fields[1:], " "), `"`)
		}
	}
	sortHosts(out)
	return out, nil
}

// PortOf returns a DBServer's port as text, for messages.
func PortOf(d *mamp.DBServer) string {
	if d == nil || d.Port == 0 {
		return "3306"
	}
	return strconv.Itoa(d.Port)
}
