// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/mysql"
	"github.com/getcovo/covo/internal/store"
)

// systemDatabases are MySQL's own: never linked, created or dropped.
var systemDatabases = []string{"information_schema", "mysql", "performance_schema", "sys"}

// SiteDatabase is a site's database and how the site connects to it.
type SiteDatabase struct {
	Site     string `json:"site"`
	Database string `json:"database"`
	// User is the site's own MySQL user, or root for a linked database.
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Socket   string `json:"socket"`
	// Dedicated is true when Covo created User for this site only.
	Dedicated bool `json:"dedicated"`
	// Dropped is true when unlink also deleted the database.
	Dropped bool `json:"dropped,omitempty"`
}

// mysqlRoot returns how to run SQL as root. MySQL must be running.
func (c *Core) mysqlRoot(ctx context.Context) (client string, port int, password string, err error) {
	st, err := c.mysqlStatus(ctx)
	if err != nil {
		return "", 0, "", err
	}
	if !st.Enabled {
		return "", 0, "", errcode.Errorf(errcode.Unsupported, "MySQL is off: turn it on first (covo mysql enable)")
	}
	if !st.Running {
		return "", 0, "", errcode.Errorf(errcode.Conflict, "MySQL is not running: start Covo first (covo start)")
	}
	dir, _, err := c.ensureMySQL(ctx)
	if err != nil {
		return "", 0, "", err
	}
	kc, err := mysqlKeychain(c.launchctl.Prefix)
	if err != nil {
		return "", 0, "", err
	}
	if password, err = kc.Get(ctx); err != nil {
		return "", 0, "", err
	}
	return filepath.Join(dir, "bin", "mysql"), st.Port, password, nil
}

func (c *Core) rootQuery(ctx context.Context, sql string) ([]string, error) {
	client, port, pw, err := c.mysqlRoot(ctx)
	if err != nil {
		return nil, err
	}
	out, err := mysql.Query(ctx, client, port, pw, sql)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			rows = append(rows, l)
		}
	}
	return rows, nil
}

// Databases lists the user databases on Covo's MySQL.
func (c *Core) Databases(ctx context.Context) ([]string, error) {
	rows, err := c.rootQuery(ctx, "SHOW DATABASES;")
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range rows {
		if !slices.Contains(systemDatabases, r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// DefaultDatabaseName is the database Covo proposes for a site: its name
// with underscores, e.g. my-shop → my_shop.
func DefaultDatabaseName(site string) string {
	return strings.ReplaceAll(site, "-", "_")
}

func checkDatabaseName(db string) error {
	if !mysql.ValidName(db, 64) || slices.Contains(systemDatabases, db) {
		return errcode.Errorf(errcode.InvalidInput,
			fmt.Sprintf("database name %q must be up to 64 lowercase letters, digits and underscores, and not a MySQL system database", db))
	}
	return nil
}

// CreateSiteDatabase creates a database (default: the site's name) and a
// MySQL user of the same name (up to 32 characters) that may only use it,
// with a random password kept in the login keychain, and links it to the
// site.
func (c *Core) CreateSiteDatabase(ctx context.Context, name, db string) (SiteDatabase, error) {
	var res SiteDatabase
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		if site.Database != "" {
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("%s already uses database %s", name, site.Database))
		}
		if db == "" {
			db = DefaultDatabaseName(name)
		}
		if err := checkDatabaseName(db); err != nil {
			return err
		}
		user := db[:min(len(db), 32)]
		if user == mysql.User {
			return errcode.Errorf(errcode.InvalidInput, "the database cannot be called root")
		}
		dbs, err := c.Databases(ctx)
		if err != nil {
			return err
		}
		if slices.Contains(dbs, db) {
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("database %s already exists: link it instead", db))
		}
		users, err := c.rootQuery(ctx, "SELECT DISTINCT user FROM mysql.user WHERE user = '"+user+"';")
		if err != nil {
			return err
		}
		if len(users) > 0 {
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("MySQL user %s already exists", user))
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
		sql := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n", db)
		for _, host := range []string{"localhost", "127.0.0.1"} {
			sql += fmt.Sprintf("CREATE USER '%s'@'%s' IDENTIFIED BY '%s';\n", user, host, pw)
			sql += fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%s';\n", db, user, host)
		}
		undo := func() {
			_, _ = c.rootQuery(ctx, dropSQL(db, user))
			_, _ = kc.Delete(ctx)
		}
		if _, err := c.rootQuery(ctx, sql); err != nil {
			undo()
			return err
		}
		site.Database, site.DBUser = db, user
		if err := c.Store.UpdateSite(ctx, site); err != nil {
			undo()
			return err
		}
		res, err = c.siteDatabase(ctx, site)
		return err
	})
	return res, err
}

func dropSQL(db, user string) string {
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;\n", db)
	if user != "" {
		sql += fmt.Sprintf("DROP USER IF EXISTS '%s'@'localhost', '%s'@'127.0.0.1';\n", user, user)
	}
	return sql
}

// LinkSiteDatabase links an existing database to a site. The site connects
// as root (as with MAMP).
func (c *Core) LinkSiteDatabase(ctx context.Context, name, db string) (SiteDatabase, error) {
	var res SiteDatabase
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		if site.Database != "" {
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("%s already uses database %s", name, site.Database))
		}
		if err := checkDatabaseName(db); err != nil {
			return err
		}
		dbs, err := c.Databases(ctx)
		if err != nil {
			return err
		}
		if !slices.Contains(dbs, db) {
			return errcode.Errorf(errcode.NotFound, fmt.Sprintf("database %s does not exist", db))
		}
		site.Database, site.DBUser = db, ""
		if err := c.Store.UpdateSite(ctx, site); err != nil {
			return err
		}
		res, err = c.siteDatabase(ctx, site)
		return err
	})
	return res, err
}

// UnlinkSiteDatabase forgets a site's database. With drop, the database
// and the site's own user are deleted as well; without it nothing in MySQL
// changes.
func (c *Core) UnlinkSiteDatabase(ctx context.Context, name string, drop bool) (SiteDatabase, error) {
	var res SiteDatabase
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		if site.Database == "" {
			return errcode.Errorf(errcode.NotFound, fmt.Sprintf("%s has no database", name))
		}
		res = SiteDatabase{Site: name, Database: site.Database, User: site.DBUser, Dedicated: site.DBUser != ""}
		if drop {
			if _, err := c.rootQuery(ctx, dropSQL(site.Database, site.DBUser)); err != nil {
				return err
			}
			if site.DBUser != "" {
				kc, err := mysqlKeychain(c.launchctl.Prefix)
				if err != nil {
					return err
				}
				if _, err := kc.For(site.DBUser).Delete(ctx); err != nil {
					return err
				}
			}
			res.Dropped = true
		}
		site.Database, site.DBUser = "", ""
		return c.Store.UpdateSite(ctx, site)
	})
	return res, err
}

// GetSiteDatabase returns a site's database with its credentials.
func (c *Core) GetSiteDatabase(ctx context.Context, name string) (SiteDatabase, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return SiteDatabase{}, err
	}
	if site.Database == "" {
		return SiteDatabase{}, errcode.Errorf(errcode.NotFound, fmt.Sprintf("%s has no database", name))
	}
	return c.siteDatabase(ctx, site)
}

func (c *Core) siteDatabase(ctx context.Context, site store.Site) (SiteDatabase, error) {
	st, err := c.mysqlStatus(ctx)
	if err != nil {
		return SiteDatabase{}, err
	}
	res := SiteDatabase{
		Site: site.Name, Database: site.Database, User: mysql.User,
		Host: st.Host, Port: st.Port, Socket: c.mysqlSocketPath(),
	}
	kc, err := mysqlKeychain(c.launchctl.Prefix)
	if err != nil {
		return res, err
	}
	if site.DBUser != "" {
		res.User, res.Dedicated = site.DBUser, true
		kc = kc.For(site.DBUser)
	}
	if res.Password, err = kc.Get(ctx); err != nil {
		return res, err
	}
	return res, nil
}
