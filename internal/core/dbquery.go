// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/mysql"
	"github.com/getcovo/covo/internal/postgres"
)

// query_database: read-only SQL on a site's database, for agents (and the
// CLI). Three locks, so a check that misses something is not enough to
// write: only reading statements are accepted, they run in a read-only
// transaction, and as a user that can only read (MySQL: a dedicated user
// with SELECT and SHOW VIEW on that database; PostgreSQL: the site's role,
// whose read-only transaction it cannot leave from inside a SELECT).

const (
	maxQueryRows = 500
	queryTimeout = 10 * time.Second
)

// QueryResult is a query's columns and rows.
type QueryResult struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	// Truncated is set when there were more than the rows returned.
	Truncated bool  `json:"truncated,omitempty"`
	MS        int64 `json:"duration_ms"`
}

var (
	reSQLComment = regexp.MustCompile(`(?s)/\*.*?\*/|--[^\n]*|#[^\n]*`)
	reFirstWord  = regexp.MustCompile(`^\s*\(?\s*([A-Za-z]+)`)
	reForbidden  = regexp.MustCompile(`(?i)\b(INTO\s+(OUT|DUMP)FILE|FOR\s+UPDATE|FOR\s+SHARE|LOCK\s+IN\s+SHARE\s+MODE|LOAD_FILE|SLEEP|BENCHMARK|PG_SLEEP\w*|PG_READ\w*|PG_LS_DIR|PG_STAT_FILE|LO_\w+|DBLINK\w*|COPY)\b`)
)

// checkReadOnlySQL accepts one SELECT, SHOW, DESCRIBE, EXPLAIN or WITH
// statement and returns it without a trailing semicolon.
func checkReadOnlySQL(sql string) (string, error) {
	s := strings.TrimSpace(sql)
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	bare := reSQLComment.ReplaceAllString(s, " ")
	if s == "" {
		return "", errcode.Errorf(errcode.InvalidInput, "no query")
	}
	if strings.Contains(bare, ";") {
		return "", errcode.Errorf(errcode.InvalidInput, "one statement at a time (no ; inside, not even in strings)")
	}
	// The statement goes to the psql or mysql client, whose backslash
	// commands (\! runs a shell, \o and \T write files, \copy reads them)
	// work anywhere on a line, even inside what looks like a string.
	if strings.Contains(s, "\\") {
		return "", errcode.Errorf(errcode.Forbidden, "backslashes are not allowed in query_database (they are client commands)")
	}
	// MySQL runs the inside of /*! ... */ comments: what the checks below
	// see must be what the server runs.
	if strings.Contains(s, "/*!") {
		return "", errcode.Errorf(errcode.Forbidden, "/*! comments are not allowed in query_database")
	}
	m := reFirstWord.FindStringSubmatch(bare)
	if m == nil {
		return "", errcode.Errorf(errcode.InvalidInput, "start with SELECT, SHOW, DESCRIBE, EXPLAIN or WITH")
	}
	switch strings.ToUpper(m[1]) {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "WITH", "TABLE", "VALUES":
	default:
		return "", errcode.Errorf(errcode.Forbidden, fmt.Sprintf("%s is not a read: query_database only reads (changes go through wp or artisan)", strings.ToUpper(m[1])))
	}
	if f := reForbidden.FindString(bare); f != "" {
		return "", errcode.Errorf(errcode.Forbidden, fmt.Sprintf("%q is not allowed in query_database", f))
	}
	return s, nil
}

// QueryDatabase runs one reading statement on the site's database (at
// most 500 rows, 10 seconds).
func (c *Core) QueryDatabase(ctx context.Context, name, sql string) (QueryResult, error) {
	var res QueryResult
	stmt, err := checkReadOnlySQL(sql)
	if err != nil {
		return res, err
	}
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return res, err
	}
	if site.Database == "" {
		return res, errcode.Errorf(errcode.NotFound, name+" has no database in Covo")
	}
	start := time.Now()
	var out string
	if engineOf(site) == EnginePostgres {
		out, err = c.queryPostgresReadOnly(ctx, site.Name, stmt)
	} else {
		out, err = c.queryMySQLReadOnly(ctx, site.Database, stmt)
	}
	if err != nil {
		return res, err
	}
	res.MS = time.Since(start).Milliseconds()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		res.Columns, res.Rows = []string{}, [][]string{}
		return res, nil
	}
	res.Columns = strings.Split(lines[0], "\t")
	res.Rows = [][]string{}
	for _, l := range lines[1:] {
		if len(res.Rows) == maxQueryRows {
			res.Truncated = true
			break
		}
		res.Rows = append(res.Rows, strings.Split(l, "\t"))
	}
	c.TouchAgentSession(ctx, name)
	return res, nil
}

// readOnlyUser is the MySQL user that may only read db.
func readOnlyUser(db string) string {
	h := sha256.Sum256([]byte(db))
	return "covo_ro_" + hex.EncodeToString(h[:6])
}

func (c *Core) queryMySQLReadOnly(ctx context.Context, db, stmt string) (string, error) {
	client, port, _, err := c.mysqlRoot(ctx)
	if err != nil {
		return "", err
	}
	user := readOnlyUser(db)
	kc, err := mysqlKeychain(c.launchctl.Prefix)
	if err != nil {
		return "", err
	}
	kc = kc.For(user)
	pw, err := kc.Get(ctx)
	if err != nil || pw == "" {
		// First use: the user is made now (or given a new password).
		pw = mysql.NewPassword()
		if err := kc.Set(ctx, pw); err != nil {
			return "", err
		}
		sql := ""
		for _, host := range []string{"localhost", "127.0.0.1"} {
			sql += fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s';\n", user, host, pw)
			sql += fmt.Sprintf("ALTER USER '%s'@'%s' IDENTIFIED BY '%s';\n", user, host, pw)
			sql += fmt.Sprintf("GRANT SELECT, SHOW VIEW ON `%s`.* TO '%s'@'%s';\n", db, user, host)
		}
		if _, err := c.rootQuery(ctx, sql); err != nil {
			_, _ = kc.Delete(ctx)
			return "", err
		}
	}
	// LIMIT is left to the statement; rows past 500 are dropped here. The
	// timeout only stops SELECTs in MySQL, so the client stops too.
	sql := "SET SESSION max_execution_time = " + fmt.Sprint(queryTimeout.Milliseconds()) + ";\n" +
		"START TRANSACTION READ ONLY;\n" + stmt + ";\nROLLBACK;\n"
	return mysql.QueryAs(ctx, client, port, user, pw, db, sql, queryTimeout+5*time.Second)
}

func (c *Core) queryPostgresReadOnly(ctx context.Context, name, stmt string) (string, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return "", err
	}
	db, err := c.postgresSiteDatabase(ctx, site)
	if err != nil {
		return "", err
	}
	if !db.Dedicated || db.User == "" || db.User == postgres.Superuser {
		return "", errcode.Errorf(errcode.Unsupported, "this database has no role of its own in Covo")
	}
	dir, _, err := c.ensurePostgres(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout+5*time.Second)
	defer cancel()
	opts := "-c default_transaction_read_only=on -c statement_timeout=" + fmt.Sprint(queryTimeout.Milliseconds())
	sql := "BEGIN READ ONLY;\n" + stmt + ";\nROLLBACK;\n"
	return postgres.QueryAs(ctx, filepath.Join(dir, "bin", "psql"), db.Port, db.User, db.Password, site.Database, opts, sql, c.Layout.Tmp)
}
