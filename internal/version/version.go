// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package version reports the build version of Covo.
package version

import "runtime/debug"

// Version is set at release time via -ldflags "-X github.com/getcovo/covo/internal/version.Version=v0.1.0".
var Version = "dev"

// License is the SPDX identifier of Covo's license.
const License = "GPL-3.0-only"

// Info describes the running build.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Dirty   bool   `json:"dirty,omitempty"`
	Go      string `json:"go"`
	License string `json:"license"`
}

// Get returns the build info of the running binary.
func Get() Info {
	info := Info{Version: Version, License: License}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	info.Go = bi.GoVersion
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.modified":
			info.Dirty = s.Value == "true"
		}
	}
	return info
}
