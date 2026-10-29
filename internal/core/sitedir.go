package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/paths"
)

// systemTrees are folders whose whole subtree must never be served.
var systemTrees = []string{
	"/System", "/Library", "/Applications", "/usr", "/bin", "/sbin",
	"/etc", "/private/etc", "/dev", "/cores", "/private/var/db",
}

// systemRoots are folders that must not be served themselves (their
// subfolders may hold legitimate sites, e.g. temp dirs under /private/var).
var systemRoots = []string{
	"/", "/Users", "/Volumes", "/private", "/var", "/private/var",
	"/tmp", "/private/tmp", "/opt", "/Network",
}

// checkSiteDir validates a site document root. home is the user's home and
// covoRoot the Covo home; both are resolved through symlinks like p.
func checkSiteDir(p, home, covoRoot string) error {
	if err := paths.CheckSafe(p); err != nil {
		return err
	}
	fi, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return errcode.Errorf(errcode.NotFound, fmt.Sprintf("folder %q does not exist", p))
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", p, err)
	}
	if !fi.IsDir() {
		return errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("%q is not a folder", p))
	}

	resolved := resolve(p)
	home = resolve(home)
	covoRoot = resolve(covoRoot)
	forbidden := func(why string) error {
		return errcode.Errorf(errcode.ForbiddenFolder, fmt.Sprintf("refusing to serve %s: %s", p, why))
	}
	for _, root := range systemRoots {
		if resolved == resolve(root) {
			return forbidden("it is a system folder")
		}
	}
	if resolved == home {
		return forbidden("it is your home folder")
	}
	if filepath.Dir(resolved) == "/Volumes" {
		return forbidden("it is the root of a disk")
	}
	for _, tree := range append(systemTrees, filepath.Join(home, "Library")) {
		if within(resolved, resolve(tree)) {
			return forbidden("it is inside a system folder")
		}
	}
	if within(resolved, covoRoot) || within(covoRoot, resolved) {
		return forbidden("it overlaps the Covo data folder")
	}
	return nil
}

// resolve returns p with symlinks evaluated, or p cleaned if that fails.
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// within reports whether p is base or inside it.
func within(p, base string) bool {
	if p == base {
		return true
	}
	if base == "/" {
		return true
	}
	return strings.HasPrefix(p, base+string(filepath.Separator))
}

func hasIndex(dir string) bool {
	for _, name := range []string{"index.php", "index.html"} {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}
