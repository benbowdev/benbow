// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import Foundation
import Observation

/// The app's state: a thin mirror of the core, refreshed over the local API.
/// Every change goes through the core; nothing is decided here.
@Observable @MainActor
final class AppModel {
    enum Phase: Equatable {
        case connecting
        case ready
        case failed(String)
    }

    private(set) var phase: Phase = .connecting
    private(set) var status: Status?
    private(set) var php: PHPList?
    private(set) var xdebug: [XdebugStatus] = []
    /// Set while a change is in progress (disables the controls).
    private(set) var busy: String?
    var error: String?

    private var api: CovoAPI?
    private var polling: Task<Void, Never>?

    var sites: [Site] { status?.sites ?? [] }
    var running: Bool { status?.running ?? false }

    func connect() async {
        phase = .connecting
        guard let bin = Daemon.binary(), FileManager.default.isExecutableFile(atPath: bin.path) else {
            phase = .failed("The covo binary is missing from the app. Rebuild it with app/build.sh.")
            return
        }
        do {
            let info = try await Daemon.start(binary: bin)
            api = CovoAPI(socketPath: info.socket)
            try await refreshAll()
            phase = .ready
            startPolling()
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    private func startPolling() {
        polling?.cancel()
        polling = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(2))
                guard let self else { return }
                if self.busy != nil { continue }
                await self.refreshStatus()
            }
        }
    }

    private func refreshStatus() async {
        guard let api else { return }
        do {
            status = try await api.status()
        } catch is APIError {
            // The core answered: nothing to recover.
        } catch {
            // The daemon went away (covo uninstall, a crash): start it again.
            await connect()
        }
    }

    private func refreshAll() async throws {
        guard let api else { return }
        status = try await api.status()
        php = try await api.php()
        xdebug = try await api.xdebug()
    }

    /// Runs a change, then refreshes. Errors are shown in an alert.
    @discardableResult
    func perform(_ what: String, _ action: @escaping (CovoAPI) async throws -> Void) async -> Bool {
        guard let api else { return false }
        busy = what
        defer { busy = nil }
        do {
            try await action(api)
            try? await refreshAll()
            return true
        } catch {
            try? await refreshAll()
            self.error = error.localizedDescription
            return false
        }
    }

    func toggleRunning() async {
        if running {
            await perform("Stopping…") { try await $0.stop() }
        } else {
            await perform("Starting…") { try await $0.start() }
        }
    }

    /// Adds a site. Returns the error code instead of showing it when the
    /// folder has no index file, so the caller can ask before forcing it.
    func addSite(name: String, php: String, path: String, force: Bool) async throws {
        guard let api else { return }
        busy = "Adding \(name)…"
        defer { busy = nil }
        _ = try await api.addSite(name: name, php: php, path: path, force: force)
        try? await refreshAll()
    }

    /// Changes a site. Throws so the caller can ask before forcing a folder
    /// without an index file; other errors are thrown as well.
    func updateSite(_ site: Site, php: String? = nil, path: String? = nil, domain: String? = nil, force: Bool = false) async throws {
        guard let api else { return }
        if let php {
            busy = installed(php) ? "Switching to PHP \(php)…" : "Downloading PHP \(php)…"
        } else {
            busy = "Updating \(site.name)…"
        }
        defer { busy = nil }
        do {
            _ = try await api.updateSite(site.name, php: php, path: path, domain: domain, force: force)
            try? await refreshAll()
        } catch {
            try? await refreshAll()
            throw error
        }
    }

    /// Runs updateSite and shows any error in the alert.
    func updateSiteShowingErrors(_ site: Site, php: String? = nil, domain: String? = nil) async {
        do {
            try await updateSite(site, php: php, domain: domain)
        } catch {
            self.error = error.localizedDescription
        }
    }

    func setEnabled(_ site: Site, _ on: Bool) async {
        await perform(on ? "Turning \(site.name) on…" : "Turning \(site.name) off…") {
            _ = try await $0.updateSite(site.name, enabled: on)
        }
    }

    func setAliases(_ site: Site, _ aliases: [String]) async {
        await perform("Updating \(site.name)…") {
            _ = try await $0.updateSite(site.name, aliases: aliases)
        }
    }

    func installed(_ minor: String) -> Bool {
        php?.installed.contains { $0.minor == minor } ?? false
    }

    func installPHP(_ minor: String) async {
        await perform("Downloading PHP \(minor)…") { try await $0.installPHP(minor) }
    }

    func phpSettings(_ minor: String) async -> PHPSettings? {
        guard let api else { return nil }
        do {
            return try await api.phpSettings(minor)
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    /// Saves php.ini values; PHP restarts with them if Covo is running.
    func setPHPSettings(_ s: PHPSettings) async -> Bool {
        await perform("Restarting PHP \(s.php)…") { _ = try await $0.setPHPSettings(s) }
    }

    func setHTTPSPort(_ port: Int) async {
        await perform("Changing the HTTPS port…") { try await $0.start(httpsPort: port) }
    }

    /// Turns a domain suffix on or off. Both need an administrator password.
    func setDomain(_ zone: String, enabled: Bool) async {
        await perform("Waiting for macOS…") { api in
            if enabled { try await api.enableDomain(zone) } else { try await api.disableDomain(zone) }
        }
    }

    func domainEnabled(_ zone: String) -> Bool {
        status?.domains.first { $0.zone == zone }?.enabled ?? false
    }

    func setMySQL(enabled: Bool, port: Int? = nil) async {
        await perform(enabled ? "Starting MySQL…" : "Stopping MySQL…") { api in
            if enabled { try await api.enableMySQL(port: port) } else { try await api.disableMySQL() }
        }
    }

    func mysqlCredentials() async -> MySQLCredentials? {
        guard let api else { return nil }
        do {
            return try await api.mysqlCredentials()
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    func untrust() async {
        await perform("Removing trust…") { try await $0.untrust() }
    }

    func removeSite(_ site: Site) async {
        await perform("Removing \(site.name)…") { try await $0.removeSite(site.name) }
    }

    func setXdebug(php: String, enabled: Bool) async {
        await perform(enabled ? "Turning Xdebug on…" : "Turning Xdebug off…") {
            try await $0.setXdebug(php: php, enabled: enabled)
        }
    }

    func trust() async {
        await perform("Waiting for macOS…") { try await $0.trust() }
    }

    func xdebug(for php: String) -> XdebugStatus? {
        xdebug.first { $0.php == php }
    }

    /// A valid site name from a folder name: "My Shop" → "my-shop".
    static func siteName(from folder: String) -> String {
        var out = ""
        for ch in folder.lowercased() {
            if ch.isASCII, ch.isLetter || ch.isNumber {
                out.append(ch)
            } else if !out.hasSuffix("-"), !out.isEmpty {
                out.append("-")
            }
        }
        while out.hasSuffix("-") { out.removeLast() }
        return String(out.prefix(63))
    }
}
