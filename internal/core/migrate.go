// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/dotenv"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/mamp"
	"github.com/getcovo/covo/internal/mysql"
	"github.com/getcovo/covo/internal/store"
	"github.com/getcovo/covo/internal/wpconfig"
)

// Assisted migration: after an import created a site on the same folder as
// the source tool, Covo copies its database (the source is only read),
// points wp-config.php or .env at the copy (after a backup in the Covo
// folder) and replaces the old address in the database. The project's files
// stay shared with the source tool: once migrated the site works from
// Covo, and the source tool, reading the same wp-config.php, no longer
// reaches its own database.

// What to do when Covo already has a database of the same name.
const (
	ExistingUse    = "use"
	ExistingRecopy = "recopy"
	ExistingSkip   = "skip"
)

// MigrationPlan says what migrating one site does (also in a dry run).
type MigrationPlan struct {
	Framework string `json:"framework"`
	// Database is the source database to copy; empty when unknown.
	Database string `json:"database,omitempty"`
	// Server is where it is read from (host:port or socket).
	Server string `json:"server,omitempty"`
	// Exists is true when Covo already has a database of that name.
	Exists bool `json:"exists"`
	// Config is the file Covo changes (wp-config.php or .env), if any.
	Config string `json:"config,omitempty"`
	// URLFrom and URLTo: the old address replaced in the database.
	URLFrom string `json:"url_from,omitempty"`
	URLTo   string `json:"url_to,omitempty"`
	// Steps are what will happen, in words.
	Steps []string `json:"steps"`
	// Manual are settings to make by hand (sites Covo cannot configure).
	Manual map[string]string `json:"manual,omitempty"`
	// Problem, when set, means the site cannot be migrated: why.
	Problem string `json:"problem,omitempty"`
	// Done is set after a successful migration.
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`
}

// MigrateOptions are the user's choices for a migration.
type MigrateOptions struct {
	// Existing: use, recopy or skip (default use).
	Existing string
	// DumpFile, when set, is loaded instead of reading the source server.
	DumpFile string
	// SourceUser and SourcePassword override the credentials found in the
	// project's configuration. Kept in memory only.
	SourceUser, SourcePassword string
}

// projectDB is the database a project's own configuration uses.
type projectDB struct {
	framework, configPath string
	server                mysql.Server
	db, prefix            string
}

// readProjectDB reads the database settings of a WordPress (wp-config.php
// in the folder or the one above, as WordPress allows) or Laravel (.env)
// project. h.DB fills in what "localhost" means for the source tool.
func readProjectDB(site store.Site, h mamp.Host) (projectDB, error) {
	var p projectDB
	def := mamp.DBServer{Host: "127.0.0.1", Port: 3306, User: "root"}
	if h.DB != nil {
		def = *h.DB
	}
	resolveHost := func(host string) mysql.Server {
		s := mysql.Server{Host: def.Host, Port: def.Port, Socket: def.Socket}
		name, rest, hasRest := strings.Cut(host, ":")
		switch {
		case hasRest && strings.HasPrefix(rest, "/"):
			s = mysql.Server{Socket: rest}
		case hasRest:
			port, _ := strconv.Atoi(rest)
			s = mysql.Server{Host: name, Port: port}
		case host == "localhost" && def.Socket != "" && fileIsSocket(def.Socket):
			s = mysql.Server{Socket: def.Socket}
		case host != "" && host != "localhost":
			s = mysql.Server{Host: host, Port: def.Port}
		}
		return s
	}
	if site.Framework == FrameworkLaravel {
		p.framework, p.configPath = FrameworkLaravel, filepath.Join(projectRoot(site), ".env")
		b, err := os.ReadFile(p.configPath)
		if err != nil {
			return p, errcode.Errorf(errcode.NotFound, "no .env in "+projectRoot(site))
		}
		env := string(b)
		if conn, _ := dotenv.Get(env, "DB_CONNECTION"); conn != "" && conn != "mysql" && conn != "mariadb" {
			return p, errcode.Errorf(errcode.Unsupported, "the project uses "+conn+": Covo migrates MySQL databases")
		}
		host, _ := dotenv.Get(env, "DB_HOST")
		port, _ := dotenv.Get(env, "DB_PORT")
		if port != "" {
			host += ":" + port
		}
		p.server = resolveHost(host)
		p.server.User, _ = dotenv.Get(env, "DB_USERNAME")
		p.server.Password, _ = dotenv.Get(env, "DB_PASSWORD")
		p.db, _ = dotenv.Get(env, "DB_DATABASE")
		return p, nil
	}
	for _, dir := range []string{site.Path, filepath.Dir(site.Path)} {
		path := filepath.Join(dir, "wp-config.php")
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		cfg := wpconfig.Read(string(b))
		p.framework, p.configPath, p.prefix = FrameworkWordPress, path, cfg.TablePrefix
		p.server = resolveHost(cfg.Values["DB_HOST"])
		p.server.User, p.server.Password = cfg.Values["DB_USER"], cfg.Values["DB_PASSWORD"]
		p.db = cfg.Values["DB_NAME"]
		return p, nil
	}
	return p, nil
}

func fileIsSocket(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

func describeServer(s mysql.Server) string {
	if s.Socket != "" {
		return s.Socket
	}
	return s.Host + ":" + strconv.Itoa(s.Port)
}

// planMigration describes the migration of a site about to be (or just)
// imported from h.
func (c *Core) planMigration(ctx context.Context, site store.Site, h mamp.Host, url string, opts MigrateOptions) MigrationPlan {
	plan := MigrationPlan{Framework: site.Framework, URLTo: url, Steps: []string{}}
	p, err := readProjectDB(site, h)
	if err != nil {
		plan.Problem = err.Error()
		return plan
	}
	if p.framework == "" {
		plan.Problem = "not a WordPress or Laravel project: set its database by hand"
		plan.Manual = map[string]string{}
		if db, err := c.mysqlStatus(ctx); err == nil && db.Enabled {
			plan.Manual["host"] = "127.0.0.1:" + strconv.Itoa(db.Port)
		}
		return plan
	}
	plan.Framework, plan.Config = p.framework, p.configPath
	if p.db == "" && opts.DumpFile == "" {
		plan.Problem = "no database name in " + filepath.Base(p.configPath)
		return plan
	}
	plan.Database, plan.Server = p.db, describeServer(p.server)
	if dbs, err := c.Databases(ctx); err == nil && slices.Contains(dbs, migrationDBName(site, p)) {
		plan.Exists = true
	}
	switch {
	case opts.DumpFile != "":
		plan.Steps = append(plan.Steps, "load "+opts.DumpFile+" into a new database "+migrationDBName(site, p))
	case plan.Exists && (opts.Existing == "" || opts.Existing == ExistingUse):
		plan.Steps = append(plan.Steps, "use Covo's existing database "+p.db+" (not copied again)")
	case plan.Exists && opts.Existing == ExistingRecopy:
		plan.Steps = append(plan.Steps, "back up Covo's database "+p.db+", then copy it again from "+plan.Server)
	case plan.Exists && opts.Existing == ExistingSkip:
		plan.Problem = "Covo already has database " + p.db + ": skipped as asked"
		return plan
	default:
		plan.Steps = append(plan.Steps, "copy database "+p.db+" from "+plan.Server+" (only read)")
	}
	plan.Steps = append(plan.Steps, "give the site its own MySQL user on that database")
	plan.Steps = append(plan.Steps, "back up "+filepath.Base(p.configPath)+" in Covo's folder, then set its database settings to Covo's")
	if p.framework == FrameworkWordPress {
		plan.Steps = append(plan.Steps, "replace the old address with "+url+" in the database (wp search-replace)")
	} else {
		plan.Steps = append(plan.Steps, "set APP_URL to "+url)
	}
	return plan
}

// migrationDBName is the database the migrated site uses on Covo: the
// source's name (valid MySQL names only), else the site's default.
func migrationDBName(site store.Site, p projectDB) string {
	if p.db != "" && mysql.ValidName(p.db, 64) {
		return p.db
	}
	return DefaultDatabaseName(site.Name)
}

// migrateSite runs a planned migration. On failure everything it did is
// undone and the site stays as imported.
func (c *Core) migrateSite(ctx context.Context, source string, site store.Site, h mamp.Host, url string, opts MigrateOptions) (MigrationPlan, error) {
	plan := c.planMigration(ctx, site, h, url, opts)
	if plan.Problem != "" {
		return plan, errcode.Errorf(errcode.Unsupported, plan.Problem)
	}
	p, err := readProjectDB(site, h)
	if err != nil {
		return plan, err
	}
	if opts.SourceUser != "" {
		p.server.User, p.server.Password = opts.SourceUser, opts.SourcePassword
	}
	client, port, rootPW, err := c.mysqlRoot(ctx)
	if err != nil {
		return plan, err
	}
	dbName := migrationDBName(site, p)
	mig := store.Migration{Site: site.Name, Source: source, DBName: dbName, ConfigPath: p.configPath, URLTo: url, At: time.Now()}
	var undo []func()
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	bg := context.WithoutCancel(ctx)

	// 1. The database.
	copyIn := true
	switch {
	case plan.Exists && (opts.Existing == "" || opts.Existing == ExistingUse) && opts.DumpFile == "":
		copyIn = false
	case plan.Exists:
		backup, err := c.backupDatabase(ctx, site.Name, dbName)
		if err != nil {
			return plan, err
		}
		if _, err := c.rootQuery(ctx, "DROP DATABASE `"+dbName+"`;"); err != nil {
			return plan, err
		}
		undo = append(undo, func() { _ = c.restoreDatabaseBackup(bg, dbName, backup) })
	}
	if copyIn {
		if _, err := c.rootQuery(ctx, "CREATE DATABASE `"+dbName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"); err != nil {
			rollback()
			return plan, err
		}
		mig.CreatedDB = true
		undo = append(undo, func() { _, _ = c.rootQuery(bg, "DROP DATABASE IF EXISTS `"+dbName+"`;") })
		if err := c.loadIntoDatabase(ctx, p, dbName, opts.DumpFile, client, port, rootPW); err != nil {
			rollback()
			return plan, err
		}
	}
	db, err := c.attachSiteUser(ctx, site.Name, dbName)
	if err != nil {
		rollback()
		return plan, err
	}
	mig.DBUser = db.User
	undo = append(undo, func() { c.detachSiteUser(bg, site.Name, db.User) })

	// 2. The project's configuration, after a backup in Covo's folder.
	backup, err := c.backupFile(site.Name, p.configPath)
	if err != nil {
		rollback()
		return plan, err
	}
	mig.BackupPath = backup
	undo = append(undo, func() { _ = copyFile(backup, p.configPath) })
	host := "127.0.0.1:" + strconv.Itoa(port)
	if err := c.pointConfigAt(p, db, host, url); err != nil {
		rollback()
		return plan, err
	}

	// 3. The address (WordPress keeps it in the database).
	if p.framework == FrameworkWordPress {
		stored, err := c.Store.Site(ctx, site.Name)
		if err != nil {
			rollback()
			return plan, err
		}
		old, err := c.wpHome(ctx, dbName, p.prefix)
		if err == nil && old != "" && old != url {
			plan.URLFrom, mig.URLFrom = old, old
			if err := c.replaceURL(ctx, stored, old, url); err != nil {
				rollback()
				return plan, err
			}
		}
	}
	if err := c.Store.PutMigration(ctx, mig); err != nil {
		rollback()
		return plan, err
	}
	plan.Done = true
	return plan, nil
}

// loadIntoDatabase copies the source database, or loads a dump file
// (.sql or .sql.gz), into dbName.
func (c *Core) loadIntoDatabase(ctx context.Context, p projectDB, dbName, dumpFile, client string, port int, rootPW string) error {
	dir := filepath.Dir(client)
	if dumpFile == "" {
		err := mysql.Copy(ctx, filepath.Join(dir, "mysqldump"), client, p.server, p.db, port, rootPW, dbName)
		if err != nil {
			return errcode.New(errcode.Service, fmt.Errorf("%w (is the source's MySQL running? Covo never starts it)", err))
		}
		return nil
	}
	f, err := os.Open(dumpFile)
	if err != nil {
		return errcode.New(errcode.NotFound, fmt.Errorf("open dump: %w", err))
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(dumpFile, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return errcode.New(errcode.InvalidInput, fmt.Errorf("read %s: %w", dumpFile, err))
		}
		defer gz.Close()
		r = gz
	}
	pr, pw := io.Pipe()
	go func() { _ = pw.CloseWithError(mysql.Clean(r, pw)) }()
	return mysql.Restore(ctx, client, port, rootPW, dbName, pr)
}

// attachSiteUser gives the site its own MySQL user on an existing database
// (password in the keychain) and links them.
func (c *Core) attachSiteUser(ctx context.Context, name, db string) (SiteDatabase, error) {
	var res SiteDatabase
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		user := db[:min(len(db), 32)]
		if user == mysql.User {
			user = "site_" + user
		}
		users, err := c.rootQuery(ctx, "SELECT DISTINCT user FROM mysql.user WHERE user = '"+user+"';")
		if err != nil {
			return err
		}
		kc, err := mysqlKeychain(c.launchctl.Prefix)
		if err != nil {
			return err
		}
		kc = kc.For(user)
		pw := mysql.NewPassword()
		if err := kc.Set(ctx, pw); err != nil {
			return err
		}
		verb := "CREATE USER"
		if len(users) > 0 {
			verb = "ALTER USER" // an earlier migration's user: new password
		}
		sql := ""
		for _, host := range []string{"localhost", "127.0.0.1"} {
			sql += fmt.Sprintf("%s '%s'@'%s' IDENTIFIED BY '%s';\n", verb, user, host, pw)
			sql += fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%s';\n", db, user, host)
		}
		if _, err := c.rootQuery(ctx, sql); err != nil {
			return err
		}
		site.Database, site.DBUser, site.DBEngine = db, user, EngineMySQL
		if err := c.Store.UpdateSite(ctx, site); err != nil {
			return err
		}
		res, err = c.siteDatabase(ctx, site)
		return err
	})
	return res, err
}

// detachSiteUser undoes attachSiteUser.
func (c *Core) detachSiteUser(ctx context.Context, name, user string) {
	_, _ = c.rootQuery(ctx, fmt.Sprintf("DROP USER IF EXISTS '%s'@'localhost', '%s'@'127.0.0.1';", user, user))
	if kc, err := mysqlKeychain(c.launchctl.Prefix); err == nil {
		_, _ = kc.For(user).Delete(ctx)
	}
	if site, err := c.Store.Site(ctx, name); err == nil {
		site.Database, site.DBUser, site.DBEngine = "", "", ""
		_ = c.Store.UpdateSite(ctx, site)
	}
}

// backupDir is where a site's backups go, inside the Covo folder.
func (c *Core) backupDir(site string) string { return filepath.Join(c.Layout.Data, "backups", site) }

// backupFile copies path into the site's backup folder, with a timestamp.
func (c *Core) backupFile(site, path string) (string, error) {
	dst := filepath.Join(c.backupDir(site), filepath.Base(path)+"."+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", fmt.Errorf("create backup folder: %w", err)
	}
	if err := copyFile(path, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// copyFile copies src over dst, keeping src's permissions.
func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if _, err := fsutil.WriteAtomic(dst, b, info.Mode().Perm()); err != nil {
		return err
	}
	return nil
}

// backupDatabase dumps one of Covo's databases into the site's backup
// folder (gzip) and returns the file.
func (c *Core) backupDatabase(ctx context.Context, site, db string) (string, error) {
	client, port, pw, err := c.mysqlRoot(ctx)
	if err != nil {
		return "", err
	}
	path := filepath.Join(c.backupDir(site), db+"."+time.Now().Format("20060102-150405")+".sql.gz")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create backup folder: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", path, err)
	}
	gz := gzip.NewWriter(f)
	err = mysql.Dump(ctx, filepath.Join(filepath.Dir(client), "mysqldump"),
		mysql.Server{Host: "127.0.0.1", Port: port, User: mysql.User, Password: pw}, db, gz)
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("back up database %s: %w", db, err)
	}
	return path, nil
}

// restoreDatabaseBackup loads a backupDatabase file into db (recreated).
func (c *Core) restoreDatabaseBackup(ctx context.Context, db, path string) error {
	client, port, pw, err := c.mysqlRoot(ctx)
	if err != nil {
		return err
	}
	if _, err := c.rootQuery(ctx, "DROP DATABASE IF EXISTS `"+db+"`; CREATE DATABASE `"+db+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"); err != nil {
		return err
	}
	return c.loadIntoDatabase(ctx, projectDB{}, db, path, client, port, pw)
}

// pointConfigAt changes the project's configuration to Covo's database.
func (c *Core) pointConfigAt(p projectDB, db SiteDatabase, host, url string) error {
	if p.framework == FrameworkLaravel {
		hostOnly, port, _ := strings.Cut(host, ":")
		return editEnvFile(p.configPath, []dotenv.Pair{
			{Key: "APP_URL", Value: url}, {Key: "DB_CONNECTION", Value: "mysql"},
			{Key: "DB_HOST", Value: hostOnly}, {Key: "DB_PORT", Value: port},
			{Key: "DB_DATABASE", Value: db.Database}, {Key: "DB_USERNAME", Value: db.User}, {Key: "DB_PASSWORD", Value: db.Password},
		})
	}
	b, err := os.ReadFile(p.configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", p.configPath, err)
	}
	out, err := wpconfig.Set(string(b), map[string]string{
		"DB_NAME": db.Database, "DB_USER": db.User, "DB_PASSWORD": db.Password, "DB_HOST": host,
	})
	if err != nil {
		return err
	}
	return rewriteProjectFile(p.configPath, []byte(out))
}

// wpHome reads WordPress's home address from a database on Covo's server.
func (c *Core) wpHome(ctx context.Context, db, prefix string) (string, error) {
	if !mysql.ValidName(strings.TrimSuffix(prefix, "_"), 64) && prefix != "" {
		prefix = "wp_"
	}
	rows, err := c.rootQuery(ctx, "SELECT option_value FROM `"+db+"`.`"+prefix+"options` WHERE option_name = 'home';")
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return strings.TrimRight(strings.TrimSpace(rows[0]), "/"), nil
}

// replaceURL replaces the old address (and its other scheme) with the new
// one in every table of the site's prefix, serialized data included.
func (c *Core) replaceURL(ctx context.Context, site store.Site, from, to string) error {
	froms := []string{from}
	switch {
	case strings.HasPrefix(from, "http://"):
		froms = append(froms, "https://"+strings.TrimPrefix(from, "http://"))
	case strings.HasPrefix(from, "https://"):
		froms = append(froms, "http://"+strings.TrimPrefix(from, "https://"))
	}
	// Each variant goes to a placeholder first, then the placeholder to the
	// new address: the new address often starts with an old one
	// (http://site → https://site.localhost:8443), and replacing the
	// variants one after the other would apply the change twice.
	placeholder := "covo-url-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	pairs := make([][2]string, 0, len(froms)+1)
	for _, f := range froms {
		pairs = append(pairs, [2]string{f, placeholder})
	}
	pairs = append(pairs, [2]string{placeholder, to})
	for _, p := range pairs {
		r, err := c.RunToolCaptured(ctx, site.Name, ToolWP, []string{"search-replace", p[0], p[1], "--all-tables-with-prefix", "--skip-columns=guid", "--precise", "--quiet"})
		if err != nil {
			return err
		}
		if r.ExitCode != 0 {
			return errcode.Errorf(errcode.Service, "wp search-replace failed: "+lastChars(r.Output+r.Errors, 1000))
		}
	}
	return nil
}

// UndoMigration puts a migrated site back as it was before the migration:
// its configuration file from the backup, and the database Covo copied
// (with the site's own user) deleted. The site itself stays.
func (c *Core) UndoMigration(ctx context.Context, name string) (store.Migration, error) {
	var m store.Migration
	err := c.locked(ctx, func() error {
		var err error
		m, err = c.undoMigration(ctx, name)
		return err
	})
	return m, err
}

func (c *Core) undoMigration(ctx context.Context, name string) (store.Migration, error) {
	m, err := c.Store.Migration(ctx, name)
	if err != nil {
		return m, err
	}
	if m.BackupPath != "" && m.ConfigPath != "" {
		if err := copyFile(m.BackupPath, m.ConfigPath); err != nil {
			return m, fmt.Errorf("restore %s: %w", m.ConfigPath, err)
		}
	}
	if m.DBUser != "" {
		c.detachSiteUser(ctx, name, m.DBUser)
	}
	if m.CreatedDB {
		if _, err := c.rootQuery(ctx, "DROP DATABASE IF EXISTS `"+m.DBName+"`;"); err != nil {
			return m, err
		}
	}
	return m, c.Store.DeleteMigration(ctx, name)
}
