// Package paths defines where Covo keeps everything on disk. All files Covo
// creates live under Root (~/Library/Application Support/covo by default); the
// only exception is the socket fallback directory, see RunDir.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/getcovo/covo/internal/errcode"
)

// EnvHome overrides the root directory (used by tests).
const EnvHome = "COVO_HOME"

// maxSocketPath is the usable length of sockaddr_un.sun_path on macOS
// (104 bytes including the trailing NUL).
const maxSocketPath = 103

// longestSocketName is the longest socket file name Covo creates in RunDir.
const longestSocketName = "caddy-admin.sock"

// Layout is the resolved set of Covo directories and files.
type Layout struct {
	Root    string `json:"root"`
	Bin     string `json:"bin"`
	Etc     string `json:"etc"`
	Launchd string `json:"launchd"`
	Data    string `json:"data"`
	Logs    string `json:"logs"`
	Tmp     string `json:"tmp"`
	// Run holds Unix sockets. It is Root/run unless that path is too long for
	// a socket, in which case it is a short per-user directory under $TMPDIR.
	Run  string `json:"run"`
	DB   string `json:"db"`
	Lock string `json:"lock"`
}

// Default returns the layout rooted at $COVO_HOME or the standard location.
func Default() (Layout, error) {
	root := os.Getenv(EnvHome)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Layout{}, fmt.Errorf("resolve home directory: %w", err)
		}
		root = filepath.Join(home, "Library", "Application Support", "covo")
	}
	return New(root)
}

// New returns the layout rooted at root, which must be absolute.
func New(root string) (Layout, error) {
	if !filepath.IsAbs(root) {
		return Layout{}, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("covo home must be an absolute path: %q", root))
	}
	root = filepath.Clean(root)
	l := Layout{
		Root:    root,
		Bin:     filepath.Join(root, "bin"),
		Etc:     filepath.Join(root, "etc"),
		Launchd: filepath.Join(root, "launchd"),
		Data:    filepath.Join(root, "data"),
		Logs:    filepath.Join(root, "logs"),
		Tmp:     filepath.Join(root, "tmp"),
		Run:     filepath.Join(root, "run"),
		DB:      filepath.Join(root, "covo.db"),
		Lock:    filepath.Join(root, "covo.lock"),
	}
	if len(filepath.Join(l.Run, longestSocketName)) > maxSocketPath {
		l.Run = fallbackRunDir(root)
	}
	return l, nil
}

// fallbackRunDir is a short directory unique to this user and this root, so
// that test homes never share sockets with the real one.
func fallbackRunDir(root string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(os.TempDir(), fmt.Sprintf("covo-%d-%s", os.Getuid(), hex.EncodeToString(sum[:4])))
}

// Ensure creates the directory tree with restrictive permissions.
func (l Layout) Ensure() error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{l.Root, 0o755},
		{l.Bin, 0o755},
		{l.Etc, 0o755},
		{l.Launchd, 0o755},
		{l.Logs, 0o755},
		{l.Tmp, 0o700},
		{l.Data, 0o700},
		{l.Run, 0o700},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return fmt.Errorf("create %s: %w", d.path, err)
		}
		if err := os.Chmod(d.path, d.mode); err != nil {
			return fmt.Errorf("chmod %s: %w", d.path, err)
		}
	}
	return l.checkRunDir()
}

// checkRunDir refuses a socket directory that is a symlink, not ours, or
// readable by others: it may live in a shared temp directory.
func (l Layout) checkRunDir() error {
	fi, err := os.Lstat(l.Run)
	if err != nil {
		return fmt.Errorf("stat %s: %w", l.Run, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.IsDir():
		return errcode.Errorf(errcode.Conflict, l.Run+" is not a directory")
	case !ok || int(st.Uid) != os.Getuid():
		return errcode.Errorf(errcode.Conflict, l.Run+" is not owned by the current user")
	case fi.Mode().Perm()&0o077 != 0:
		return errcode.Errorf(errcode.Conflict, l.Run+" is accessible by other users")
	}
	return nil
}

// Socket returns the path of a socket named name in the run directory.
func (l Layout) Socket(name string) string {
	return filepath.Join(l.Run, name)
}
