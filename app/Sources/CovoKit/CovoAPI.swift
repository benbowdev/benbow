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

    public func start() async throws {
        let _: Status = try await call("POST", "/v1/start")
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

    public func removeSite(_ name: String) async throws {
        let _: Ignored = try await call("DELETE", "/v1/sites/" + escape(name))
    }

    public func xdebug() async throws -> [XdebugStatus] { try await call("GET", "/v1/xdebug") }

    struct Toggle: Encodable { let enabled: Bool }

    public func setXdebug(php: String, enabled: Bool) async throws {
        let _: Ignored = try await call("PUT", "/v1/xdebug/" + escape(php), Toggle(enabled: enabled))
    }

    public func enableDomain(_ zone: String) async throws {
        let _: Ignored = try await call("POST", "/v1/domains/\(escape(zone))/enable")
    }

    public func trust() async throws {
        let _: Ignored = try await call("POST", "/v1/trust")
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
