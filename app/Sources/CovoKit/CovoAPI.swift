// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import Foundation

/// An error reported by the core, with its stable code (see docs/api.md).
public struct APIError: Error, Sendable, Equatable, LocalizedError {
    public let code: String
    public let message: String

    public var errorDescription: String? { message }
}

struct Envelope<T: Decodable>: Decodable {
    let schema: Int
    let ok: Bool
    let data: T?
    let error: ErrorBody?

    struct ErrorBody: Decodable {
        let code: String
        let message: String
    }
}

/// Decodes the core's JSON envelope.
public enum EnvelopeDecoder {
    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .iso8601
        return d
    }()

    public static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        let env = try decoder.decode(Envelope<T>.self, from: data)
        if let e = env.error, !env.ok {
            throw APIError(code: e.code, message: e.message)
        }
        guard env.schema == 1, env.ok, let value = env.data else {
            throw APIError(code: "internal", message: "Unexpected answer from the Covo core (schema \(env.schema)).")
        }
        return value
    }
}

/// The core's local API (`covo daemon`).
public struct CovoAPI: Sendable {
    let http: UnixHTTP

    public init(socketPath: String) {
        http = UnixHTTP(socketPath: socketPath)
    }

    static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        return e
    }()

    func call<T: Decodable>(_ method: String, _ path: String, _ body: (some Encodable)? = nil as Empty?) async throws -> T {
        let data = try body.map { try Self.encoder.encode($0) }
        let resp = try await http.send(method: method, path: path, body: data)
        return try EnvelopeDecoder.decode(T.self, from: resp.body)
    }

    struct Empty: Encodable {}

    public func status() async throws -> Status { try await call("GET", "/v1/status") }

    public func ports() async throws -> PortsStatus { try await call("GET", "/v1/ports") }

    struct PHPCheckRequest: Encodable {
        let target: String
        let all: Bool
    }

    /// Checks a site's code against a PHP version (minutes on a large site).
    public func checkPHP(_ site: String, target: String, all: Bool = false) async throws -> PHPCheckResult {
        try await call("POST", "/v1/sites/\(escape(site))/php-check", PHPCheckRequest(target: target, all: all))
    }

    struct PortsRequest: Encodable {
        let httpsPort: Int
        let httpPort: Int
    }

    /// Sets the HTTPS port and the HTTP redirect port (0: off). 443 and 80
    /// make macOS ask for an administrator password once.
    public func setPorts(https: Int, http: Int) async throws -> PortsStatus {
        try await call("PUT", "/v1/ports", PortsRequest(httpsPort: https, httpPort: http))
    }

    struct StartRequest: Encodable { let httpsPort: Int? }

    /// Starts Covo; a non-nil port also changes the HTTPS port.
    public func start(httpsPort: Int? = nil) async throws {
        let _: Ignored = try await call("POST", "/v1/start", StartRequest(httpsPort: httpsPort))
    }

    public func stop() async throws {
        let _: Ignored = try await call("POST", "/v1/stop")
    }

    public func php() async throws -> PHPList { try await call("GET", "/v1/php") }

    /// Everything a new site can start with. The core applies it in one
    /// step and undoes it all if any part fails.
    public struct NewSite: Encodable, Sendable {
        public var name: String
        public var domain: String?
        public var php: String
        public var path: String
        public var force = false
        /// Create the folder (missing or empty) for a new, empty site.
        public var createFolder = false
        public var aliases: [String] = []
        public var groupId: Int64?
        public var phpSettings: [String: String] = [:]
        /// A new database with its own user, or an existing one to link.
        public var newDatabase = false
        public var linkDatabase: String?

        public init(name: String, domain: String? = nil, php: String, path: String) {
            self.name = name
            self.domain = domain
            self.php = php
            self.path = path
        }
    }

    public func addSite(_ site: NewSite) async throws -> SiteResult {
        try await call("POST", "/v1/sites", site)
    }

    public func addSite(name: String, domain: String? = nil, php: String, path: String, force: Bool = false) async throws -> SiteResult {
        var s = NewSite(name: name, domain: domain, php: php, path: path)
        s.force = force
        return try await addSite(s)
    }

    struct SiteChange: Encodable {
        let php: String?
        let path: String?
        let domain: String?
        let enabled: Bool?
        let archived: Bool?
        let aliases: [String]?
        let groupId: Int64?
        let force: Bool
    }

    /// Changes a site; nil fields are left as they are. group 0 moves the
    /// site to the top level.
    public func updateSite(_ name: String, php: String? = nil, path: String? = nil, domain: String? = nil,
                           enabled: Bool? = nil, archived: Bool? = nil, aliases: [String]? = nil,
                           group: Int64? = nil, force: Bool = false) async throws -> SiteResult {
        try await call("PATCH", "/v1/sites/" + escape(name),
                       SiteChange(php: php, path: path, domain: domain, enabled: enabled, archived: archived,
                                  aliases: aliases, groupId: group, force: force))
    }

    /// A new WordPress site; domain, aliases, group and php.ini values as
    /// for NewSite.
    public struct NewWordPress: Encodable, Sendable {
        public var name: String
        public var path: String
        public var php: String
        public var title: String
        public var adminUser: String
        public var adminEmail: String
        public var domain: String?
        public var aliases: [String] = []
        public var groupId: Int64?
        public var phpSettings: [String: String] = [:]

        public init(name: String, path: String, php: String, title: String, adminUser: String, adminEmail: String) {
            self.name = name
            self.path = path
            self.php = php
            self.title = title
            self.adminUser = adminUser
            self.adminEmail = adminEmail
        }
    }

    public func newWordPress(_ req: NewWordPress) async throws -> WordPressResult {
        try await call("POST", "/v1/wordpress", req)
    }

    public func newWordPress(name: String, path: String, php: String, title: String,
                             adminUser: String, adminEmail: String) async throws -> WordPressResult {
        try await newWordPress(NewWordPress(name: name, path: path, php: php, title: title,
                                            adminUser: adminUser, adminEmail: adminEmail))
    }

    public struct NewLaravel: Encodable, Sendable {
        public var name: String
        public var path: String
        public var php: String
        /// "new" or "sqlite".
        public var database: String
        public var domain: String?
        public var aliases: [String] = []
        public var groupId: Int64?
        public var phpSettings: [String: String] = [:]

        public init(name: String, path: String, php: String, database: String) {
            self.name = name
            self.path = path
            self.php = php
            self.database = database
        }
    }

    /// Creates a Laravel project with Composer (a minute or more).
    public func newLaravel(_ req: NewLaravel) async throws -> LaravelResult {
        try await call("POST", "/v1/laravel", req)
    }

    struct ToolRequest: Encodable {
        let tool: String
        let args: [String]
    }

    /// Runs wp-cli, Composer or Artisan for a site, without input.
    public func runTool(_ site: String, tool: String, args: [String]) async throws -> ToolResult {
        try await call("POST", "/v1/sites/\(escape(site))/run", ToolRequest(tool: tool, args: args))
    }

    struct GroupRequest: Encodable {
        let name: String?
        let parentId: Int64?
    }

    public func addGroup(name: String, parent: Int64?) async throws -> SiteGroup {
        try await call("POST", "/v1/groups", GroupRequest(name: name, parentId: parent))
    }

    /// Renames a group (name) or moves it (parent: 0 for the top level).
    public func updateGroup(_ id: Int64, name: String? = nil, parent: Int64? = nil) async throws -> SiteGroup {
        try await call("PATCH", "/v1/groups/\(id)", GroupRequest(name: name, parentId: parent))
    }

    /// Removes a group; its sites and groups move up one level.
    public func removeGroup(_ id: Int64) async throws {
        let _: Ignored = try await call("DELETE", "/v1/groups/\(id)")
    }

    struct AdminerRequest: Encodable { let site: String }
    struct AdminerLogin: Decodable { let url: String }

    /// A one-time URL that opens Adminer logged in, as root (site "") or as
    /// the site's database user.
    public func adminerLogin(site: String = "") async throws -> URL {
        let r: AdminerLogin = try await call("POST", "/v1/adminer/login", AdminerRequest(site: site))
        guard let url = URL(string: r.url) else { throw APIError(code: "internal", message: "Bad Adminer URL") }
        return url
    }

    public func setMail(enabled: Bool) async throws {
        let _: Ignored = try await call("POST", enabled ? "/v1/mail/enable" : "/v1/mail/disable")
    }

    public func updates() async throws -> UpdatesStatus { try await call("GET", "/v1/updates") }

    public func checkUpdates() async throws -> UpdatesStatus { try await call("POST", "/v1/updates/check") }

    struct ApplyRequest: Encodable { let components: [String] }

    /// Installs updates; an empty list means every available one.
    public func applyUpdates(_ components: [String] = []) async throws -> UpdatesStatus {
        try await call("POST", "/v1/updates/apply", ApplyRequest(components: components))
    }

    public func databases() async throws -> [String] { try await call("GET", "/v1/databases") }

    public func siteDatabase(_ name: String) async throws -> SiteDatabase {
        try await call("GET", "/v1/sites/\(escape(name))/database")
    }

    struct DatabaseRequest: Encodable {
        let name: String
        let link: Bool
    }

    /// Creates a database with its own user; an empty name means the site's.
    public func createSiteDatabase(_ name: String, database: String = "") async throws -> SiteDatabase {
        try await call("POST", "/v1/sites/\(escape(name))/database", DatabaseRequest(name: database, link: false))
    }

    public func linkSiteDatabase(_ name: String, database: String) async throws -> SiteDatabase {
        try await call("POST", "/v1/sites/\(escape(name))/database", DatabaseRequest(name: database, link: true))
    }

    /// Forgets the site's database; drop also deletes it.
    public func unlinkSiteDatabase(_ name: String, drop: Bool) async throws {
        let _: Ignored = try await call("DELETE", "/v1/sites/\(escape(name))/database" + (drop ? "?drop=true" : ""))
    }

    public func sitePHPSettings(_ name: String) async throws -> SitePHPSettings {
        try await call("GET", "/v1/sites/\(escape(name))/php-settings")
    }

    /// php.ini name → value; "" goes back to the PHP version's value.
    public func setSitePHPSettings(_ name: String, _ values: [String: String]) async throws -> SitePHPSettings {
        // Keys are php.ini names already: encode them as they are.
        let data = try JSONEncoder().encode(values)
        let resp = try await http.send(method: "PATCH", path: "/v1/sites/\(escape(name))/php-settings", body: data)
        return try EnvelopeDecoder.decode(SitePHPSettings.self, from: resp.body)
    }

    /// kind is "access" or "php".
    public func siteLogs(_ name: String, kind: String, lines: Int = 500) async throws -> SiteLog {
        try await call("GET", "/v1/sites/\(escape(name))/logs/\(escape(kind))?lines=\(lines)")
    }

    public func clearSiteLogs(_ name: String) async throws {
        let _: Ignored = try await call("DELETE", "/v1/sites/\(escape(name))/logs")
    }

    /// Removes a site; dropDatabase also deletes its database first.
    public func removeSite(_ name: String, dropDatabase: Bool = false) async throws {
        let _: Ignored = try await call("DELETE", "/v1/sites/" + escape(name) + (dropDatabase ? "?drop_database=true" : ""))
    }

    struct InstallRequest: Encodable { let version: String }

    public func installPHP(_ version: String) async throws {
        let _: Ignored = try await call("POST", "/v1/php", InstallRequest(version: version))
    }

    public func phpSettings(_ minor: String) async throws -> PHPSettings {
        try await call("GET", "/v1/php/\(escape(minor))/settings")
    }

    struct SettingsChange: Encodable {
        let memoryLimit: String
        let uploadMaxFilesize: String
        let maxExecutionTime: Int
        let displayErrors: Bool
    }

    public func setPHPSettings(_ s: PHPSettings) async throws -> PHPSettings {
        try await call("PATCH", "/v1/php/\(escape(s.php))/settings", SettingsChange(
            memoryLimit: s.memoryLimit, uploadMaxFilesize: s.uploadMaxFilesize,
            maxExecutionTime: s.maxExecutionTime, displayErrors: s.displayErrors))
    }

    public func xdebug() async throws -> [XdebugStatus] { try await call("GET", "/v1/xdebug") }

    struct Toggle: Encodable { let enabled: Bool }

    public func setXdebug(php: String, enabled: Bool) async throws {
        let _: Ignored = try await call("PUT", "/v1/xdebug/" + escape(php), Toggle(enabled: enabled))
    }

    public func enableDomain(_ zone: String) async throws {
        let _: Ignored = try await call("POST", "/v1/domains/\(escape(zone))/enable")
    }

    public func disableDomain(_ zone: String) async throws {
        let _: Ignored = try await call("POST", "/v1/domains/\(escape(zone))/disable")
    }

    public func trust() async throws {
        let _: Ignored = try await call("POST", "/v1/trust")
    }

    public func untrust() async throws {
        let _: Ignored = try await call("POST", "/v1/untrust")
    }

    struct PortRequest: Encodable { let port: Int? }

    /// Turns MySQL on; a non-nil port also changes its port.
    public func enableMySQL(port: Int? = nil) async throws {
        let _: Ignored = try await call("POST", "/v1/mysql/enable", PortRequest(port: port))
    }

    public func disableMySQL() async throws {
        let _: Ignored = try await call("POST", "/v1/mysql/disable")
    }

    public func mysqlCredentials() async throws -> MySQLCredentials {
        try await call("GET", "/v1/mysql/credentials")
    }

    struct ImportRequest: Encodable {
        let dryRun: Bool
        let localhost: Bool
    }

    public func importMAMP(dryRun: Bool, localhost: Bool) async throws -> ImportPlan {
        try await call("POST", "/v1/import/mamp", ImportRequest(dryRun: dryRun, localhost: localhost))
    }

    func escape(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(CharacterSet(charactersIn: "-._"))) ?? s
    }
}

/// For calls whose answer the app does not need.
struct Ignored: Decodable {}
