// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
)

// AppDetection is what Covo proposes for an app's folder. The user always
// confirms (or edits) the command: it is a guess from the project's files.
type AppDetection struct {
	Path string `json:"path"`
	// Framework is e.g. "next", "vite", "django", or "" when unknown.
	Framework string `json:"framework,omitempty"`
	// Command is the proposed command ("" when nothing was recognised);
	// "{port}" stands for the port Covo gives the app.
	Command string `json:"command"`
	// PackageManager is npm, pnpm, yarn or bun for JavaScript projects.
	PackageManager string `json:"package_manager,omitempty"`
	// Notes explain the guess or what to check.
	Notes []string `json:"notes"`
}

// App frameworks Covo recognises (stored in Site.Framework).
const (
	FrameworkNext      = "next"
	FrameworkNuxt      = "nuxt"
	FrameworkAstro     = "astro"
	FrameworkSvelteKit = "sveltekit"
	FrameworkRemix     = "remix"
	FrameworkVite      = "vite"
	FrameworkCRA       = "create-react-app"
	FrameworkDjango    = "django"
	FrameworkFastAPI   = "fastapi"
	FrameworkFlask     = "flask"
	FrameworkRails     = "rails"
)

// DetectApp proposes a command for the project in dir.
func DetectApp(dir string) (AppDetection, error) {
	if !filepath.IsAbs(dir) {
		return AppDetection{}, errcode.Newf(errcode.InvalidInput, "%s is not an absolute path", dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return AppDetection{}, errcode.Newf(errcode.NotFound, "%s is not a folder", dir)
	}
	d := AppDetection{Path: dir, Notes: []string{}}
	switch {
	case fileExists(filepath.Join(dir, "package.json")):
		detectNode(dir, &d)
	case fileExists(filepath.Join(dir, "manage.py")) || fileExists(filepath.Join(dir, "pyproject.toml")) || fileExists(filepath.Join(dir, "requirements.txt")):
		detectPython(dir, &d)
	case fileExists(filepath.Join(dir, "Gemfile")) && fileExists(filepath.Join(dir, "bin", "rails")):
		d.Framework, d.Command = FrameworkRails, "bin/rails server -p {port}"
	}
	if d.Command == "" {
		d.Notes = append(d.Notes, "No dev server recognised: enter the command that starts it, and make it listen on the port Covo gives ($PORT or {port}).")
	}
	return d, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// nodeFrameworks maps a dependency to the framework, most specific first.
// portFlag is how the dev command takes a port (empty: it reads $PORT).
var nodeFrameworks = []struct {
	dep, framework, portFlag string
}{
	{"next", FrameworkNext, ""},
	{"nuxt", FrameworkNuxt, "--port {port}"},
	{"astro", FrameworkAstro, "--port {port}"},
	{"@sveltejs/kit", FrameworkSvelteKit, "--port {port} --strictPort"},
	{"@react-router/dev", FrameworkRemix, "--port {port} --strictPort"},
	{"@remix-run/dev", FrameworkRemix, "--port {port} --strictPort"},
	{"vite", FrameworkVite, "--port {port} --strictPort"},
	{"react-scripts", FrameworkCRA, ""},
}

func detectNode(dir string, d *AppDetection) {
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		PackageManager  string            `json:"packageManager"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || json.Unmarshal(b, &pkg) != nil {
		d.Notes = append(d.Notes, "package.json could not be read.")
		return
	}
	d.PackageManager = packageManager(dir, pkg.PackageManager)
	script := ""
	for _, s := range []string{"dev", "start", "serve"} {
		if _, ok := pkg.Scripts[s]; ok {
			script = s
			break
		}
	}
	portFlag := ""
	for _, f := range nodeFrameworks {
		_, dep := pkg.Dependencies[f.dep]
		_, dev := pkg.DevDependencies[f.dep]
		if dep || dev {
			d.Framework, portFlag = f.framework, f.portFlag
			break
		}
	}
	if script == "" {
		d.Notes = append(d.Notes, "package.json has no dev, start or serve script.")
		return
	}
	cmd := runScript(d.PackageManager, script)
	if portFlag != "" {
		// npm needs -- before the script's own arguments; pnpm, yarn and
		// bun pass them on.
		if d.PackageManager == "npm" {
			cmd += " --"
		}
		cmd += " " + portFlag
	} else {
		d.Notes = append(d.Notes, "The dev server should read the port from $PORT; if it does not, add its port option with {port}.")
	}
	d.Command = cmd
	if len(pkg.Dependencies)+len(pkg.DevDependencies) > 0 && !dirExists(filepath.Join(dir, "node_modules")) {
		d.Notes = append(d.Notes, "node_modules is missing: run "+d.PackageManager+" install first.")
	}
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// packageManager reads package.json's packageManager field, else the
// lockfile; npm when there is neither.
func packageManager(dir, field string) string {
	if name, _, _ := strings.Cut(field, "@"); name != "" {
		switch name {
		case "npm", "pnpm", "yarn", "bun":
			return name
		}
	}
	for _, l := range []struct{ file, pm string }{
		{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"package-lock.json", "npm"},
	} {
		if fileExists(filepath.Join(dir, l.file)) {
			return l.pm
		}
	}
	return "npm"
}

func runScript(pm, script string) string {
	switch pm {
	case "pnpm", "yarn":
		return pm + " " + script
	case "bun":
		return "bun run " + script
	}
	return "npm run " + script
}

var fastAPIRe = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*FastAPI\(`)

func detectPython(dir string, d *AppDetection) {
	python, prefix := pythonRunner(dir, d)
	deps := strings.ToLower(readSmall(filepath.Join(dir, "pyproject.toml")) + "\n" + readSmall(filepath.Join(dir, "requirements.txt")))
	switch {
	case fileExists(filepath.Join(dir, "manage.py")):
		d.Framework = FrameworkDjango
		d.Command = python + " manage.py runserver 127.0.0.1:{port}"
	case strings.Contains(deps, "fastapi"):
		d.Framework = FrameworkFastAPI
		for _, f := range []string{"main.py", "app.py", "app/main.py", "src/main.py"} {
			if m := fastAPIRe.FindStringSubmatch(readSmall(filepath.Join(dir, f))); m != nil {
				module := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(f, "src/"), ".py"), "/", ".")
				d.Command = prefix + "uvicorn " + module + ":" + m[1] + " --reload --port {port}"
				break
			}
		}
		if d.Command == "" {
			d.Notes = append(d.Notes, "FastAPI found, but not the module that creates the app: use uvicorn <module>:<app> --reload --port {port}.")
		}
	case strings.Contains(deps, "flask"):
		d.Framework = FrameworkFlask
		for _, f := range []string{"app.py", "wsgi.py", "main.py"} {
			if fileExists(filepath.Join(dir, f)) {
				d.Command = prefix + "flask --app " + strings.TrimSuffix(f, ".py") + " run --debug --port {port}"
				break
			}
		}
	}
}

// pythonRunner returns how to run python and its tools in dir: the
// project's virtual environment when there is one, else uv or Poetry when
// they manage it, else the python on the user's PATH.
func pythonRunner(dir string, d *AppDetection) (python, prefix string) {
	for _, venv := range []string{".venv", "venv"} {
		if fileExists(filepath.Join(dir, venv, "bin", "python")) {
			bin := venv + "/bin/"
			d.Notes = append(d.Notes, "Runs with the project's "+venv+".")
			return bin + "python", bin
		}
	}
	switch {
	case fileExists(filepath.Join(dir, "uv.lock")):
		return "uv run python", "uv run "
	case fileExists(filepath.Join(dir, "poetry.lock")):
		return "poetry run python", "poetry run "
	}
	return "python", ""
}

// readSmall reads a project file for detection, at most 256 KiB.
func readSmall(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 256<<10))
	return string(b)
}
