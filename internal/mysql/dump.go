// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package mysql

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
)

// Server is how to reach a MySQL server: TCP host and port, or a socket.
type Server struct {
	Host     string
	Port     int
	Socket   string
	User     string
	Password string
}

// optionFile is the [client] section for s, handed to the client through
// a pipe (never a file on disk or the command line).
func (s Server) optionFile() string {
	var b strings.Builder
	b.WriteString("[client]\n")
	fmt.Fprintf(&b, "user=%s\n", optionValue(s.User))
	fmt.Fprintf(&b, "password=%s\n", optionValue(s.Password))
	if s.Socket != "" {
		fmt.Fprintf(&b, "socket=%s\nprotocol=SOCKET\n", optionValue(s.Socket))
	} else {
		host := s.Host
		if host == "" || host == "localhost" {
			host = "127.0.0.1"
		}
		port := s.Port
		if port == 0 {
			port = 3306
		}
		fmt.Fprintf(&b, "host=%s\nport=%d\nprotocol=TCP\n", optionValue(host), port)
	}
	return b.String()
}

// optionValue quotes a value for a MySQL option file.
func optionValue(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v) + `"`
}

// startWithFD3 starts cmd with body readable on fd 3 and returns a wait
// function.
func startWithFD3(cmd *exec.Cmd, body string) (func() error, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pipe: %w", err)
	}
	cmd.ExtraFiles = []*os.File{r}
	if err := cmd.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	_ = r.Close()
	_, werr := io.WriteString(w, body)
	_ = w.Close()
	return func() error {
		err := cmd.Wait()
		if err == nil && werr != nil {
			err = fmt.Errorf("write to %s: %w", cmd.Path, werr)
		}
		return err
	}, nil
}

// Dump writes a consistent dump of db on src to w (read only: one
// transaction, no locks on InnoDB tables).
func Dump(ctx context.Context, mysqldump string, src Server, db string, w io.Writer) error {
	var errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, mysqldump, "--defaults-extra-file=/dev/fd/3",
		"--single-transaction", "--quick", "--routines", "--triggers", "--no-tablespaces", "--hex-blob",
		"--skip-column-statistics", "--set-gtid-purged=OFF", "--default-character-set=utf8mb4", "--databases", db)
	cmd.Stdout, cmd.Stderr = w, &errOut
	wait, err := startWithFD3(cmd, src.optionFile())
	if err != nil {
		return errcode.New(errcode.Service, err)
	}
	if err := wait(); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("mysqldump %s: %w: %s", db, err, strings.TrimSpace(errOut.String())))
	}
	return nil
}

// Restore runs the SQL from r as root on Covo's server, into db (which
// must exist).
func Restore(ctx context.Context, client string, port int, password, db string, r io.Reader) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	var errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, client, "--defaults-extra-file=/dev/fd/3",
		"--protocol=TCP", "-h", "127.0.0.1", "-P", strconv.Itoa(port), "-u", User,
		"--default-character-set=utf8mb4", "--binary-mode", db)
	cmd.Stdin, cmd.Stderr = r, &errOut
	wait, err := startWithFD3(cmd, "[client]\npassword="+password+"\n")
	if err != nil {
		return errcode.New(errcode.Service, err)
	}
	if err := wait(); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("mysql import into %s: %w: %s", db, err, strings.TrimSpace(errOut.String())))
	}
	return nil
}

var (
	definerRe    = regexp.MustCompile("DEFINER=`[^`]*`@`[^`]*` ?")
	useDBRe      = regexp.MustCompile("^(CREATE DATABASE|USE) ")
	createDBLine = regexp.MustCompile(`^/\*!40000 DROP DATABASE|^CREATE DATABASE`)
)

// Clean copies a dump from r to w for loading into another database:
// DEFINER clauses go (the source's users do not exist here), and so do
// the dump's own CREATE DATABASE and USE lines (the target is chosen by
// Restore). Lines of any length are handled.
func Clean(r io.Reader, w io.Writer) error {
	br := bufio.NewReaderSize(r, 1<<20)
	bw := bufio.NewWriterSize(w, 1<<20)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			switch {
			case len(line) < 512 && (useDBRe.MatchString(line) || createDBLine.MatchString(line)):
				// skip
			case strings.Contains(line, "DEFINER="):
				if _, werr := bw.WriteString(definerRe.ReplaceAllString(line, "")); werr != nil {
					return fmt.Errorf("write dump: %w", werr)
				}
			default:
				if _, werr := bw.WriteString(line); werr != nil {
					return fmt.Errorf("write dump: %w", werr)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read dump: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("write dump: %w", err)
	}
	return nil
}

// Copy dumps db from src and loads it into dstDB on Covo's server,
// streaming (nothing is written to disk) and cleaned (see Clean).
func Copy(ctx context.Context, mysqldump, client string, src Server, db string, dstPort int, dstPassword, dstDB string) error {
	pr, pw := io.Pipe()
	cr, cw := io.Pipe()
	errs := make(chan error, 2)
	go func() {
		err := Dump(ctx, mysqldump, src, db, pw)
		_ = pw.CloseWithError(err)
		errs <- err
	}()
	go func() {
		err := Clean(pr, cw)
		_ = cw.CloseWithError(err)
		_ = pr.CloseWithError(err)
		errs <- err
	}()
	restoreErr := Restore(ctx, client, dstPort, dstPassword, dstDB, cr)
	_ = cr.CloseWithError(restoreErr)
	first := <-errs
	second := <-errs
	for _, err := range []error{first, second, restoreErr} {
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return err
		}
	}
	return nil
}
