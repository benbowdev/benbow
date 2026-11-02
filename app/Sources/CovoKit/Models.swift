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
    /// Extra domains served by the same site.
    public var aliases: [String]
    /// The linked MySQL database, or "".
    public var database: String
    /// The site's own MySQL user, when Covo created the database.
    public var databaseUser: String?
    public var url: String
    public var id: String { name }
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

public struct Status: Codable, Sendable, Hashable {
    public var running: Bool
    public var httpsPort: Int?
    public var home: String
    public var services: [Service]
    public var sites: [Site]
    public var ca: CAStatus
    public var domains: [Domain]
    public var mysql: MySQLStatus
    /// Set while MySQL is enabled.
    public var adminerUrl: String?
    public var orphans: [String]?
}

public struct PHPVersion: Codable, Sendable, Hashable {
    public var version: String
    public var minor: String
    public var path: String
}

public struct PHPList: Codable, Sendable, Hashable {
    public var installed: [PHPVersion]
    public var available: [String]

    /// Minor versions Covo can serve, newest first ("8.4", "8.2", …).
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
    public var warnings: [String]

    public var creates: Bool { action == "create" }
}

public struct ImportPlan: Codable, Sendable, Hashable {
    public var source: String
    public var dryRun: Bool
    public var items: [ImportItem]
    public var create: Int
    public var skip: Int
    public var needsLocal: Bool
    public var databases: [String]
    public var applied: Bool
}

public struct DaemonInfo: Codable, Sendable, Hashable {
    public var socket: String
    public var answering: Bool
    public var pid: Int?
}
