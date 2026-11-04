// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/dotenv"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/keychain"
	"github.com/getcovo/covo/internal/mysql"
	"github.com/getcovo/covo/internal/remote"
	"github.com/getcovo/covo/internal/store"
	"github.com/getcovo/covo/internal/wpconfig"
)

// Sync with a server: pull a site's database and files from production or
// staging, push them to staging only. A snapshot is taken first. Over SSH
// the database goes through the server's wp-cli or mysqldump and files
// through rsync; over SFTP or FTP a temporary PHP bridge reads the database
// (see remote.Bridge).

// Remote kinds.
const (
	RemoteProduction = "production"
	RemoteStaging    = "staging"
)

// Env for `covo askpass`: the keychain item ssh asks for.
const (
	EnvAskPassService = "COVO_ASKPASS_SERVICE"
	EnvAskPassAccount = "COVO_ASKPASS_ACCOUNT"
)

// remoteHostRe never starts with "-": ssh would read it as an option.
var remoteHostRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

func (c *Core) remoteKeychainService() string { return c.launchctl.Prefix + ".remote" }

func remoteAccount(site, name string) string { return site + "/" + name }

// remoteSecret is where a remote's password lives: the login keychain.
// Unlike MySQL passwords it is the user's, so any character is allowed.
func (c *Core) remoteSecret(site, name string) (keychain.Item, error) {
	it, err := keychain.Login(c.remoteKeychainService(), remoteAccount(site, name))
	it.Label = "Covo remote " + remoteAccount(site, name)
	return it, err
}

// defaultSyncPaths are what pull and push copy when the remote says nothing:
// the uploads, never the code (which belongs in git).
func defaultSyncPaths(framework string) []string {
	switch framework {
	case FrameworkWordPress:
		return []string{"wp-content/uploads"}
	case FrameworkLaravel:
		return []string{"storage/app/public"}
	}
	return []string{"."}
}

// SetRemote adds or changes a remote of a site. A non-nil password is
// stored in the login keychain ("" deletes it).
func (c *Core) SetRemote(ctx context.Context, r store.Remote, password *string) (store.Remote, error) {
	site, err := c.Store.Site(ctx, r.Site)
	if err != nil {
		return r, err
	}
	if r.Name == "" {
		r.Name = r.Kind
	}
	switch {
	case r.Kind != RemoteProduction && r.Kind != RemoteStaging:
		return r, errcode.Errorf(errcode.InvalidInput, "kind must be production or staging")
	case !slices.Contains([]string{remote.SSH, remote.SFTP, remote.FTP, remote.FTPS}, r.Transport):
		return r, errcode.Errorf(errcode.InvalidInput, "transport must be ssh, sftp, ftp or ftps")
	case !remoteHostRe.MatchString(r.Host):
		return r, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("bad host %q", r.Host))
	case r.User == "" || strings.ContainsAny(r.User, " @:/\n\x00") || strings.HasPrefix(r.User, "-"):
		return r, errcode.Errorf(errcode.InvalidInput, "a user is needed (no spaces, @, : or /, not starting with -)")
	case r.Path == "" || strings.ContainsAny(r.Path, "\n\x00"):
		return r, errcode.Errorf(errcode.InvalidInput, "the remote project folder is needed")
	case !strings.HasPrefix(r.URL, "https://") && !strings.HasPrefix(r.URL, "http://"):
		return r, errcode.Errorf(errcode.InvalidInput, "the remote site's address must start with https:// or http://")
	case r.Port < 0 || r.Port > 65535:
		return r, errcode.Errorf(errcode.InvalidInput, "bad port")
	}
	r.URL = strings.TrimRight(r.URL, "/")
	if r.Webroot == "" && site.Framework == FrameworkLaravel {
		r.Webroot = "public"
	}
	if len(r.Paths) == 0 {
		r.Paths = defaultSyncPaths(site.Framework)
	}
	for _, p := range append(slices.Clone(r.Paths), r.Webroot) {
		if strings.HasPrefix(p, "/") || slices.Contains(strings.Split(p, "/"), "..") {
			return r, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("%q must be relative to the project folder", p))
		}
	}
	if r.Excludes == nil {
		r.Excludes = []string{".git", "node_modules", ".DS_Store", "cache"}
	}
	if password != nil {
		kc, err := c.remoteSecret(r.Site, r.Name)
		if err != nil {
			return r, err
		}
		if *password == "" {
			_, err = kc.Delete(ctx)
		} else {
			err = kc.Set(ctx, *password)
		}
		if err != nil {
			return r, err
		}
	}
	return r, c.Store.PutRemote(ctx, r)
}

// ListRemotes returns a site's remotes.
func (c *Core) ListRemotes(ctx context.Context, site string) ([]store.Remote, error) {
	if _, err := c.Store.Site(ctx, site); err != nil {
		return nil, err
	}
	return c.Store.ListRemotes(ctx, site)
}

// RemoveRemote forgets a remote and its password.
func (c *Core) RemoveRemote(ctx context.Context, site, name string) error {
	if _, err := c.Store.Remote(ctx, site, name); err != nil {
		return err
	}
	if kc, err := c.remoteSecret(site, name); err == nil {
		_, _ = kc.Delete(ctx)
	}
	return c.Store.DeleteRemote(ctx, site, name)
}

// AskPass prints the password of the keychain item named by the
// environment (ssh runs `covo askpass` through SSH_ASKPASS).
func (c *Core) AskPass(ctx context.Context) (string, error) {
	service, account := os.Getenv(EnvAskPassService), os.Getenv(EnvAskPassAccount)
	if service != c.remoteKeychainService() || account == "" {
		return "", errcode.Errorf(errcode.InvalidInput, "askpass only answers for Covo's remotes")
	}
	kc, err := keychain.Login(service, account)
	if err != nil {
		return "", err
	}
	return kc.Get(ctx)
}

// server returns how to log in to a remote, with its stored password.
func (c *Core) server(ctx context.Context, r store.Remote) (remote.Server, error) {
	s := remote.Server{Transport: r.Transport, Host: r.Host, Port: r.Port, User: r.User}
	kc, err := c.remoteSecret(r.Site, r.Name)
	if err != nil {
		return s, err
	}
	if pw, err := kc.Get(ctx); err == nil {
		s.Password = pw
	} else if !errors.Is(err, keychain.ErrNotFound) {
		return s, err
	}
	if s.Password != "" && (r.Transport == remote.SSH || r.Transport == remote.SFTP) {
		exe, err := os.Executable()
		if err != nil {
			return s, fmt.Errorf("locate covo: %w", err)
		}
		s.AskPass = exe
		s.AskPassEnv = []string{EnvAskPassService + "=" + kc.Service, EnvAskPassAccount + "=" + kc.Account}
	}
	if (r.Transport == remote.FTP || r.Transport == remote.FTPS) && s.Password == "" {
		return s, errcode.Errorf(errcode.InvalidInput, "FTP needs the password: set it with covo remote set … --password")
	}
	return s, nil
}

// SyncOptions choose what a pull or push moves.
type SyncOptions struct {
	DryRun bool
	// NoFiles and NoDatabase leave one of them out.
	NoFiles, NoDatabase bool
}

// SyncResult reports a pull or push.
type SyncResult struct {
	Site   string `json:"site"`
	Remote string `json:"remote"`
	DryRun bool   `json:"dry_run"`
	// Files changed (or that would change), relative to the project.
	Files []string `json:"files"`
	// Database is the local database replaced (pull) or the remote one
	// (push), when the database was synced.
	Database string `json:"database,omitempty"`
	URLFrom  string `json:"url_from,omitempty"`
	URLTo    string `json:"url_to,omitempty"`
	// Snapshot is taken before a pull (to undo it) or a push.
	Snapshot string `json:"snapshot,omitempty"`
	// Steps say what happened (or would), in words.
	Steps []string `json:"steps"`
}

// Pull copies a remote's database and files into the site. A snapshot is
// taken first; WordPress addresses are replaced with the local one.
func (c *Core) Pull(ctx context.Context, name, remoteName string, opts SyncOptions) (SyncResult, error) {
	var res SyncResult
	err := c.locked(ctx, func() error {
		var err error
		res, err = c.pull(ctx, name, remoteName, opts)
		return err
	})
	return res, err
}

func (c *Core) pull(ctx context.Context, name, remoteName string, opts SyncOptions) (SyncResult, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return SyncResult{}, err
	}
	r, err := c.Store.Remote(ctx, name, remoteName)
	if err != nil {
		return SyncResult{}, err
	}
	srv, err := c.server(ctx, r)
	if err != nil {
		return SyncResult{}, err
	}
	res := SyncResult{Site: name, Remote: r.Name, DryRun: opts.DryRun, Files: []string{}, Steps: []string{}}
	localURL := siteURL(site, c.publicHTTPS(ctx))
	if !opts.DryRun {
		snap, err := c.createSnapshot(ctx, name, SnapshotPrePull, "before pulling from "+r.Name, "")
		if err != nil {
			return res, err
		}
		res.Snapshot = snap.ID
		res.Steps = append(res.Steps, "snapshot "+snap.ID+" taken (restore it to undo the pull)")
	}
	if !opts.NoFiles {
		changed, err := c.pullFiles(ctx, srv, r, projectRoot(site), opts.DryRun)
		if err != nil {
			return res, err
		}
		res.Files = changed
		res.Steps = append(res.Steps, fmt.Sprintf("%d files from %s", len(changed), strings.Join(r.Paths, ", ")))
	}
	if !opts.NoDatabase && (site.Framework == FrameworkWordPress || site.Framework == FrameworkLaravel) {
		if engineOf(site) == EnginePostgres {
			return res, errcode.Errorf(errcode.Unsupported, "pulling PostgreSQL databases is not supported yet")
		}
		if site.Database == "" && !opts.DryRun {
			if _, err := c.createMySQLSiteDatabase(ctx, name, ""); err != nil {
				return res, err
			}
			if site, err = c.Store.Site(ctx, name); err != nil {
				return res, err
			}
		}
		res.Database = site.Database
		if opts.DryRun {
			res.Steps = append(res.Steps, "replace the local database with the remote one")
		} else {
			if err := c.pullDatabase(ctx, srv, r, site); err != nil {
				return res, err
			}
			res.Steps = append(res.Steps, "database "+site.Database+" replaced with the remote one")
		}
		if site.Framework == FrameworkWordPress && r.URL != localURL {
			res.URLFrom, res.URLTo = r.URL, localURL
			if !opts.DryRun {
				if home, err := c.wpHome(ctx, site.Database, c.tablePrefix(site)); err == nil && home != "" {
					res.URLFrom = home
				}
				if err := c.replaceURL(ctx, site, res.URLFrom, localURL); err != nil {
					return res, err
				}
			}
			res.Steps = append(res.Steps, "address "+res.URLFrom+" → "+localURL)
		}
	}
	return res, nil
}

// tablePrefix reads WordPress's table prefix from the site's wp-config.php.
func (c *Core) tablePrefix(site store.Site) string {
	for _, dir := range []string{site.Path, filepath.Dir(site.Path)} {
		if b, err := os.ReadFile(filepath.Join(dir, "wp-config.php")); err == nil {
			return wpconfig.Read(string(b)).TablePrefix
		}
	}
	return "wp_"
}

func (c *Core) pullFiles(ctx context.Context, srv remote.Server, r store.Remote, root string, dryRun bool) ([]string, error) {
	var all []string
	skip := remote.Skipper(r.Excludes)
	for _, p := range r.Paths {
		local := filepath.Join(root, filepath.FromSlash(p))
		remoteDir := path.Join(r.Path, p)
		if !dryRun {
			if err := os.MkdirAll(local, 0o755); err != nil {
				return all, fmt.Errorf("create %s: %w", local, err)
			}
		}
		if srv.Transport == remote.SSH {
			changed, err := srv.Rsync(ctx, srv.User+"@"+srv.Host+":"+remoteDir+"/", local+"/", r.Excludes, dryRun)
			if err != nil {
				return all, err
			}
			for _, f := range changed {
				all = append(all, path.Join(p, f))
			}
			continue
		}
		fs, err := srv.Open(ctx)
		if err != nil {
			return all, err
		}
		changed, err := mirrorDown(ctx, fs, remoteDir, local, skip, dryRun)
		_ = fs.Close()
		for _, f := range changed {
			all = append(all, path.Join(p, f))
		}
		if err != nil {
			return all, err
		}
	}
	slices.Sort(all)
	return all, nil
}

// mirrorDown downloads the files of remoteDir that local lacks or has
// older, keeping the remote times.
func mirrorDown(ctx context.Context, fs remote.FS, remoteDir, local string, skip func(string) bool, dryRun bool) ([]string, error) {
	rem, err := fs.Walk(ctx, remoteDir, skip)
	if err != nil {
		return nil, err
	}
	loc, err := remote.LocalFiles(local, skip)
	if err != nil {
		return nil, err
	}
	changed := remote.Changed(rem, loc)
	slices.Sort(changed)
	if dryRun {
		return changed, nil
	}
	for _, rel := range changed {
		dst := filepath.Join(local, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return changed, fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
		}
		tmp := dst + ".covo-part"
		f, err := os.Create(tmp)
		if err != nil {
			return changed, fmt.Errorf("create %s: %w", tmp, err)
		}
		err = fs.Get(ctx, path.Join(remoteDir, rel), f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(tmp, dst)
		}
		if err != nil {
			_ = os.Remove(tmp)
			return changed, fmt.Errorf("download %s: %w", rel, err)
		}
		_ = os.Chtimes(dst, rem[rel].ModTime, rem[rel].ModTime)
	}
	return changed, nil
}

// remoteDB reads the database settings of the remote project over SSH.
func remoteDBSettings(ctx context.Context, srv remote.Server, r store.Remote) (mysql.Server, string, error) {
	var out bytes.Buffer
	for _, f := range []string{"wp-config.php", "../wp-config.php", ".env"} {
		out.Reset()
		cmd := "cat " + remote.Quote(path.Join(r.Path, f)) + " 2>/dev/null"
		if err := srv.Run(ctx, cmd, nil, &out); err != nil || out.Len() == 0 {
			continue
		}
		var s mysql.Server
		var db string
		if strings.HasSuffix(f, ".env") {
			env := out.String()
			s.Host, _ = dotenv.Get(env, "DB_HOST")
			port, _ := dotenv.Get(env, "DB_PORT")
			if port != "" {
				s.Host += ":" + port
			}
			s.User, _ = dotenv.Get(env, "DB_USERNAME")
			s.Password, _ = dotenv.Get(env, "DB_PASSWORD")
			db, _ = dotenv.Get(env, "DB_DATABASE")
		} else {
			cfg := wpconfig.Read(out.String())
			s.Host, s.User, s.Password, db = cfg.Values["DB_HOST"], cfg.Values["DB_USER"], cfg.Values["DB_PASSWORD"], cfg.Values["DB_NAME"]
		}
		return s, db, nil
	}
	return mysql.Server{}, "", errcode.Errorf(errcode.NotFound, "no wp-config.php or .env in "+r.Path+" on "+r.Host)
}

// remoteClientOptions is a [client] option file for mysql/mysqldump on the
// server, sent on their stdin.
func remoteClientOptions(s mysql.Server) string {
	host, port := s.Host, ""
	if h, p, ok := strings.Cut(s.Host, ":"); ok {
		host, port = h, p
	}
	var b strings.Builder
	q := func(v string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"` }
	fmt.Fprintf(&b, "[client]\nuser=%s\npassword=%s\n", q(s.User), q(s.Password))
	switch {
	case strings.HasPrefix(port, "/"):
		fmt.Fprintf(&b, "socket=%s\n", q(port))
		if host != "" {
			fmt.Fprintf(&b, "host=%s\n", q(host))
		}
	case port != "":
		fmt.Fprintf(&b, "host=%s\nport=%s\n", q(host), port)
	case host != "":
		fmt.Fprintf(&b, "host=%s\n", q(host))
	}
	return b.String()
}

// pullDatabase replaces the site's database with the remote one.
func (c *Core) pullDatabase(ctx context.Context, srv remote.Server, r store.Remote, site store.Site) error {
	client, port, rootPW, err := c.mysqlRoot(ctx)
	if err != nil {
		return err
	}
	if _, err := c.rootQuery(ctx, "DROP DATABASE IF EXISTS `"+site.Database+"`; CREATE DATABASE `"+site.Database+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"); err != nil {
		return err
	}
	pr, pw := io.Pipe()
	dumpErr := make(chan error, 1)
	go func() {
		var err error
		if srv.Transport == remote.SSH {
			err = sshDump(ctx, srv, r, pw)
		} else {
			err = c.bridgeDump(ctx, srv, r, pw)
		}
		_ = pw.CloseWithError(err)
		dumpErr <- err
	}()
	cr, cw := io.Pipe()
	go func() {
		err := mysql.Clean(pr, cw)
		_ = cw.CloseWithError(err)
		_ = pr.CloseWithError(err)
	}()
	restoreErr := mysql.Restore(ctx, client, port, rootPW, site.Database, cr)
	_ = cr.CloseWithError(errors.New("import ended"))
	// A dump that stopped half way must not count as a pull: the snapshot
	// taken before restores the site.
	if err := <-dumpErr; err != nil {
		return fmt.Errorf("%w (restore snapshot to undo)", err)
	}
	return restoreErr
}

// sshDump writes the remote database's SQL to w: with the server's wp-cli
// for WordPress when it has one, else mysqldump with the project's
// credentials (sent on stdin, never on the command line).
func sshDump(ctx context.Context, srv remote.Server, r store.Remote, w io.Writer) error {
	var probe bytes.Buffer
	_ = srv.Run(ctx, "cd "+remote.Quote(r.Path)+" && test -f wp-config.php -o -f ../wp-config.php && command -v wp", nil, &probe)
	if strings.TrimSpace(probe.String()) != "" {
		return srv.Run(ctx, "cd "+remote.Quote(r.Path)+" && wp db export - --single-transaction --quiet", nil, w)
	}
	creds, db, err := remoteDBSettings(ctx, srv, r)
	if err != nil {
		return err
	}
	cmd := "mysqldump --defaults-extra-file=/dev/stdin --single-transaction --quick --routines --triggers --hex-blob --no-tablespaces " + remote.Quote(db)
	return srv.Run(ctx, cmd, strings.NewReader(remoteClientOptions(creds)), w)
}

// withBridge uploads a bridge next to the remote site, runs fn with it and
// always removes it.
func (c *Core) withBridge(ctx context.Context, srv remote.Server, r store.Remote, fn func(remote.Bridge) error) error {
	b, src, err := remote.NewBridge(r.URL)
	if err != nil {
		return err
	}
	fs, err := srv.Open(ctx)
	if err != nil {
		return err
	}
	defer fs.Close()
	where := path.Join(r.Path, r.Webroot, b.File)
	if err := fs.Put(ctx, where, strings.NewReader(src)); err != nil {
		return err
	}
	defer func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		b.Bye(bg)
		_ = fs.Remove(bg, where)
	}()
	return fn(b)
}

func (c *Core) bridgeDump(ctx context.Context, srv remote.Server, r store.Remote, w io.Writer) error {
	return c.withBridge(ctx, srv, r, func(b remote.Bridge) error {
		gr, gw := io.Pipe()
		errc := make(chan error, 1)
		go func() {
			err := b.Dump(ctx, gw)
			_ = gw.CloseWithError(err)
			errc <- err
		}()
		zr, err := gzip.NewReader(gr)
		if err != nil {
			_ = gr.CloseWithError(err)
			if derr := <-errc; derr != nil {
				return derr
			}
			return errcode.New(errcode.Service, fmt.Errorf("read the database from the bridge: %w", err))
		}
		if _, err := io.Copy(w, zr); err != nil {
			return errcode.New(errcode.Service, fmt.Errorf("read the database from the bridge: %w", err))
		}
		return <-errc
	})
}

// Push copies the site's files and database to a staging remote, never to
// production. A snapshot of the site is taken first.
func (c *Core) Push(ctx context.Context, name, remoteName string, opts SyncOptions) (SyncResult, error) {
	var res SyncResult
	err := c.locked(ctx, func() error {
		var err error
		res, err = c.push(ctx, name, remoteName, opts)
		return err
	})
	return res, err
}

func (c *Core) push(ctx context.Context, name, remoteName string, opts SyncOptions) (SyncResult, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return SyncResult{}, err
	}
	r, err := c.Store.Remote(ctx, name, remoteName)
	if err != nil {
		return SyncResult{}, err
	}
	if r.Kind != RemoteStaging {
		return SyncResult{}, errcode.Errorf(errcode.Unsupported, fmt.Sprintf("%s is %s: Covo only pushes to staging", r.Name, r.Kind))
	}
	srv, err := c.server(ctx, r)
	if err != nil {
		return SyncResult{}, err
	}
	res := SyncResult{Site: name, Remote: r.Name, DryRun: opts.DryRun, Files: []string{}, Steps: []string{}}
	localURL := siteURL(site, c.publicHTTPS(ctx))
	if !opts.DryRun {
		snap, err := c.createSnapshot(ctx, name, SnapshotManual, "before pushing to "+r.Name, "")
		if err != nil {
			return res, err
		}
		res.Snapshot = snap.ID
	}
	if !opts.NoFiles {
		changed, err := c.pushFiles(ctx, srv, r, projectRoot(site), opts.DryRun)
		if err != nil {
			return res, err
		}
		res.Files = changed
		res.Steps = append(res.Steps, fmt.Sprintf("%d files to %s", len(changed), strings.Join(r.Paths, ", ")))
	}
	if !opts.NoDatabase && site.Database != "" && (site.Framework == FrameworkWordPress || site.Framework == FrameworkLaravel) {
		if engineOf(site) == EnginePostgres {
			return res, errcode.Errorf(errcode.Unsupported, "pushing PostgreSQL databases is not supported yet")
		}
		res.Database = "the remote site's"
		res.URLFrom, res.URLTo = localURL, r.URL
		res.Steps = append(res.Steps, "replace the remote database with the local one (addresses "+localURL+" → "+r.URL+")")
		if !opts.DryRun {
			if err := c.pushDatabase(ctx, srv, r, site, localURL); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

func (c *Core) pushFiles(ctx context.Context, srv remote.Server, r store.Remote, root string, dryRun bool) ([]string, error) {
	var all []string
	skip := remote.Skipper(r.Excludes)
	for _, p := range r.Paths {
		local := filepath.Join(root, filepath.FromSlash(p))
		remoteDir := path.Join(r.Path, p)
		if srv.Transport == remote.SSH {
			changed, err := srv.Rsync(ctx, local+"/", srv.User+"@"+srv.Host+":"+remoteDir+"/", r.Excludes, dryRun)
			if err != nil {
				return all, err
			}
			for _, f := range changed {
				all = append(all, path.Join(p, f))
			}
			continue
		}
		fs, err := srv.Open(ctx)
		if err != nil {
			return all, err
		}
		loc, err := remote.LocalFiles(local, skip)
		if err != nil {
			_ = fs.Close()
			return all, err
		}
		rem, err := fs.Walk(ctx, remoteDir, skip)
		if err != nil {
			rem = remote.Files{}
		}
		changed := remote.Changed(loc, rem)
		slices.Sort(changed)
		for _, rel := range changed {
			all = append(all, path.Join(p, rel))
			if dryRun {
				continue
			}
			if err := fs.MkdirAll(ctx, path.Dir(path.Join(remoteDir, rel))); err != nil {
				_ = fs.Close()
				return all, err
			}
			f, err := os.Open(filepath.Join(local, filepath.FromSlash(rel)))
			if err != nil {
				_ = fs.Close()
				return all, fmt.Errorf("open %s: %w", rel, err)
			}
			err = fs.Put(ctx, path.Join(remoteDir, rel), f)
			_ = f.Close()
			if err != nil {
				_ = fs.Close()
				return all, err
			}
		}
		_ = fs.Close()
	}
	slices.Sort(all)
	return all, nil
}

// pushDatabase sends the local database to the remote, with the local
// address replaced by the remote one (WordPress: wp search-replace
// --export, which leaves the local database unchanged).
func (c *Core) pushDatabase(ctx context.Context, srv remote.Server, r store.Remote, site store.Site, localURL string) error {
	sqlFile := filepath.Join(c.Layout.Tmp, "push-"+site.Name+"-"+time.Now().Format("150405")+".sql")
	defer os.Remove(sqlFile)
	if site.Framework == FrameworkWordPress {
		res, err := c.RunToolCaptured(ctx, site.Name, ToolWP, []string{"search-replace", localURL, r.URL,
			"--all-tables-with-prefix", "--skip-columns=guid", "--precise", "--export=" + sqlFile})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return errcode.Errorf(errcode.Service, "wp search-replace --export failed: "+lastChars(res.Output+res.Errors, 1000))
		}
	} else {
		client, port, pw, err := c.mysqlRoot(ctx)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(sqlFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("create %s: %w", sqlFile, err)
		}
		err = mysql.Dump(ctx, filepath.Join(filepath.Dir(client), "mysqldump"), mysql.Server{Host: "127.0.0.1", Port: port, User: mysql.User, Password: pw}, site.Database, f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	raw, err := os.Open(sqlFile)
	if err != nil {
		return fmt.Errorf("open %s: %w", sqlFile, err)
	}
	defer raw.Close()
	// The dump without CREATE DATABASE/USE/DEFINER: it goes into whatever
	// database the remote site uses.
	// Like mysqldump's own header: WordPress tables have zero-date defaults
	// that a strict SQL mode refuses.
	header := strings.NewReader("SET SESSION sql_mode='NO_AUTO_VALUE_ON_ZERO';\nSET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS=0;\n")
	pr, pw := io.Pipe()
	go func() { _ = pw.CloseWithError(mysql.Clean(io.MultiReader(header, raw), pw)) }()
	if srv.Transport == remote.SSH {
		var probe bytes.Buffer
		_ = srv.Run(ctx, "cd "+remote.Quote(r.Path)+" && test -f wp-config.php -o -f ../wp-config.php && command -v wp", nil, &probe)
		if strings.TrimSpace(probe.String()) != "" {
			return srv.Run(ctx, "cd "+remote.Quote(r.Path)+" && wp db import - --quiet", pr, io.Discard)
		}
		creds, db, err := remoteDBSettings(ctx, srv, r)
		if err != nil {
			return err
		}
		// Credentials go through a private file that is removed at once:
		// stdin carries the SQL.
		cnf := "$HOME/.covo-push-" + time.Now().Format("150405") + ".cnf"
		if err := srv.Run(ctx, "umask 077 && cat > "+cnf, strings.NewReader(remoteClientOptions(creds)), io.Discard); err != nil {
			return err
		}
		return srv.Run(ctx, "mysql --defaults-extra-file="+cnf+" "+remote.Quote(db)+"; rc=$?; rm -f "+cnf+"; exit $rc", pr, io.Discard)
	}
	gzPath := sqlFile + ".gz"
	defer os.Remove(gzPath)
	gf, err := os.OpenFile(gzPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", gzPath, err)
	}
	zw := gzip.NewWriter(gf)
	_, err = io.Copy(zw, pr)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := gf.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("compress the database: %w", err)
	}
	return c.withBridge(ctx, srv, r, func(b remote.Bridge) error {
		f, err := os.Open(gzPath)
		if err != nil {
			return fmt.Errorf("open %s: %w", gzPath, err)
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return fmt.Errorf("stat %s: %w", gzPath, err)
		}
		return b.Import(ctx, f, fi.Size())
	})
}
