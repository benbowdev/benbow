// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import Foundation
import Testing
@testable import CovoKit

func fixture(_ name: String) throws -> Data {
    let url = try #require(Bundle.module.url(forResource: name, withExtension: "json", subdirectory: "Fixtures"))
    return try Data(contentsOf: url)
}

// Fixtures are real `covo --json` output (home path anonymised): the API
// returns the same envelope and data.
@Test func decodesStatus() throws {
    let st = try EnvelopeDecoder.decode(Status.self, from: fixture("status"))
    #expect(st.running)
    #expect(st.httpsPort == 8443)
    #expect(st.sites.map(\.name) == ["demo", "demo2"])
    #expect(st.sites[1].domain == "demo.covo.test")
    #expect(st.sites[0].url == "https://demo.localhost:8443")
    #expect(st.ca.trusted)
    #expect(st.services.first?.name == "caddy")
    #expect(st.domains.contains { $0.zone == "test" && $0.enabled && $0.dnsPort == 53535 })
    #expect(st.mysql.enabled && st.mysql.port == 3307)
}

@Test func decodesPHPAndXdebug() throws {
    let php = try EnvelopeDecoder.decode(PHPList.self, from: fixture("php-list"))
    #expect(php.installed.first?.minor == "8.4")
    #expect(php.minors.first == "8.4")
    #expect(php.minors == php.minors.sorted { $0.compare($1, options: .numeric) == .orderedDescending })
    let x = try EnvelopeDecoder.decode([XdebugStatus].self, from: fixture("php-xdebug"))
    #expect(x.first?.php == "8.4")
}

@Test func decodesSitesAndDomains() throws {
    let sites = try EnvelopeDecoder.decode([Site].self, from: fixture("site-list"))
    #expect(sites.count == 2)
    let domains = try EnvelopeDecoder.decode([Domain].self, from: fixture("domains"))
    #expect(domains.map(\.zone) == ["test", "local"])
    #expect(domains[1].helper != nil)
}

@Test func decodesImportPlan() throws {
    let plan = try EnvelopeDecoder.decode(ImportPlan.self, from: fixture("import-mamp"))
    #expect(plan.dryRun)
    #expect(plan.needsLocal)
    #expect(plan.items.count == plan.create + plan.skip)
    let created = try #require(plan.items.first { $0.creates })
    #expect(created.domain == "blog.client.local")
    #expect(created.mamp.databases == ["blog", "blog_old"])
    #expect(plan.items.first?.reason == "MAMP's default host")
}

@Test func decodesErrors() throws {
    #expect(throws: APIError(code: "not_found", message: #"folder "/nonexistent" does not exist"#)) {
        try EnvelopeDecoder.decode(SiteResult.self, from: fixture("error"))
    }
}

@Test func parsesHTTPResponses() throws {
    let raw = Data("HTTP/1.0 409 Conflict\r\nContent-Type: application/json\r\n\r\n{\"a\":1}".utf8)
    let r = try UnixHTTP.parse(raw)
    #expect(r.status == 409)
    #expect(String(data: r.body, encoding: .utf8) == "{\"a\":1}")
    #expect(throws: UnixHTTP.Failure.malformed) { try UnixHTTP.parse(Data("garbage".utf8)) }
}

@Test func binaryLookup() {
    #expect(Daemon.binary(environment: ["COVO_BIN": "/x/covo"])?.path == "/x/covo")
}

/// Talks to a real daemon in an isolated Covo home. Runs when COVO_BIN
/// points at a covo binary (build.sh test does that).
@Test(.enabled(if: ProcessInfo.processInfo.environment["COVO_BIN"] != nil))
func liveDaemon() async throws {
    let bin = try #require(Daemon.binary())
    let home = FileManager.default.temporaryDirectory.appendingPathComponent("covo-app-test-\(UUID().uuidString)")
    var env = ProcessInfo.processInfo.environment
    let tag = UUID().uuidString.prefix(8).lowercased()
    env["COVO_HOME"] = home.path
    env["COVO_LABEL_PREFIX"] = "dev.getcovo.test-\(tag)"
    env["COVO_CA_NAME"] = "Covo Test CA \(tag)"
    env["COVO_RESOLVER_DIR"] = home.appendingPathComponent("resolver").path
    let info = try await Daemon.start(binary: bin, environment: env)
    let api = CovoAPI(socketPath: info.socket)
    let checks: () async throws -> Void = {
        #expect(info.answering)
        let st = try await api.status()
        #expect(st.home == home.path)
        #expect(st.sites.isEmpty)
        let php = try await api.php()
        #expect(php.minors.contains("8.4"))
        await #expect(throws: APIError(code: "not_found", message: #"site "nope" not found"#)) {
            try await api.removeSite("nope")
        }
        await #expect(throws: APIError(code: "not_found", message: #"site "nope" not found"#)) {
            try await api.updateSite("nope", php: "8.2")
        }
        #expect(try await api.xdebug().isEmpty)
        let ini = try await api.phpSettings("8.4")
        #expect(ini.memoryLimit == "512M" && ini.displayErrors)
    }
    let result = await Result { try await checks() }
    // Always: stops the daemon and deletes the test home.
    _ = try await Daemon.run(bin, ["uninstall", "--yes", "--json"], environment: env)
    try result.get()
}

extension Result where Failure == Error {
    init(catching body: () async throws -> Success) async {
        do { self = .success(try await body()) } catch { self = .failure(error) }
    }
}
