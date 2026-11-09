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
        #expect(try await api.agentSessions().isEmpty)
        #expect(try await api.agentIdleHours() == 24)
        let tools = try await api.agentTools()
        #expect(tools.agents.contains { $0.id == "claude" })
        await #expect(throws: APIError(code: "not_found", message: #"site "nope" not found"#)) {
            try await api.removeSite("nope")
        }
        await #expect(throws: APIError(code: "not_found", message: #"site "nope" not found"#)) {
            try await api.updateSite("nope", php: "8.2")
        }
        #expect(try await api.xdebug().isEmpty)
        await #expect(throws: APIError(code: "not_found", message: #"site "nope" not found"#)) {
            try await api.siteLogs("nope", kind: "php")
        }
        await #expect(throws: APIError(code: "not_found", message: #"site "nope" not found"#)) {
            try await api.siteDatabase("nope")
        }
        await #expect(throws: APIError(code: "not_found", message: #"site "nope" not found"#)) {
            try await api.setSitePHPSettings("nope", ["memory_limit": "1G"])
        }
        let up = try await api.updates()
        #expect(up.updates.isEmpty && up.serial > 0)
        let ini = try await api.phpSettings("8.4")
        #expect(ini.memoryLimit == "512M" && ini.displayErrors)
        let clients = try await api.addGroup(name: "Clients", parent: nil)
        let acme = try await api.addGroup(name: "Acme", parent: clients.id)
        #expect(acme.path == "Clients/Acme" && acme.parentId == clients.id)
        let moved = try await api.updateGroup(acme.id, name: "Acme Corp", parent: 0)
        #expect(moved.path == "Acme Corp" && moved.parentId == nil)
        await #expect(throws: APIError(code: "invalid_input", message: "a group cannot go inside itself")) {
            try await api.updateGroup(clients.id, parent: clients.id)
        }
        try await api.removeGroup(clients.id)
        #expect(try await api.status().groups?.map(\.path) == ["Acme Corp"])
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

@Test func decodesGoDates() throws {
    let json = #"{"schema":1,"ok":true,"data":[{"id":"20261103-124501.123","site":"s","created_at":"2026-11-03T12:45:01.123456789+01:00","kind":"manual","bytes":0},{"id":"b","site":"s","created_at":"2026-11-03T11:45:01Z","kind":"manual","bytes":0}]}"#
    let list = try EnvelopeDecoder.decode([Snapshot].self, from: Data(json.utf8))
    #expect(list.count == 2)
    #expect(list[0].createdAt.timeIntervalSince(list[1].createdAt) < 1)
}

// Fixtures/wire is written by the Go test TestWireGolden (internal/api):
// every answer the app decodes, with every field filled in. A field renamed
// or retyped in Go fails here.
@Test func decodesWireFixtures() throws {
    func check<T: Decodable>(_ name: String, _ type: T.Type) throws {
        let url = try #require(Bundle.module.url(forResource: name, withExtension: "json", subdirectory: "Fixtures/wire"), "missing Fixtures/wire/\(name).json")
        do {
            _ = try EnvelopeDecoder.decode(type, from: Data(contentsOf: url))
        } catch {
            Issue.record("wire/\(name).json does not decode as \(T.self): \(error)")
        }
    }
    let decoders: [String: () throws -> Void] = [
        "status": { try check("status", Status.self) },
        "site-result": { try check("site-result", SiteResult.self) },
        "site-database": { try check("site-database", SiteDatabase.self) },
        "wordpress-result": { try check("wordpress-result", WordPressResult.self) },
        "laravel-result": { try check("laravel-result", LaravelResult.self) },
        "tool-result": { try check("tool-result", ToolResult.self) },
        "site-php-settings": { try check("site-php-settings", SitePHPSettings.self) },
        "site-log": { try check("site-log", SiteLog.self) },
        "updates": { try check("updates", UpdatesStatus.self) },
        "php-list": { try check("php-list", PHPList.self) },
        "php-settings": { try check("php-settings", PHPSettings.self) },
        "xdebug": { try check("xdebug", [XdebugStatus].self) },
        "mysql-credentials": { try check("mysql-credentials", MySQLCredentials.self) },
        "import-plan": { try check("import-plan", ImportPlan.self) },
        "group": { try check("group", SiteGroup.self) },
        "php-check": { try check("php-check", PHPCheckResult.self) },
        "ports": { try check("ports", PortsStatus.self) },
        "mcp-permissions": { try check("mcp-permissions", MCPPermissions.self) },
        "mcp-actions": { try check("mcp-actions", [MCPAction].self) },
        "blueprints": { try check("blueprints", [BlueprintInfo].self) },
        "blueprint-result": { try check("blueprint-result", BlueprintResult.self) },
        "remotes": { try check("remotes", [Remote].self) },
        "sync-result": { try check("sync-result", SyncResult.self) },
        "snapshots": { try check("snapshots", [Snapshot].self) },
        "restore-result": { try check("restore-result", RestoreResult.self) },
        "agent-sessions": { try check("agent-sessions", [AgentSession].self) },
        "close-session": { try check("close-session", CloseSessionResult.self) },
        "session-diff": { try check("session-diff", SessionDiff.self) },
        "agent-tools": { try check("agent-tools", AgentTools.self) },
        "agent-connect": { try check("agent-connect", AgentConnectResult.self) },
        "agent-start": { try check("agent-start", AgentStartResult.self) },
        "phone": { try check("phone", PhoneStatus.self) },
        "app-detection": { try check("app-detection", AppDetection.self) },
        "app-status": { try check("app-status", AppStatus.self) },
    ]
    let dir = try #require(Bundle.module.url(forResource: "wire", withExtension: nil, subdirectory: "Fixtures"))
    let files = try FileManager.default.contentsOfDirectory(atPath: dir.path).filter { $0.hasSuffix(".json") }
    #expect(!files.isEmpty)
    for file in files.sorted() {
        let name = String(file.dropLast(".json".count))
        guard let decode = decoders[name] else {
            Issue.record("wire/\(file) has no Swift type in decodesWireFixtures")
            continue
        }
        try decode()
    }
}
