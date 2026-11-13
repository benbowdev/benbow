// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import Foundation

/// An error reported by the core, with its stable code (see docs/api.md).
public struct APIError: Error, Sendable, Equatable, LocalizedError {
    public let code: String
    public let message: String

    public var errorDescription: String? { message }

    public init(code: String, message: String) {
        self.code = code
        self.message = message
    }
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
        // RFC 3339 with or without fractional seconds (Go's time.Time).
        d.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            if let date = try? Date(text, strategy: .iso8601.year().month().day().time(includingFractionalSeconds: true).timeZone(separator: .omitted)) {
                return date
            }
            for fractional in [true, false] {
                let f = ISO8601DateFormatter()
                f.formatOptions = fractional ? [.withInternetDateTime, .withFractionalSeconds] : [.withInternetDateTime]
                if let date = f.date(from: text) { return date }
            }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date \(text)"))
        }
        return d
    }()

    public static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        let env = try decoder.decode(Envelope<T>.self, from: data)
        if let e = env.error, !env.ok {
            throw APIError(code: e.code, message: e.message)
        }
        guard env.schema == 1, env.ok, let value = env.data else {
            throw APIError(code: "internal", message: "Unexpected answer from the Benbow core (schema \(env.schema)).")
        }
        return value
    }
}

/// The core's local API (`benbow daemon`).
public struct BenbowAPI: Sendable {
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

    public func blueprints() async throws -> [BlueprintInfo] { try await call("GET", "/v1/blueprints") }

    /// A blueprint's JSON, as a file would hold it.
    public func blueprintJSON(_ name: String) async throws -> Data {
        let resp = try await http.send(method: "GET", path: "/v1/blueprints/" + escape(name), body: nil)
        guard let env = try JSONSerialization.jsonObject(with: resp.body) as? [String: Any] else {
            throw APIError(code: "internal", message: "Unexpected answer from the Benbow core.")
        }
        if env["ok"] as? Bool != true {
            let e = env["error"] as? [String: Any]
            throw APIError(code: e?["code"] as? String ?? "internal", message: e?["message"] as? String ?? "The blueprint could not be read.")
        }
        return try JSONSerialization.data(withJSONObject: env["data"] ?? [:], options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes])
    }

    /// Adds a blueprint from its JSON (name: the file's, unless given).
    public func importBlueprint(_ json: Data, name: String, overwrite: Bool) async throws -> BlueprintInfo {
        let resp = try await http.send(method: "PUT", path: "/v1/blueprints/" + escape(name) + (overwrite ? "?overwrite=1" : ""), body: json)
        return try EnvelopeDecoder.decode(BlueprintInfo.self, from: resp.body)
    }

    public func deleteBlueprint(_ name: String) async throws {
        let _: Ignored = try await call("DELETE", "/v1/blueprints/" + escape(name))
    }

    struct SaveBlueprintRequest: Encodable {
        let name: String
        let overwrite: Bool
    }

    public func saveBlueprint(site: String, name: String, overwrite: Bool) async throws -> SavedBlueprint {
        try await call("POST", "/v1/sites/\(escape(site))/blueprint", SaveBlueprintRequest(name: name, overwrite: overwrite))
    }

    struct ApplyBlueprintRequest: Encodable {
        let site: String
        let dryRun: Bool
        let acceptChanges: Bool
    }

    public func applyBlueprint(_ name: String, site: String, dryRun: Bool, acceptChanges: Bool) async throws -> BlueprintResult {
        try await call("POST", "/v1/blueprints/\(escape(name))/apply",
                       ApplyBlueprintRequest(site: site, dryRun: dryRun, acceptChanges: acceptChanges))
    }

    struct GitHubTokenState: Codable { let stored: Bool }
    struct GitHubTokenRequest: Encodable { let token: String }

    public func hasGitHubToken() async throws -> Bool {
        let s: GitHubTokenState = try await call("GET", "/v1/github-token")
        return s.stored
    }

    /// token "" removes it.
    public func setGitHubToken(_ token: String) async throws -> Bool {
        let s: GitHubTokenState = try await call("PUT", "/v1/github-token", GitHubTokenRequest(token: token))
        return s.stored
    }

    public func mcpPermissions() async throws -> MCPPermissions { try await call("GET", "/v1/mcp/permissions") }

    struct MCPPermissionRequest: Encodable {
        let scope: String
        let write: Bool
    }

    /// scope: a site name or "*".
    public func setMCPPermission(_ scope: String, write: Bool) async throws -> MCPPermissions {
        try await call("PUT", "/v1/mcp/permissions", MCPPermissionRequest(scope: scope, write: write))
    }

    public func mcpActions() async throws -> [MCPAction] { try await call("GET", "/v1/mcp/actions") }

    public func agentSessions() async throws -> [AgentSession] { try await call("GET", "/v1/agent/sessions") }

    public func agentSessionDiff(_ site: String) async throws -> SessionDiff {
        try await call("GET", "/v1/sites/\(escape(site))/agent-session/diff")
    }

    /// Keeps the changes (keep: true) or undoes them all.
    public func closeAgentSession(_ site: String, keep: Bool) async throws -> CloseSessionResult {
        try await call("POST", "/v1/sites/\(escape(site))/agent-session/\(keep ? "keep" : "revert")")
    }

    public func agentIdleHours() async throws -> Int {
        struct R: Decodable { let hours: Int }
        let r: R = try await call("GET", "/v1/agent/idle")
        return r.hours
    }

    struct IdleRequest: Encodable { let hours: Int }

    public func setAgentIdleHours(_ h: Int) async throws {
        let _: Ignored = try await call("PUT", "/v1/agent/idle", IdleRequest(hours: h))
    }

    public func agentTools() async throws -> AgentTools { try await call("GET", "/v1/agent/tools") }

    public func connectAgent(_ id: String) async throws -> AgentConnectResult {
        try await call("POST", "/v1/agent/tools/\(escape(id))/connect")
    }

    struct AgentStartRequest: Encodable {
        let tool: String
        let terminal: String?
        let connect: Bool
    }

    public func startAgent(site: String, tool: String, terminal: String?, connect: Bool) async throws -> AgentStartResult {
        try await call("POST", "/v1/sites/\(escape(site))/agent/start", AgentStartRequest(tool: tool, terminal: terminal, connect: connect))
    }

    public func remotes(_ site: String) async throws -> [Remote] {
        try await call("GET", "/v1/sites/\(escape(site))/remotes")
    }

    struct RemoteRequest: Encodable {
        let kind, transport, host: String
        let port: Int
        let user, path, url, webroot: String
        let paths, excludes: [String]
        let password: String?
    }

    /// Saves a remote; password nil keeps the stored one, "" forgets it.
    public func setRemote(_ r: Remote, password: String?) async throws -> Remote {
        try await call("PUT", "/v1/sites/\(escape(r.site))/remotes/\(escape(r.name))", RemoteRequest(
            kind: r.kind, transport: r.transport, host: r.host, port: r.port ?? 0, user: r.user, path: r.path, url: r.url,
            webroot: r.webroot ?? "", paths: r.paths, excludes: r.excludes, password: password))
    }

    public func removeRemote(_ site: String, name: String) async throws {
        let _: Ignored = try await call("DELETE", "/v1/sites/\(escape(site))/remotes/\(escape(name))")
    }

    struct SyncRequest: Encodable {
        let dryRun, noFiles, noDatabase: Bool
    }

    /// "pull" or "push".
    public func sync(_ op: String, site: String, remote: String, dryRun: Bool, files: Bool, database: Bool) async throws -> SyncResult {
        try await call("POST", "/v1/sites/\(escape(site))/remotes/\(escape(remote))/\(op)",
                       SyncRequest(dryRun: dryRun, noFiles: !files, noDatabase: !database))
    }

    public func snapshots(_ site: String) async throws -> [Snapshot] {
        try await call("GET", "/v1/sites/\(escape(site))/snapshots")
    }

    struct NoteRequest: Encodable { let note: String }

    public func createSnapshot(_ site: String, note: String) async throws -> Snapshot {
        try await call("POST", "/v1/sites/\(escape(site))/snapshots", NoteRequest(note: note))
    }

    struct RestoreRequest: Encodable { let php: Bool }

    public func restoreSnapshot(_ site: String, id: String, php: Bool = false) async throws -> RestoreResult {
        try await call("POST", "/v1/sites/\(escape(site))/snapshots/\(escape(id))/restore", RestoreRequest(php: php))
    }

    /// The space snapshots take, of one site or (nil) all.
    public func snapshotUsage(_ site: String? = nil) async throws -> SnapshotsUsage {
        var path = "/v1/snapshots/usage"
        if let site { path += "?site=" + escape(site) }
        return try await call("GET", path)
    }

    struct PruneRequest: Encodable {
        let site: String?
        let olderThanDays: Int
        let dryRun: Bool
    }

    /// Deletes (or with dryRun lists) snapshots older than the given days.
    public func pruneSnapshots(site: String? = nil, olderThanDays: Int, dryRun: Bool) async throws -> PruneResult {
        try await call("POST", "/v1/snapshots/prune", PruneRequest(site: site, olderThanDays: olderThanDays, dryRun: dryRun))
    }

    public func deleteSnapshot(_ site: String, id: String) async throws {
        let _: Ignored = try await call("DELETE", "/v1/sites/\(escape(site))/snapshots/\(escape(id))")
    }

    struct KeepRequest: Codable { let keep: Int }

    public func snapshotKeep() async throws -> Int {
        let r: KeepRequest = try await call("GET", "/v1/snapshots/keep")
        return r.keep
    }

    public func setSnapshotKeep(_ n: Int) async throws {
        let _: Ignored = try await call("PUT", "/v1/snapshots/keep", KeepRequest(keep: n))
    }

    struct PhoneRequest: Encodable { let minutes: Int }

    /// Opens a site on the local network for minutes.
    public func sharePhone(_ site: String, minutes: Int) async throws -> PhoneStatus {
        try await call("PUT", "/v1/sites/\(escape(site))/phone", PhoneRequest(minutes: minutes))
    }

    public func stopPhone() async throws {
        let _: Ignored = try await call("DELETE", "/v1/phone")
    }

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

    /// Starts Benbow; a non-nil port also changes the HTTPS port.
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
        /// "mysql" or "pgsql".
        public var databaseEngine: String?
        /// Apps: the command Benbow starts ("{port}" is its port), or the port
        /// of a dev server the user runs (proxyPort); port fixes the
        /// started command's port; appHost "localhost" sends that Host.
        public var command: String?
        public var port: Int?
        public var proxyPort: Int?
        public var appHost: String?

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

    struct ArchiveRequest: Encodable {
        let file: String
        let name: String?
        let php: String?
        let folder: String?
        let engine: String?
        let dryRun: Bool
    }

    /// Creates a site from a .zip/.tar.gz with its folder and a .sql dump,
    /// or with dryRun shows what Benbow found in it.
    public func importArchive(file: String, name: String?, php: String?, folder: String?, engine: String? = nil, dryRun: Bool) async throws -> ArchiveImportResult {
        try await call("POST", "/v1/import/archive", ArchiveRequest(file: file, name: name, php: php, folder: folder, engine: engine, dryRun: dryRun))
    }

    struct CloneRequest: Encodable {
        let name: String
        let php: String
        let folder: String?
        let sameFolder: Bool
    }

    /// Copies a site under a new name (folder, database, address), or with
    /// sameFolder serves the same folder under the new name.
    public func cloneSite(_ site: String, name: String, php: String, folder: String?, sameFolder: Bool) async throws -> CloneResult {
        try await call("POST", "/v1/sites/\(escape(site))/clone", CloneRequest(name: name, php: php, folder: folder, sameFolder: sameFolder))
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
        let title: String?
        let force: Bool
    }

    /// Changes a site; nil fields are left as they are. group 0 moves the
    /// site to the top level.
    public func updateSite(_ name: String, php: String? = nil, path: String? = nil, domain: String? = nil,
                           enabled: Bool? = nil, archived: Bool? = nil, aliases: [String]? = nil,
                           group: Int64? = nil, title: String? = nil, force: Bool = false) async throws -> SiteResult {
        try await call("PATCH", "/v1/sites/" + escape(name),
                       SiteChange(php: php, path: path, domain: domain, enabled: enabled, archived: archived,
                                  aliases: aliases, groupId: group, title: title, force: force))
    }

    struct AppChange: Encodable {
        let command: String?
        let port: Int?
        let proxyPort: Int?
        let appHost: String?
        let companionCommand: String?
        let companionPort: Int?
    }

    /// Changes an app's command, port, proxied port or Host header, or a PHP
    /// site's companion ("" removes it); nil fields are left as they are.
    public func updateApp(_ name: String, command: String? = nil, port: Int? = nil, proxyPort: Int? = nil,
                          appHost: String? = nil, companion: String? = nil, companionPort: Int? = nil) async throws -> SiteResult {
        try await call("PATCH", "/v1/sites/" + escape(name),
                       AppChange(command: command, port: port, proxyPort: proxyPort, appHost: appHost,
                                 companionCommand: companion, companionPort: companionPort))
    }

    /// The command Benbow proposes for a dev server's folder.
    public func detectApp(_ path: String) async throws -> AppDetection {
        try await call("GET", "/v1/detect?path=" + escape(path))
    }

    /// Stops an app's command (or a PHP site's companion) and starts it again.
    public func restartApp(_ name: String) async throws -> AppStatus {
        try await call("POST", "/v1/sites/\(escape(name))/restart")
    }

    /// An app's variables: names only.
    public func appEnv(_ name: String) async throws -> AppEnv {
        try await call("GET", "/v1/sites/\(escape(name))/env")
    }

    struct EnvChange: Encodable {
        let set: [String: String]
        let unset: [String]
    }

    /// Sets and removes an app's variables (kept in the login keychain).
    public func setAppEnv(_ name: String, set: [String: String] = [:], unset: [String] = []) async throws -> AppEnv {
        // Variable names go as they are (no snake_case conversion).
        let data = try JSONEncoder().encode(EnvChange(set: set, unset: unset))
        let resp = try await http.send(method: "PATCH", path: "/v1/sites/\(escape(name))/env", body: data)
        return try EnvelopeDecoder.decode(AppEnv.self, from: resp.body)
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
        /// A saved blueprint applied after the installation.
        public var blueprint: String?
        public var acceptChanges = false

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
        /// Engine of a new database: "mysql" or "pgsql".
        public var databaseEngine: String?
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

    struct ListOrderRequest: Encodable {
        let group: Int64
        let items: [String]
    }

    /// Orders one level of the site list (group 0: top level): items are
    /// "group:<id>" and "site:<name>", mixed. Items from elsewhere move
    /// there; the others of that level follow.
    public func setListOrder(group: Int64, items: [String]) async throws {
        let _: Ignored = try await call("PUT", "/v1/list-order", ListOrderRequest(group: group, items: items))
    }

    struct EnabledRequest: Encodable { let enabled: Bool }

    /// Turns every site in a group and its subgroups on or off (archived
    /// sites stay off).
    public func setGroupEnabled(_ id: Int64, _ enabled: Bool) async throws {
        let _: Ignored = try await call("POST", "/v1/groups/\(id)/enabled", EnabledRequest(enabled: enabled))
    }

    /// Removes a group; its sites and groups move up one level.
    public func removeGroup(_ id: Int64) async throws {
        let _: Ignored = try await call("DELETE", "/v1/groups/\(id)")
    }

    struct AdminerRequest: Encodable {
        let site: String
        let engine: String
    }
    struct AdminerLogin: Decodable { let url: String }

    /// A one-time URL that opens Adminer logged in, as root (site "") or as
    /// the site's database user.
    public func adminerLogin(site: String = "", engine: String = "") async throws -> URL {
        let r: AdminerLogin = try await call("POST", "/v1/adminer/login", AdminerRequest(site: site, engine: engine))
        guard let url = URL(string: r.url) else { throw APIError(code: "internal", message: "Bad Adminer URL") }
        return url
    }

    public func setMail(enabled: Bool) async throws {
        let _: Ignored = try await call("POST", enabled ? "/v1/mail/enable" : "/v1/mail/disable")
    }

    public func updates() async throws -> UpdatesStatus { try await call("GET", "/v1/updates") }

    public func checkUpdates() async throws -> UpdatesStatus { try await call("POST", "/v1/updates/check") }

    struct AutoRequest: Encodable { let on: Bool }

    /// Turns the daemon's daily update check on or off.
    public func setAutoUpdates(_ on: Bool) async throws -> UpdatesStatus {
        try await call("PUT", "/v1/updates/auto", AutoRequest(on: on))
    }

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
        let engine: String
    }

    /// Creates a database with its own user; an empty name means the site's.
    public func createSiteDatabase(_ name: String, database: String = "", engine: String = "mysql") async throws -> SiteDatabase {
        try await call("POST", "/v1/sites/\(escape(name))/database", DatabaseRequest(name: database, link: false, engine: engine))
    }

    public func linkSiteDatabase(_ name: String, database: String, engine: String = "mysql") async throws -> SiteDatabase {
        try await call("POST", "/v1/sites/\(escape(name))/database", DatabaseRequest(name: database, link: true, engine: engine))
    }

    public func postgresDatabases() async throws -> [String] { try await call("GET", "/v1/postgres/databases") }

    /// "postgres" or "redis": turns an optional server on (with a port) or off.
    public func setServer(_ server: String, enabled: Bool, port: Int? = nil) async throws {
        if enabled {
            let _: Ignored = try await call("POST", "/v1/\(server)/enable", PortRequest(port: port))
        } else {
            let _: Ignored = try await call("POST", "/v1/\(server)/disable")
        }
    }

    public func postgresCredentials() async throws -> MySQLCredentials {
        try await call("GET", "/v1/postgres/credentials")
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

    /// kind is "access", "php", "app" or "companion".
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

    public func removePHP(_ minor: String) async throws {
        let _: Ignored = try await call("DELETE", "/v1/php/\(escape(minor))")
    }

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

    /// The optional MySQL 5.7 server.
    public func enableMySQL57(port: Int? = nil) async throws {
        let _: Ignored = try await call("POST", "/v1/mysql57/enable", PortRequest(port: port))
    }

    public func disableMySQL57() async throws {
        let _: Ignored = try await call("POST", "/v1/mysql57/disable")
    }

    public func mysql57Credentials() async throws -> MySQLCredentials {
        try await call("GET", "/v1/mysql57/credentials")
    }

    public func mysql57Databases() async throws -> [String] {
        try await call("GET", "/v1/mysql57/databases")
    }

    struct ImportRequest: Encodable {
        let dryRun: Bool
        let localhost: Bool
    }

    public struct ImportOptions: Encodable, Sendable {
        public var dryRun = true
        public var localhost = false
        public var sites: [String] = []
        public var migrate = false
        /// use, recopy or skip.
        public var existing = "use"
        public var sourceUser: String?
        public var sourcePassword: String?
        public init() {}
    }

    /// Imports from mamp, local, herd, valet or xampp (dry run by default).
    public func importSites(from source: String, _ opts: ImportOptions) async throws -> ImportPlan {
        try await call("POST", "/v1/import/\(source)", opts)
    }

    public func importMAMP(dryRun: Bool, localhost: Bool) async throws -> ImportPlan {
        var o = ImportOptions()
        o.dryRun = dryRun
        o.localhost = localhost
        return try await importSites(from: "mamp", o)
    }

    public func undoMigration(_ site: String) async throws {
        let _: Ignored = try await call("POST", "/v1/sites/\(escape(site))/migration/undo")
    }

    func escape(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(CharacterSet(charactersIn: "-._"))) ?? s
    }
}

/// For calls whose answer the app does not need.
struct Ignored: Decodable {}
