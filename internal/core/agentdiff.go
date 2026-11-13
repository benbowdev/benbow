// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/store"
)

// What an agent session changed: files against the starting snapshot, and
// the database's tables through the server's per-table row counters
// (MySQL performance_schema, PostgreSQL pg_stat_user_tables), read at the
// start of the session and now. Both are fast on any size.

// maxDiffEntries caps the files listed (the counts are always complete).
const maxDiffEntries = 500

// File changes.
const (
	ChangeAdded    = "added"
	ChangeModified = "modified"
	ChangeDeleted  = "deleted"
)

// SessionDiff is what changed since a site's agent session started.
type SessionDiff struct {
	Site     string    `json:"site"`
	Session  string    `json:"session"`
	Since    time.Time `json:"since"`
	Files    FileDiff  `json:"files"`
	Database *DBDiff   `json:"database,omitempty"`
}

// FileDiff counts and lists changed files (paths relative to the project).
type FileDiff struct {
	Added     int          `json:"added"`
	Modified  int          `json:"modified"`
	Deleted   int          `json:"deleted"`
	Changes   []FileChange `json:"changes"`
	Truncated bool         `json:"truncated,omitempty"`
}

// FileChange is one changed file.
type FileChange struct {
	Path   string `json:"path"`
	Change string `json:"change"`
	Size   int64  `json:"size"`
	// Sensitive files (.env, wp-config.php, keys) show masked values to
	// agents.
	Sensitive bool `json:"sensitive,omitempty"`
}

// DBDiff lists the tables that changed.
type DBDiff struct {
	Engine   string        `json:"engine"`
	Database string        `json:"database"`
	Tables   []TableChange `json:"tables"`
	// Approximate is set when the exact counters were lost (the database
	// server restarted during the session): row counts are estimates.
	Approximate bool   `json:"approximate,omitempty"`
	Note        string `json:"note,omitempty"`
}

// TableChange is one table's rows inserted, updated and deleted since the
// session started, or a table added, dropped or altered.
type TableChange struct {
	Table    string `json:"table"`
	Change   string `json:"change"` // rows, added, dropped, altered
	Inserted int64  `json:"inserted,omitempty"`
	Updated  int64  `json:"updated,omitempty"`
	Deleted  int64  `json:"deleted,omitempty"`
}

// dbStats is a database's counters at one moment.
type dbStats struct {
	Engine   string `json:"engine"`
	Database string `json:"database"`
	// ServerStart tells whether the counters still count from the same
	// point (they reset when the server restarts).
	ServerStart string               `json:"server_start"`
	Tables      map[string]tableStat `json:"tables"`
	At          time.Time            `json:"at"`
}

type tableStat struct {
	Inserted int64  `json:"i"`
	Updated  int64  `json:"u"`
	Deleted  int64  `json:"d"`
	Schema   string `json:"s"`
	Rows     int64  `json:"r"`
	// Changed is information_schema's UPDATE_TIME (MySQL), for the
	// approximate comparison.
	Changed string `json:"c,omitempty"`
}

const dbStatsFile = "dbstats.json"

// recordDBStats saves the database counters next to a session's starting
// snapshot. Without a running database server nothing is recorded (the
// diff then says so).
func (c *Core) recordDBStats(ctx context.Context, site store.Site, sn store.Snapshot) {
	if site.Database == "" {
		return
	}
	st, err := c.dbStats(ctx, site)
	if err != nil {
		return
	}
	b, _ := json.Marshal(st)
	_ = os.WriteFile(filepath.Join(filepath.Dir(sn.FilesDir), dbStatsFile), b, 0o600)
}

func (c *Core) dbStats(ctx context.Context, site store.Site) (dbStats, error) {
	st := dbStats{Engine: engineOf(site), Database: site.Database, Tables: map[string]tableStat{}, At: time.Now()}
	if st.Engine == EnginePostgres {
		return st, c.pgStats(ctx, &st)
	}
	return st, c.mysqlStats(ctx, mysqlFor(st.Engine), &st)
}

func (c *Core) mysqlStats(ctx context.Context, s mysqlServer, st *dbStats) error {
	db := strings.ReplaceAll(st.Database, "'", "''")
	expiry := "SET SESSION information_schema_stats_expiry = 0;\n" // 8.0+: fresh row counts
	if s.legacy {
		expiry = ""
	}
	rows, err := c.rootQueryOf(ctx, s, expiry+`SELECT 'start', UNIX_TIMESTAMP() - VARIABLE_VALUE, '', '', '', ''
  FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Uptime';
SELECT t.TABLE_NAME, IFNULL(t.TABLE_ROWS, 0), IFNULL(DATE_FORMAT(t.UPDATE_TIME, '%Y-%m-%d %H:%i:%s'), ''),
  IFNULL((SELECT SHA2(GROUP_CONCAT(CONCAT(c.COLUMN_NAME, ' ', c.COLUMN_TYPE) ORDER BY c.ORDINAL_POSITION SEPARATOR ','), 256)
    FROM information_schema.COLUMNS c WHERE c.TABLE_SCHEMA = t.TABLE_SCHEMA AND c.TABLE_NAME = t.TABLE_NAME), ''),
  IFNULL(CONCAT(w.COUNT_INSERT, ' ', w.COUNT_UPDATE, ' ', w.COUNT_DELETE), ''), 'table'
  FROM information_schema.TABLES t
  LEFT JOIN performance_schema.table_io_waits_summary_by_table w ON w.OBJECT_SCHEMA = t.TABLE_SCHEMA AND w.OBJECT_NAME = t.TABLE_NAME
  WHERE t.TABLE_SCHEMA = '`+db+`' AND t.TABLE_TYPE = 'BASE TABLE';`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		f := strings.Split(r, "\t")
		if len(f) < 6 {
			continue
		}
		if f[0] == "start" && f[5] == "" {
			st.ServerStart = f[1]
			continue
		}
		ts := tableStat{Schema: f[3], Changed: f[2]}
		ts.Rows, _ = strconv.ParseInt(f[1], 10, 64)
		if n := strings.Fields(f[4]); len(n) == 3 {
			ts.Inserted, _ = strconv.ParseInt(n[0], 10, 64)
			ts.Updated, _ = strconv.ParseInt(n[1], 10, 64)
			ts.Deleted, _ = strconv.ParseInt(n[2], 10, 64)
		}
		st.Tables[f[0]] = ts
	}
	if st.ServerStart == "" {
		return errcode.Errorf(errcode.Service, "MySQL's performance schema is off: restart Benbow to turn it on")
	}
	return nil
}

func (c *Core) pgStats(ctx context.Context, st *dbStats) error {
	rows, err := c.pgQuery(ctx, st.Database, `SELECT 'start' || E'\t' || pg_postmaster_start_time()::text || E'\t' || COALESCE((SELECT stats_reset::text FROM pg_stat_database WHERE datname = current_database()), '');
SELECT s.relname || E'\t' || s.n_tup_ins || E'\t' || s.n_tup_upd || E'\t' || s.n_tup_del || E'\t' || s.n_live_tup || E'\t' ||
  md5(COALESCE((SELECT string_agg(a.attname || ' ' || format_type(a.atttypid, a.atttypmod), ',' ORDER BY a.attnum)
    FROM pg_attribute a WHERE a.attrelid = s.relid AND a.attnum > 0 AND NOT a.attisdropped), ''))
  FROM pg_stat_user_tables s WHERE s.schemaname = 'public';`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		f := strings.Split(r, "\t")
		if len(f) == 3 && f[0] == "start" {
			st.ServerStart = f[1] + " " + f[2]
			continue
		}
		if len(f) != 6 {
			continue
		}
		ts := tableStat{Schema: f[5]}
		ts.Inserted, _ = strconv.ParseInt(f[1], 10, 64)
		ts.Updated, _ = strconv.ParseInt(f[2], 10, 64)
		ts.Deleted, _ = strconv.ParseInt(f[3], 10, 64)
		ts.Rows, _ = strconv.ParseInt(f[4], 10, 64)
		st.Tables[f[0]] = ts
	}
	return nil
}

// compareDBStats turns two counter readings into table changes.
func compareDBStats(before, after dbStats) DBDiff {
	d := DBDiff{Engine: after.Engine, Database: after.Database, Tables: []TableChange{}}
	exact := sameServerStart(before.ServerStart, after.ServerStart)
	if !exact {
		d.Approximate = true
		d.Note = "the database server restarted during the session: changed tables are found from row estimates and update times"
	}
	names := make([]string, 0, len(after.Tables))
	for n := range after.Tables {
		names = append(names, n)
	}
	for n := range before.Tables {
		if _, ok := after.Tables[n]; !ok {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	for _, n := range names {
		b, inBefore := before.Tables[n]
		a, inAfter := after.Tables[n]
		switch {
		case !inBefore:
			d.Tables = append(d.Tables, TableChange{Table: n, Change: "added", Inserted: a.Rows})
			continue
		case !inAfter:
			d.Tables = append(d.Tables, TableChange{Table: n, Change: "dropped", Deleted: b.Rows})
			continue
		}
		tc := TableChange{Table: n, Change: "rows"}
		if a.Schema != b.Schema {
			tc.Change = "altered"
		}
		if exact {
			tc.Inserted, tc.Updated, tc.Deleted = max(0, a.Inserted-b.Inserted), max(0, a.Updated-b.Updated), max(0, a.Deleted-b.Deleted)
		} else {
			if diff := a.Rows - b.Rows; diff > 0 {
				tc.Inserted = diff
			} else {
				tc.Deleted = -diff
			}
			if tc.Inserted == 0 && tc.Deleted == 0 && a.Changed != b.Changed && a.Changed != "" {
				tc.Updated = 1
			}
		}
		if tc.Change == "altered" || tc.Inserted+tc.Updated+tc.Deleted > 0 {
			d.Tables = append(d.Tables, tc)
		}
	}
	return d
}

// sameServerStart compares two readings of when the server started: exact
// strings (PostgreSQL), or Unix times computed from the uptime (MySQL),
// which may differ by a second or two.
func sameServerStart(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	x, err1 := strconv.ParseInt(a, 10, 64)
	y, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return x-y <= 3 && y-x <= 3
}

// DiffOptions narrow a session diff.
type DiffOptions struct {
	// Mask hides secret values in sensitive files (what agents see).
	Mask bool
}

// AgentSessionDiff returns what changed since the site's agent session
// started: files, and the database's tables.
func (c *Core) AgentSessionDiff(ctx context.Context, name string) (SessionDiff, error) {
	s, err := c.Store.OpenAgentSession(ctx, name)
	if err != nil {
		return SessionDiff{}, err
	}
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return SessionDiff{}, err
	}
	base, err := c.Store.Snapshot(ctx, name, s.BaseSnapshot)
	if err != nil {
		return SessionDiff{}, err
	}
	res := SessionDiff{Site: name, Session: s.ID, Since: s.OpenedAt}
	if res.Files, err = diffTrees(base.FilesDir, projectRoot(site)); err != nil {
		return res, err
	}
	if site.Database != "" {
		res.Database = c.diffDatabase(ctx, site, base)
	}
	return res, nil
}

func (c *Core) diffDatabase(ctx context.Context, site store.Site, base store.Snapshot) *DBDiff {
	d := &DBDiff{Engine: engineOf(site), Database: site.Database, Tables: []TableChange{}}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(base.FilesDir), dbStatsFile))
	var before dbStats
	if err == nil {
		err = json.Unmarshal(b, &before)
	}
	if err != nil || before.Database != site.Database {
		d.Note = "the database's state at the start of the session was not recorded (its server was not running)"
		return d
	}
	after, err := c.dbStats(ctx, site)
	if err != nil {
		d.Note = "cannot read the database now: " + err.Error()
		return d
	}
	out := compareDBStats(before, after)
	return &out
}

type fileStat struct {
	size  int64
	mtime time.Time
	mode  fs.FileMode
}

func walkFiles(root string) (map[string]fileStat, error) {
	out := map[string]fileStat{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if p != root && snapshotSkip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // gone while walking
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = fileStat{size: info.Size(), mtime: info.ModTime(), mode: info.Mode().Type()}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return out, nil
}

// diffTrees compares a snapshot's files with the project folder: size and
// modification time first, contents only when the size is the same and
// the time is not.
func diffTrees(before, after string) (FileDiff, error) {
	d := FileDiff{Changes: []FileChange{}}
	a, err := walkFiles(before)
	if err != nil {
		return d, err
	}
	b, err := walkFiles(after)
	if err != nil {
		return d, err
	}
	var all []FileChange
	for p, nb := range b {
		na, ok := a[p]
		switch {
		case !ok:
			d.Added++
			all = append(all, FileChange{Path: p, Change: ChangeAdded, Size: nb.size})
		case na.size != nb.size || na.mode != nb.mode || (!na.mtime.Equal(nb.mtime) && !sameContent(filepath.Join(before, p), filepath.Join(after, p))):
			d.Modified++
			all = append(all, FileChange{Path: p, Change: ChangeModified, Size: nb.size})
		}
	}
	for p, na := range a {
		if _, ok := b[p]; !ok {
			d.Deleted++
			all = append(all, FileChange{Path: p, Change: ChangeDeleted, Size: na.size})
		}
	}
	slices.SortFunc(all, func(x, y FileChange) int { return strings.Compare(x.Path, y.Path) })
	for i := range all {
		all[i].Sensitive = sensitiveFile(all[i].Path)
	}
	if len(all) > maxDiffEntries {
		all, d.Truncated = all[:maxDiffEntries], true
	}
	d.Changes = all
	return d, nil
}

func sameContent(a, b string) bool {
	ha, err1 := fileSHA256(a)
	hb, err2 := fileSHA256(b)
	return err1 == nil && err2 == nil && ha == hb
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", p, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	return string(h.Sum(nil)), nil
}

// FileDiffResult is one file's changes since the session started.
type FileDiffResult struct {
	Path   string `json:"path"`
	Change string `json:"change"`
	Binary bool   `json:"binary,omitempty"`
	Masked bool   `json:"masked,omitempty"`
	// Diff is a unified diff (empty for a binary or unchanged file).
	Diff string `json:"diff"`
}

// maxFileDiff caps one file's diff.
const maxFileDiff = 200 << 10

// AgentSessionFileDiff returns the unified diff of one file (relative to
// the project folder) since the site's session started.
func (c *Core) AgentSessionFileDiff(ctx context.Context, name, rel string, opts DiffOptions) (FileDiffResult, error) {
	res := FileDiffResult{Path: rel}
	clean := filepath.Clean(rel)
	if rel == "" || filepath.IsAbs(rel) || clean == ".." || strings.HasPrefix(clean, "../") {
		return res, errcode.Errorf(errcode.InvalidInput, "give a path inside the project folder, e.g. wp-content/themes/x/style.css")
	}
	for _, part := range strings.Split(clean, "/") {
		if snapshotSkip[part] {
			return res, errcode.Errorf(errcode.InvalidInput, "sessions leave "+snapshotSkipText+" out")
		}
	}
	s, err := c.Store.OpenAgentSession(ctx, name)
	if err != nil {
		return res, err
	}
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return res, err
	}
	base, err := c.Store.Snapshot(ctx, name, s.BaseSnapshot)
	if err != nil {
		return res, err
	}
	before, after := filepath.Join(base.FilesDir, clean), filepath.Join(projectRoot(site), clean)
	for _, p := range []string{before, after} {
		if fi, err := os.Lstat(p); err == nil && !fi.Mode().IsRegular() {
			return res, errcode.Errorf(errcode.InvalidInput, clean+" is not a regular file")
		}
	}
	_, errB := os.Stat(before)
	_, errA := os.Stat(after)
	switch {
	case errB != nil && errA != nil:
		return res, errcode.Errorf(errcode.NotFound, clean+" is in neither the session's start nor the folder")
	case errB != nil:
		res.Change, before = ChangeAdded, "/dev/null"
	case errA != nil:
		res.Change, after = ChangeDeleted, "/dev/null"
	default:
		res.Change = ChangeModified
	}
	for _, p := range []string{before, after} {
		if p != "/dev/null" && isBinary(p) {
			res.Binary = true
			return res, nil
		}
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/diff", "-u", "--label", "a/"+clean, "--label", "b/"+clean, before, after).Output()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return res, fmt.Errorf("diff %s: %w", clean, err)
	}
	res.Diff = string(out)
	if res.Diff == "" {
		res.Change = ""
	}
	if opts.Mask && sensitiveFile(clean) {
		res.Diff, res.Masked = MaskSecrets(clean, res.Diff), true
	}
	if len(res.Diff) > maxFileDiff {
		res.Diff = res.Diff[:maxFileDiff] + "\n… (diff cut at 200 KiB)"
	}
	return res, nil
}

func isBinary(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8000)
	n, _ := f.Read(buf)
	return bytes.IndexByte(buf[:n], 0) >= 0
}

// sensitiveFile reports files whose values agents only see masked.
func sensitiveFile(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env."),
		base == "wp-config.php", base == "auth.json", base == ".npmrc", base == ".htpasswd",
		strings.HasSuffix(base, ".pem"), strings.HasSuffix(base, ".key"), strings.HasSuffix(base, ".p12"),
		strings.HasPrefix(base, "id_rsa"), strings.HasPrefix(base, "id_ed25519"):
		return true
	}
	return false
}

const masked = "•••"

var (
	secretName = `[A-Za-z0-9_.-]*(?:PASS|PASSWORD|PASSWD|PWD|SECRET|TOKEN|KEY|SALT|AUTH|CREDENTIAL)[A-Za-z0-9_.-]*`
	// define('DB_PASSWORD', 'x');  define("AUTH_KEY", "x");
	reDefine = regexp.MustCompile(`(?i)(define\s*\(\s*['"]` + secretName + `['"]\s*,\s*)(['"]).*?(['"]\s*\))`)
	// KEY=value (.env), with optional quotes and export.
	reEnv = regexp.MustCompile(`(?i)^([+\- ]?\s*(?:export\s+)?` + secretName + `\s*=\s*).+$`)
	// "password": "x" (JSON)
	reJSON = regexp.MustCompile(`(?i)("` + `[A-Za-z0-9_.-]*(?:pass|password|secret|token|key|auth)[A-Za-z0-9_.-]*` + `"\s*:\s*)"[^"]*"`)
	// $var = 'x'; for PHP variables named like secrets.
	rePHPVar = regexp.MustCompile(`(?i)(\$` + secretName + `\s*=\s*)(['"]).*?(['"]\s*;)`)
)

// MaskSecrets replaces secret values in a sensitive file's text (or a diff
// of it): passwords, keys, salts and tokens; private key files entirely.
func MaskSecrets(path, text string) string {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".p12") ||
		strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") {
		lines := strings.Split(text, "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, "@@") || strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++") || l == "" {
				continue
			}
			lines[i] = l[:1] + masked
		}
		return strings.Join(lines, "\n")
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		l = reDefine.ReplaceAllString(l, "${1}${2}"+masked+"${3}")
		l = rePHPVar.ReplaceAllString(l, "${1}${2}"+masked+"${3}")
		l = reJSON.ReplaceAllString(l, `${1}"`+masked+`"`)
		if !strings.HasPrefix(l, "+++") && !strings.HasPrefix(l, "---") {
			l = reEnv.ReplaceAllString(l, "${1}"+masked)
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}
