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

    struct StartRequest: Encodable { let httpsPort: Int? }

    /// Starts Covo; a non-nil port also changes the HTTPS port.
    public func start(httpsPort: Int? = nil) async throws {
        let _: Ignored = try await call("POST", "/v1/start", StartRequest(httpsPort: httpsPort))
    }

    public func stop() async throws {
        let _: Ignored = try await call("POST", "/v1/stop")
    }

    public func php() async throws -> PHPList { try await call("GET", "/v1/php") }

    struct NewSite: Encodable {
        let name: String
        let domain: String?
        let php: String
        let path: String
        let force: Bool
    }

    public func addSite(name: String, domain: String? = nil, php: String, path: String, force: Bool = false) async throws -> SiteResult {
        try await call("POST", "/v1/sites", NewSite(name: name, domain: domain, php: php, path: path, force: force))
    }

    struct SiteChange: Encodable {
        let php: String?
        let path: String?
        let domain: String?
        let enabled: Bool?
        let aliases: [String]?
        let force: Bool
    }

    /// Changes a site; nil fields are left as they are.
    public func updateSite(_ name: String, php: String? = nil, path: String? = nil, domain: String? = nil,
                           enabled: Bool? = nil, aliases: [String]? = nil, force: Bool = false) async throws -> SiteResult {
        try await call("PATCH", "/v1/sites/" + escape(name),
                       SiteChange(php: php, path: path, domain: domain, enabled: enabled, aliases: aliases, force: force))
    }

    struct WordPressRequest: Encodable {
        let name: String
        let path: String
        let php: String
        let title: String
        let adminUser: String
        let adminEmail: String
    }

    public func newWordPress(name: String, path: String, php: String, title: String,
                             adminUser: String, adminEmail: String) async throws -> WordPressResult {
        try await call("POST", "/v1/wordpress", WordPressRequest(
            name: name, path: path, php: php, title: title, adminUser: adminUser, adminEmail: adminEmail))
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

    /// kind is "access" or "php".
    public func siteLogs(_ name: String, kind: String, lines: Int = 500) async throws -> SiteLog {
        try await call("GET", "/v1/sites/\(escape(name))/logs/\(escape(kind))?lines=\(lines)")
    }

    public func clearSiteLogs(_ name: String) async throws {
        let _: Ignored = try await call("DELETE", "/v1/sites/\(escape(name))/logs")
    }

    public func removeSite(_ name: String) async throws {
        let _: Ignored = try await call("DELETE", "/v1/sites/" + escape(name))
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
