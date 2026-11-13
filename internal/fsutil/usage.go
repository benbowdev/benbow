// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package fsutil

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// attrCmnExtPrivateSize is ATTR_CMNEXT_PRIVATESIZE (sys/attr.h, macOS
// 10.15+): the bytes of a file that no APFS clone shares.
const attrCmnExtPrivateSize = 0x00000008

// Usage is the space a folder takes.
type Usage struct {
	// Apparent is what du reports: every file's blocks, shared or not.
	Apparent int64 `json:"apparent"`
	// Private is what deleting the folder would give back: blocks no APFS
	// clone outside shares. Equal to Apparent on other file systems.
	Private int64 `json:"private"`
	Files   int   `json:"files"`
}

// DiskUsage walks dir (or one file) and adds up its files.
func DiskUsage(dir string) (Usage, error) {
	var u Usage
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // gone meanwhile
		}
		blocks := fi.Size()
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			blocks = st.Blocks * 512
		}
		u.Files++
		u.Apparent += blocks
		if n, err := privateSize(p); err == nil {
			u.Private += n
		} else {
			u.Private += blocks
		}
		return nil
	})
	if err != nil {
		return u, fmt.Errorf("measure %s: %w", dir, err)
	}
	return u, nil
}

// privateSize asks APFS how many of a file's bytes no clone shares.
func privateSize(path string) (int64, error) {
	al := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Forkattr: attrCmnExtPrivateSize}
	// A uint32 length, then the off_t packed right after it (offset 4).
	var buf [16]byte
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("path %q: %w", path, err)
	}
	// getattrlist(2) has no wrapper in x/sys/unix, and its number has not
	// changed since Mac OS X 10.0. On any error the caller counts the file
	// as private, the safe side.
	//nolint:gosec,staticcheck // see above
	_, _, e := syscall.Syscall6(unix.SYS_GETATTRLIST, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&al)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unix.FSOPT_ATTR_CMN_EXTENDED|unix.FSOPT_NOFOLLOW), 0)
	if e != 0 {
		return 0, fmt.Errorf("getattrlist %s: %w", path, e)
	}
	return int64(binary.LittleEndian.Uint64(buf[4:12])), nil
}
