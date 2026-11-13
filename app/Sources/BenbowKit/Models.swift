// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import Foundation

// Mirrors of the core's JSON (snake_case keys are converted to camelCase).
// Optional fields are the ones the core omits when empty; unknown fields are
// ignored, so a newer core does not break an older app.

public struct Site: Codable, Sendable, Identifiable, Hashable {
    public var name: String
    public var path: String
    public var php: String
    public var domain: String
    public var https: Bool
    /// False for a site kept in the list but not served.
    public var enabled: Bool
    /// Put away: not served, listed apart.
    public var archived: Bool
    /// Extra domains served by the same site.
    public var aliases: [String]
    /// The linked MySQL database, or "".
    public var database: String
    /// The site's own MySQL user, when Benbow created the database.
    public var databaseUser: String?
    /// The group the site is listed in; nil at the top level.
    public var groupId: Int64?
    /// Order among the groups and sites of its level (nil/0: by name).
    public var position: Int?
    /// A free label shown instead of the name.
    public var title: String?

    /// The title, or the name when there is none.
    public var displayName: String { (title?.isEmpty == false ? title : nil) ?? name }
    /// "wordpress", "laravel" or nil.
    public var framework: String?
    /// The project folder when it differs from the document root (Laravel).
    public var appRoot: String?
    /// "mysql" (or nil) or "pgsql".
    public var databaseEngine: String?
    /// Set when Benbow migrated the site from another tool.
    public var migration: SiteMigration?
    /// "php" (or nil, older cores) or "app": a dev server behind the proxy.
    public var kind: String?
    /// Apps: "run" (Benbow starts `command`) or "proxy" (the user runs it).
    public var appMode: String?
    public var command: String?
    public var appPort: Int?
    /// Apps: "localhost" when the app receives Host: localhost:<port>.
    public var appHost: String?
    /// A command started and stopped with a PHP site (e.g. Vite).
    public var companionCommand: String?
    public var companionPort: Int?
    /// Apps: the dev server's state.
    public var app: AppStatus?
    /// PHP sites with a companion: its state.
    public var companion: AppStatus?

    public var isApp: Bool { kind == "app" }

    /// Where the project's files are: the Laravel root, else the folder.
    public var projectRoot: String { appRoot ?? path }
    public var url: String
    public var id: String { name }
}

/// A group of the site list. Groups nest; they never change how a site is
/// served.
public struct SiteGroup: Codable, Sendable, Identifiable, Hashable {
    public var id: Int64
    public var name: String
    /// The enclosing group; nil at the top level.
    public var parentId: Int64?
    /// Full path, e.g. "Clients/Acme".
    public var path: String
    /// Order among the groups and sites of its level (nil/0: by name).
    public var position: Int?
}

public struct Service: Codable, Sendable, Hashable {
    public var name: String
    public var label: String
    public var running: Bool
    public var pid: Int?
}

public struct CAStatus: Codable, Sendable, Hashable {
    public var certPath: String
    public var exists: Bool
    public var trusted: Bool
}

public struct HelperStatus: Codable, Sendable, Hashable {
    public var installed: Bool
    public var running: Bool
    public var version: String?
    public var names: [String]?
}

public struct ResolverState: Codable, Sendable, Hashable {
    public var path: String
    public var installed: Bool
    public var foreign: Bool?
}

public struct Domain: Codable, Sendable, Hashable, Identifiable {
    public var zone: String
    public var enabled: Bool
    public var dnsPort: Int?
    public var resolver: ResolverState?
    public var helper: HelperStatus?
    public var id: String { zone }
}

public struct MySQLStatus: Codable, Sendable, Hashable {
    public var enabled: Bool
    public var socket: String?
    public var version: String?
    public var host: String?
    public var port: Int?
    public var running: Bool
}

public struct MySQLCredentials: Codable, Sendable, Hashable {
    public var host: String
    public var port: Int
    public var socket: String
    public var user: String
    public var password: String
}

/// A newer version of a component.
public struct ComponentUpdate: Codable, Sendable, Hashable, Identifiable {
    public var component: String
    public var current: String
    public var available: String
    public var blocked: String?
    public var id: String { component }

    /// "PHP 8.4", "MySQL", …
    public var title: String {
        switch component {
        case "caddy": return "Caddy (web server)"
        case "mysql": return "MySQL"
        case "mailpit": return "Mailpit"
        case "adminer": return "Adminer"
        case "wpcli": return "wp-cli"
        default:
            if component.hasPrefix("php-") { return "PHP " + component.dropFirst(4) }
            return component
        }
    }
}

public struct UpdatesStatus: Codable, Sendable, Hashable {
    public var checkedAt: Date?
    /// The daemon checks once a day by itself (nil from an older core: on).
    public var auto: Bool?
    public var serial: Int
    public var error: String?
    public var updates: [ComponentUpdate]

    /// Updates that can be installed now.
    public var ready: [ComponentUpdate] { updates.filter { $0.blocked == nil } }
}

/// Mailpit, which catches the mail PHP sends.
public struct MailStatus: Codable, Sendable, Hashable {
    public var enabled: Bool
    public var version: String?
    public var running: Bool
    public var smtpPort: Int?
    public var uiPort: Int?
    public var url: String?
}

/// Benbow's web ports. 443 and 80 are forwarded by Benbow's helper to the
/// high ports Caddy listens on.
public struct PortsStatus: Codable, Sendable, Hashable {
    public struct Forward: Codable, Sendable, Hashable {
        public var from: Int
        public var to: Int
    }

    public var httpsPort: Int
    public var httpsListenPort: Int
    /// nil: no HTTP redirect.
    public var httpPort: Int?
    public var httpListenPort: Int?
    public var forwarded: Bool
    public var active: [Forward]
    public var error: String?
}

public struct Status: Codable, Sendable, Hashable {
    public var running: Bool
    public var httpsPort: Int?
    public var httpPort: Int?
    public var home: String
    public var services: [Service]
    public var sites: [Site]
    public var ca: CAStatus
    public var domains: [Domain]
    public var mysql: MySQLStatus
    /// Set while MySQL is enabled.
    public var adminerUrl: String?
    public var mail: MailStatus?
    /// PostgreSQL and Redis share MySQL's status fields.
    public var postgres: MySQLStatus?
    public var redis: MySQLStatus?
    /// The site open on the phone, if any.
    public var phone: PhoneStatus?
    public var groups: [SiteGroup]?
    public var orphans: [String]?
}

public struct PHPVersion: Codable, Sendable, Hashable {
    public var version: String
    public var minor: String
    public var path: String
}

/// A site served on the local network for a phone.
public struct PhoneStatus: Codable, Sendable, Hashable {
    public var site: String
    public var host: String
    public var port: Int
    public var url: String
    /// Plain HTTP on the Mac's address: opens at once, nothing to install.
    public var plainUrl: String?
    public var caPageUrl: String
    public var addresses: [String]
    /// "off", "on", "block-all" or "unknown".
    public var firewall: String
    public var minutesLeft: Int
}

/// A saved WordPress blueprint.
public struct BlueprintInfo: Codable, Sendable, Hashable, Identifiable {
    public var name: String
    public var description: String?
    public var themes: Int
    public var plugins: Int
    public var updated: Date
    public var id: String { name }
}

/// What applying a blueprint did (or would do, in a dry run).
public struct BlueprintResult: Codable, Sendable, Hashable {
    public var blueprint: String
    public var installed: [String]
    public var warnings: [String]?
    public var snapshot: String?
    public var notice: String
}

/// A blueprint saved from a site, with what could not be included.
public struct SavedBlueprint: Codable, Sendable, Hashable {
    public var blueprint: BlueprintInfo
    public var warnings: [String]?
}

/// What AI agents may change.
public struct MCPPermissions: Codable, Sendable, Hashable {
    public var all: Bool
    public var sites: [String]
    /// Sites agents may change only while a "Work with an agent" session lasts.
    public var temporary: [String]?
}

/// The time an AI agent works on a site: Benbow keeps the starting state
/// until the user keeps the changes or undoes them all.
public struct AgentSession: Codable, Sendable, Hashable, Identifiable {
    public var id: String
    public var site: String
    public var openedAt: Date
    public var lastActive: Date
    public var agent: String?
    public var source: String
    public var baseSnapshot: String
    public var databaseSaved: Bool
    public var writeGrant: Bool?
    public var launchedTool: String?
    public var agentRunning: Bool?
    public var closedAt: Date?
    public var outcome: String?
    public var url: String?
    public var base: Snapshot?
    public var checkpoints: [Snapshot]
    public var idleHours: Int

    /// "Claude Code", or "An AI agent" when the agent did not say.
    public var agentName: String { (agent?.isEmpty == false ? agent : nil) ?? "An AI agent" }

    /// Whether the agent still seems at work: an agent Benbow started runs
    /// until its terminal window ends; others count while they called Benbow
    /// in the last 15 minutes. Either way the session stays open until the
    /// user keeps or undoes the changes.
    public func isWorking(now: Date = Date()) -> Bool {
        if launchedTool?.isEmpty == false { return agentRunning == true }
        return now.timeIntervalSince(lastActive) < 15 * 60
    }
}

/// What changed since a session started.
public struct SessionDiff: Codable, Sendable, Hashable {
    public struct Files: Codable, Sendable, Hashable {
        public var added: Int
        public var modified: Int
        public var deleted: Int
        public var changes: [FileChange]
        public var truncated: Bool?
    }
    public struct FileChange: Codable, Sendable, Hashable, Identifiable {
        public var path: String
        public var change: String
        public var size: Int64
        public var id: String { path }
    }
    public struct Database: Codable, Sendable, Hashable {
        public var engine: String
        public var database: String
        public var tables: [TableChange]
        public var approximate: Bool?
        public var note: String?
    }
    public struct TableChange: Codable, Sendable, Hashable, Identifiable {
        public var table: String
        public var change: String
        public var inserted: Int64?
        public var updated: Int64?
        public var deleted: Int64?
        public var id: String { table }
    }
    public var site: String
    public var since: Date
    public var files: Files
    public var database: Database?
}

public struct CloseSessionResult: Codable, Sendable, Hashable {
    public var restored: RestoreResult?
}

/// An AI agent on this Mac.
public struct AgentTool: Codable, Sendable, Hashable, Identifiable {
    public var id: String
    public var name: String
    public var kind: String
    public var installed: Bool
    public var path: String?
    public var connected: Bool
    public var configPath: String?
    public var perRun: Bool
    public var canConnect: Bool
}

public struct AgentTools: Codable, Sendable, Hashable {
    public var agents: [AgentTool]
    public var last: String?
    public var terminal: String?
    public var benbowBin: String
}

public struct AgentConnectResult: Codable, Sendable, Hashable {
    public var configPath: String
    public var backup: String?
    public var command: String?
    public var already: Bool?
    public var restart: Bool?
}

public struct AgentStartResult: Codable, Sendable, Hashable {
    public struct Started: Codable, Sendable, Hashable {
        public var terminal: String?
    }
    public var session: AgentSession
    public var started: Started
}

/// One change (or refused attempt) by an AI agent.
public struct MCPAction: Codable, Sendable, Hashable, Identifiable {
    public var id: Int64
    public var at: Date
    public var tool: String
    public var site: String?
    public var ok: Bool
    public var error: String?
    public var snapshot: String?
    public var dryRun: Bool?
}

/// A copy of a site on a server.
public struct Remote: Codable, Sendable, Hashable, Identifiable {
    public var site: String
    public var name: String
    /// production or staging.
    public var kind: String
    /// ssh, sftp, ftps or ftp.
    public var transport: String
    public var host: String
    public var port: Int?
    public var user: String
    public var path: String
    public var url: String
    public var webroot: String?
    public var paths: [String]
    public var excludes: [String]
    public var id: String { name }

    public init(site: String, name: String, kind: String, transport: String, host: String, port: Int?, user: String,
                path: String, url: String, webroot: String?, paths: [String], excludes: [String]) {
        self.site = site; self.name = name; self.kind = kind; self.transport = transport; self.host = host
        self.port = port; self.user = user; self.path = path; self.url = url; self.webroot = webroot
        self.paths = paths; self.excludes = excludes
    }
}

/// What a pull or push did (or would do).
public struct SyncResult: Codable, Sendable, Hashable {
    public var remote: String
    public var dryRun: Bool
    public var files: [String]
    public var database: String?
    public var urlFrom: String?
    public var urlTo: String?
    public var snapshot: String?
    public var steps: [String]
}

/// A saved state of a site: files (APFS clones) and database.
public struct Snapshot: Codable, Sendable, Hashable, Identifiable {
    public var id: String
    public var site: String
    public var createdAt: Date
    public var kind: String
    public var note: String?
    public var database: String?
    public var bytes: Int64
}

public struct RestoreResult: Codable, Sendable, Hashable {
    public var restored: Snapshot
    public var before: Snapshot
}

/// A new Laravel project.
public struct LaravelResult: Codable, Sendable, Hashable {
    public var name: String
    public var url: String
    public var laravel: String
    public var database: String
}

/// A tool run (wp-cli, Composer, Artisan).
public struct ToolResult: Codable, Sendable, Hashable {
    public var exitCode: Int
    public var output: String
    public var errors: String?
}

/// What in a site's code would break on another PHP version.
public struct PHPCheckResult: Codable, Sendable, Hashable {
    public struct Message: Codable, Sendable, Hashable {
        public var line: Int
        public var column: Int
        public var type: String
        public var message: String
        public var source: String
    }

    public struct File: Codable, Sendable, Hashable, Identifiable {
        public var path: String
        public var messages: [Message]
        public var id: String { path }
    }

    public var site: String
    public var php: String
    public var target: String
    public var wordpress: Bool
    public var scanned: [String]
    public var errors: Int
    public var warnings: Int
    public var files: [File]
    public var seconds: Double
}

/// How long php.net maintains a PHP version.
public struct PHPSupport: Codable, Sendable, Hashable {
    public var minor: String
    /// "active", "security" (security fixes only) or "eol" (no fixes).
    public var status: String
    public var activeUntil: String?
    public var securityUntil: String?

    public var isEOL: Bool { status == "eol" }
}

public struct PHPList: Codable, Sendable, Hashable {
    public var installed: [PHPVersion]
    public var available: [String]
    public var support: [PHPSupport]?

    public func support(for minor: String) -> PHPSupport? {
        support?.first { $0.minor == minor }
    }

    /// Minor versions Benbow can serve, newest first ("8.4", "8.2", …).
    public var minors: [String] {
        var seen: [String] = []
        for v in available {
            let minor = v.split(separator: ".").prefix(2).joined(separator: ".")
            if !seen.contains(minor) { seen.append(minor) }
        }
        return seen.sorted { $0.compare($1, options: .numeric) == .orderedDescending }
    }
}

/// php.ini values of one PHP minor version. post_max_size follows
/// uploadMaxFilesize.
public struct PHPSettings: Codable, Sendable, Hashable {
    public var php: String
    public var memoryLimit: String
    public var uploadMaxFilesize: String
    public var maxExecutionTime: Int
    public var displayErrors: Bool
}

/// A new WordPress site. The admin password is shown once, never stored.
public struct WordPressResult: Codable, Sendable, Hashable, Identifiable {
    public var name: String
    public var url: String
    public var path: String
    public var wordpress: String
    public var database: String
    public var adminUrl: String
    public var adminUser: String
    public var adminPassword: String
    public var blueprint: BlueprintResult?
    public var id: String { name }
}

/// A site's database and how it connects.
public struct SiteDatabase: Codable, Sendable, Hashable {
    public var site: String
    public var database: String
    public var user: String
    public var password: String?
    public var host: String
    public var port: Int
    public var socket: String
    public var dedicated: Bool
    /// "mysql" or "pgsql".
    public var engine: String?

    /// The lines to paste into wp-config.php.
    public var wpConfig: String {
        """
        define( 'DB_NAME', '\(database)' );
        define( 'DB_USER', '\(user)' );
        define( 'DB_PASSWORD', '\(password ?? "")' );
        define( 'DB_HOST', 'localhost' );
        """
    }
}

/// The end of a site's access log or PHP error log.
public struct SiteLog: Codable, Sendable, Hashable {
    public var site: String
    public var kind: String
    public var path: String
    public var size: Int
    public var lines: [String]
}

/// A site's effective php.ini values and which ones are its own.
public struct SitePHPSettings: Codable, Sendable, Hashable {
    public var site: String
    public var php: String
    public var memoryLimit: String
    public var uploadMaxFilesize: String
    public var maxExecutionTime: Int
    public var displayErrors: Bool
    /// php.ini name → value set for this site only.
    public var overrides: [String: String]
}

public struct XdebugStatus: Codable, Sendable, Hashable, Identifiable {
    public var php: String
    public var phpVersion: String
    public var enabled: Bool
    public var available: Bool
    public var version: String?
    public var id: String { php }
}

public struct SiteResult: Codable, Sendable, Hashable {
    public var name: String
    public var url: String
    public var applied: Bool
}

/// The result of cloning a site (POST /v1/sites/{name}/clone).
public struct CloneResult: Codable, Sendable, Hashable {
    public var name: String
    public var url: String
    public var folder: String?
    public var database: String?
    public var urlFrom: String?
    public var warnings: [String]?
}

public struct MAMPHost: Codable, Sendable, Hashable {
    public var serverName: String
    public var documentRoot: String
    public var phpVersion: String
    public var databases: [String]
}

public struct ImportItem: Codable, Sendable, Hashable {
    public var mamp: MAMPHost
    public var action: String
    public var reason: String?
    public var name: String?
    public var domain: String?
    public var php: String?
    public var aliases: [String]?
    /// The MAMP database linked to the new site (already on Benbow's MySQL).
    public var database: String?
    public var warnings: [String]
    /// What migrating the site does (or did).
    public var migration: MigrationPlan?

    public var creates: Bool { action == "create" }
}

public struct ImportPlan: Codable, Sendable, Hashable {
    public var source: String
    public var dryRun: Bool
    public var items: [ImportItem]
    public var create: Int
    public var skip: Int
    public var needsLocal: Bool
    public var needsTest: Bool?
    public var databases: [String]
    public var applied: Bool
    public var kind: String?
    public var migrated: Int?
}

/// One site's assisted migration.
public struct MigrationPlan: Codable, Sendable, Hashable {
    public var framework: String?
    public var database: String?
    public var server: String?
    public var exists: Bool
    public var config: String?
    public var urlFrom: String?
    public var urlTo: String?
    public var steps: [String]
    public var manual: [String: String]?
    public var warnings: [String]?
    public var problem: String?
    public var done: Bool?
    public var error: String?
}

/// What a migration changed (shown in the site detail, undoable).
public struct SiteMigration: Codable, Sendable, Hashable {
    public var source: String
    public var database: String
    public var configPath: String?
    public var backupPath: String?
    public var urlFrom: String?
    public var createdDatabase: Bool
}

public struct DaemonInfo: Codable, Sendable, Hashable {
    public var socket: String
    public var answering: Bool
    public var pid: Int?
}

/// The state of an app's dev server (Site.app, POST /v1/sites/{name}/restart).
public struct AppStatus: Codable, Sendable, Hashable {
    /// starting, running, exited, error, stopped, or waiting (a proxied
    /// app with nothing on its port).
    public var state: String
    public var port: Int
    public var pid: Int?
    public var exitCode: Int?
    public var message: String?
    public var since: Date?
    public var log: String?
    public var logTail: [String]?
}

/// An app's variables (GET/PATCH /v1/sites/{name}/env): names only.
public struct AppEnv: Codable, Sendable, Hashable {
    public var site: String
    /// The user's own variables, kept in the login keychain.
    public var names: [String]
    /// The variables Benbow sets itself (APP_URL, DATABASE_URL…).
    public var provided: [String]?
    public var restarted: Bool?
}

/// The command Benbow proposes for an app's folder (GET /v1/detect).
public struct AppDetection: Codable, Sendable, Hashable {
    public var path: String
    public var framework: String?
    /// "" when nothing was recognised; "{port}" is the app's port.
    public var command: String
    public var packageManager: String?
    public var notes: [String]
}
