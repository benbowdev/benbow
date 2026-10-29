// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package mamp reads MAMP PRO's site list, read only.
//
// MAMP PRO keeps its hosts in settings6.plist, an NSKeyedArchiver archive.
// Covo never opens that file except to copy it: the copy is converted to XML
// by /usr/bin/plutil and parsed here. Only the fields Covo needs are read,
// lazily, so the archive's object cycles do not matter.
package mamp

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
)

// maxSettings caps the file copied and parsed (MAMP's is ~60 KB).
const maxSettings = 16 << 20

// DefaultSettings returns MAMP PRO's settings file for the current user.
func DefaultSettings() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "appsolute", "MAMP PRO", "settings6.plist"), nil
}

// Host is one MAMP PRO virtual host.
type Host struct {
	ServerName   string   `json:"server_name"`
	DocumentRoot string   `json:"document_root"`
	PHPVersion   string   `json:"php_version"`
	SSL          bool     `json:"ssl"`
	Aliases      []string `json:"aliases"`
	Databases    []string `json:"databases"`
}

// ReadCopy copies path into tmpDir, reads the hosts from the copy and
// deletes it. The original is only ever read, once, by the copy.
func ReadCopy(ctx context.Context, path, tmpDir string) ([]Host, error) {
	src, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errcode.Errorf(errcode.NotFound, "MAMP PRO settings not found at "+path)
	}
	if err != nil {
		return nil, fmt.Errorf("open MAMP PRO settings: %w", err)
	}
	defer src.Close()
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", tmpDir, err)
	}
	dst, err := os.CreateTemp(tmpDir, "mamp-settings-*.plist")
	if err != nil {
		return nil, fmt.Errorf("create copy: %w", err)
	}
	defer os.Remove(dst.Name())
	n, err := io.Copy(dst, io.LimitReader(src, maxSettings+1))
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("copy MAMP PRO settings: %w", err)
	}
	if n > maxSettings {
		return nil, errcode.Errorf(errcode.InvalidInput, path+" is too large")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", "--", dst.Name())
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, errcode.New(errcode.InvalidInput, fmt.Errorf("read MAMP PRO settings: %w: %s", err, strings.TrimSpace(errOut.String())))
	}
	return Parse(out.Bytes())
}

// Parse reads the hosts from the XML form of settings6.plist.
func Parse(xmlPlist []byte) ([]Host, error) {
	root, err := decodePlist(xmlPlist)
	if err != nil {
		return nil, errcode.New(errcode.InvalidInput, fmt.Errorf("read MAMP PRO settings: %w", err))
	}
	a, err := newArchive(root)
	if err != nil {
		return nil, errcode.New(errcode.InvalidInput, fmt.Errorf("read MAMP PRO settings: %w", err))
	}
	top, _ := root.(map[string]any)["$top"].(map[string]any)
	settings := a.dict(top["root"])
	if settings == nil {
		return nil, errcode.Errorf(errcode.InvalidInput, "read MAMP PRO settings: no root object")
	}
	var hosts []Host
	for _, v := range a.list(settings["virtualHosts"]) {
		h := a.dict(v)
		if h == nil {
			continue
		}
		host := Host{
			ServerName:   a.str(h["serverName"]),
			DocumentRoot: a.str(h["documentRoot"]),
			SSL:          a.boolean(h["ssl"]),
			Aliases:      []string{},
			Databases:    []string{},
		}
		if php := a.dict(h["phpVersion"]); php != nil {
			host.PHPVersion = a.str(php["versionString"])
		}
		for _, alias := range a.list(h["serverAliases"]) {
			if s := a.str(alias); s != "" {
				host.Aliases = append(host.Aliases, s)
			}
		}
		for _, db := range a.list(h["databases"]) {
			if d := a.dict(db); d != nil {
				if name := a.str(d["name"]); name != "" {
					host.Databases = append(host.Databases, name)
				}
			}
		}
		if host.ServerName != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts, nil
}

// uid is an NSKeyedArchiver object reference.
type uid int

type archive struct{ objects []any }

func newArchive(root any) (archive, error) {
	m, ok := root.(map[string]any)
	if !ok || m["$archiver"] != "NSKeyedArchiver" {
		return archive{}, errors.New("not an NSKeyedArchiver archive")
	}
	objs, ok := m["$objects"].([]any)
	if !ok {
		return archive{}, errors.New("no $objects")
	}
	return archive{objects: objs}, nil
}

func (a archive) deref(v any) any {
	if u, ok := v.(uid); ok {
		if int(u) >= 0 && int(u) < len(a.objects) {
			return a.objects[u]
		}
		return nil
	}
	return v
}

func (a archive) str(v any) string {
	switch x := a.deref(v).(type) {
	case string:
		if x == "$null" {
			return ""
		}
		return x
	case map[string]any:
		if s, ok := x["NS.string"].(string); ok {
			return s
		}
	}
	return ""
}

func (a archive) boolean(v any) bool {
	switch x := a.deref(v).(type) {
	case bool:
		return x
	case int64:
		return x != 0
	}
	return false
}

func (a archive) list(v any) []any {
	m, ok := a.deref(v).(map[string]any)
	if !ok {
		return nil
	}
	l, _ := m["NS.objects"].([]any)
	return l
}

// dict returns an NSDictionary as a map of its keys to their (unresolved)
// values, or an archived object's own fields.
func (a archive) dict(v any) map[string]any {
	m, ok := a.deref(v).(map[string]any)
	if !ok {
		return nil
	}
	keys, hasKeys := m["NS.keys"].([]any)
	if !hasKeys {
		return m
	}
	vals, _ := m["NS.objects"].([]any)
	out := map[string]any{}
	for i, k := range keys {
		if i < len(vals) {
			out[a.str(k)] = vals[i]
		}
	}
	return out
}

// decodePlist decodes an XML property list into map[string]any, []any,
// string, int64, float64, bool, []byte (data, undecoded) and uid.
func decodePlist(b []byte) (any, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("parse plist: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local != "plist" {
				return nil, fmt.Errorf("parse plist: unexpected <%s>", se.Name.Local)
			}
			v, end, err := decodeValue(d, 0)
			if err != nil {
				return nil, err
			}
			if end {
				return nil, errors.New("parse plist: empty")
			}
			return v, nil
		}
	}
}

const maxDepth = 64

// decodeValue reads the next value; end is true at a closing tag instead.
func decodeValue(d *xml.Decoder, depth int) (v any, end bool, err error) {
	if depth > maxDepth {
		return nil, false, errors.New("parse plist: too deep")
	}
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, false, fmt.Errorf("parse plist: %w", err)
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil, true, nil
		case xml.StartElement:
			return decodeElement(d, t, depth)
		}
	}
}

func decodeElement(d *xml.Decoder, se xml.StartElement, depth int) (any, bool, error) {
	text := func() (string, error) {
		var s string
		if err := d.DecodeElement(&s, &se); err != nil {
			return "", fmt.Errorf("parse plist <%s>: %w", se.Name.Local, err)
		}
		return s, nil
	}
	switch se.Name.Local {
	case "dict":
		m := map[string]any{}
		for {
			k, end, err := decodeValue(d, depth+1)
			if err != nil {
				return nil, false, err
			}
			if end {
				break
			}
			key, ok := k.(plistKey)
			if !ok {
				return nil, false, errors.New("parse plist: dict entry without key")
			}
			v, end, err := decodeValue(d, depth+1)
			if err != nil {
				return nil, false, err
			}
			if end {
				return nil, false, errors.New("parse plist: key without value")
			}
			m[string(key)] = v
		}
		if len(m) == 1 {
			if n, ok := m["CF$UID"].(int64); ok {
				return uid(n), false, nil
			}
		}
		return m, false, nil
	case "array":
		l := []any{}
		for {
			v, end, err := decodeValue(d, depth+1)
			if err != nil {
				return nil, false, err
			}
			if end {
				return l, false, nil
			}
			l = append(l, v)
		}
	case "key":
		s, err := text()
		return plistKey(s), false, err
	case "string", "date", "data":
		s, err := text()
		if se.Name.Local == "data" {
			return []byte(s), false, err
		}
		return s, false, err
	case "integer":
		s, err := text()
		if err != nil {
			return nil, false, err
		}
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("parse plist integer: %w", err)
		}
		return n, false, nil
	case "real":
		s, err := text()
		if err != nil {
			return nil, false, err
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, false, fmt.Errorf("parse plist real: %w", err)
		}
		return f, false, nil
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, false, fmt.Errorf("parse plist: %w", err)
		}
		return se.Name.Local == "true", false, nil
	}
	return nil, false, fmt.Errorf("parse plist: unexpected <%s>", se.Name.Local)
}

type plistKey string
