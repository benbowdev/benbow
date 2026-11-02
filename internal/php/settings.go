// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package php

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
)

// Settings are the php.ini values a user may change, per PHP minor version.
// post_max_size always follows upload_max_filesize.
type Settings struct {
	MemoryLimit       string `json:"memory_limit"`        // e.g. 512M, 1G, -1
	UploadMaxFilesize string `json:"upload_max_filesize"` // e.g. 128M
	MaxExecutionTime  int    `json:"max_execution_time"`  // seconds, 0 = no limit
	DisplayErrors     bool   `json:"display_errors"`
}

// DefaultSettings suit WordPress development.
func DefaultSettings() Settings {
	return Settings{MemoryLimit: "512M", UploadMaxFilesize: "128M", MaxExecutionTime: 300, DisplayErrors: true}
}

// SettingNames are the keys accepted by Set, in display order.
var SettingNames = []string{"memory_limit", "upload_max_filesize", "max_execution_time", "display_errors"}

var sizeRe = regexp.MustCompile(`^[1-9][0-9]{0,5}[KMG]$`)

// Set changes one setting from its php.ini name and a user-typed value.
func (s *Settings) Set(name, value string) error {
	value = strings.TrimSpace(value)
	switch name {
	case "memory_limit":
		v := strings.ToUpper(value)
		if v != "-1" && !sizeRe.MatchString(v) {
			return invalid(name, value, "a size like 512M or 1G, or -1 for no limit")
		}
		s.MemoryLimit = v
	case "upload_max_filesize":
		v := strings.ToUpper(value)
		if !sizeRe.MatchString(v) {
			return invalid(name, value, "a size like 128M or 1G")
		}
		s.UploadMaxFilesize = v
	case "max_execution_time":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 86400 {
			return invalid(name, value, "seconds from 0 (no limit) to 86400")
		}
		s.MaxExecutionTime = n
	case "display_errors":
		switch strings.ToLower(value) {
		case "on", "1", "true", "yes":
			s.DisplayErrors = true
		case "off", "0", "false", "no":
			s.DisplayErrors = false
		default:
			return invalid(name, value, "on or off")
		}
	default:
		return errcode.Errorf(errcode.InvalidInput,
			fmt.Sprintf("unknown PHP setting %q; Covo manages %s", name, strings.Join(SettingNames, ", ")))
	}
	return nil
}

// Validate checks every field, e.g. after reading them from storage.
func (s Settings) Validate() error {
	var probe Settings
	for name, value := range map[string]string{
		"memory_limit":        s.MemoryLimit,
		"upload_max_filesize": s.UploadMaxFilesize,
		"max_execution_time":  strconv.Itoa(s.MaxExecutionTime),
	} {
		if err := probe.Set(name, value); err != nil {
			return err
		}
	}
	return nil
}

func invalid(name, value, want string) error {
	return errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("%s %q: want %s", name, value, want))
}
