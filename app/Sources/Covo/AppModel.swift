// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
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
    private(set) var updates: UpdatesStatus?
    private(set) var ports: PortsStatus?
    /// Open AI agent sessions, one per site at most.
    private(set) var sessions: [AgentSession] = []
    /// Set while a change is in progress (disables the controls).
    private(set) var busy: String?
    /// Set by File → Import from MAMP PRO… to show the import sheet.
    var importing = false
    var error: String?

    private var api: CovoAPI?
    private var polling: Task<Void, Never>?
    /// Start-on-launch runs once per app launch, not on reconnects.
    private var launched = false
    private var ticks = 0

    /// UserDefaults key: start every site when the app opens.
    static let startOnLaunchKey = "startSitesOnLaunch"

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
            if !launched {
                launched = true
                if UserDefaults.standard.bool(forKey: Self.startOnLaunchKey), !running {
                    await perform("Starting…") { try await $0.start() }
                }
            }
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
            sessions = (try? await api.agentSessions()) ?? sessions
            // The daemon checks for updates on its own: pick them up now and then.
            ticks += 1
            if ticks % 150 == 0 { updates = try? await api.updates() }
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
        updates = try? await api.updates()
        ports = try? await api.ports()
        sessions = (try? await api.agentSessions()) ?? []
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

    /// Adds a site. Throws so the caller can ask before forcing a folder
    /// without an index file, and show other errors in place.
    func addSite(name: String, php: String, path: String, force: Bool) async throws {
        var s = CovoAPI.NewSite(name: name, php: php, path: path)
        s.force = force
        try await addSite(s)
    }

    func addSite(_ site: CovoAPI.NewSite) async throws {
        guard let api else { return }
        busy = installed(site.php) ? "Adding \(site.name)…" : "Downloading PHP \(site.php)…"
        defer { busy = nil }
        do {
            _ = try await api.addSite(site)
            try? await refreshAll()
        } catch {
            try? await refreshAll()
            throw error
        }
    }

    var groups: [SiteGroup] { status?.groups ?? [] }

    /// Creates a group and returns it (nil on error, shown in the alert).
    @discardableResult
    func addGroup(_ name: String, parent: Int64?) async -> SiteGroup? {
        var made: SiteGroup?
        await perform("Creating \(name)…") { made = try await $0.addGroup(name: name, parent: parent) }
        return made
    }

    func renameGroup(_ group: SiteGroup, to name: String) async {
        await perform("Renaming…") { _ = try await $0.updateGroup(group.id, name: name) }
    }

    /// Moves a group into another one (nil: top level).
    func moveGroup(_ group: SiteGroup, into parent: Int64?) async {
        guard group.parentId != parent, group.id != parent else { return }
        await perform("Moving \(group.name)…") { _ = try await $0.updateGroup(group.id, parent: parent ?? 0) }
    }

    /// Orders one level of the site list (parent nil: top level).
    func setListOrder(parent: Int64?, items: [String]) async {
        await perform("Ordering…") { try await $0.setListOrder(group: parent ?? 0, items: items) }
    }

    func removeGroup(_ group: SiteGroup) async {
        await perform("Removing \(group.name)…") { try await $0.removeGroup(group.id) }
    }

    /// Moves a site into a group (nil: top level).
    func moveSite(_ site: Site, into group: Int64?) async {
        guard site.groupId != group else { return }
        await perform("Moving \(site.name)…") { _ = try await $0.updateSite(site.name, group: group ?? 0) }
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

    func setGroupEnabled(_ group: SiteGroup, _ on: Bool) async {
        await perform(on ? "Turning \(group.name) on…" : "Turning \(group.name) off…") {
            try await $0.setGroupEnabled(group.id, on)
        }
    }

    func setArchived(_ site: Site, _ archived: Bool) async {
        await perform(archived ? "Archiving \(site.name)…" : "Bringing back \(site.name)…") {
            _ = try await $0.updateSite(site.name, archived: archived)
        }
    }

    func setAliases(_ site: Site, _ aliases: [String]) async {
        await perform("Updating \(site.name)…") {
            _ = try await $0.updateSite(site.name, aliases: aliases)
        }
    }

    /// Reads a log without the busy state: the log window polls it.
    func siteLogs(_ name: String, kind: String) async throws -> SiteLog? {
        guard let api else { return nil }
        return try await api.siteLogs(name, kind: kind)
    }

    func clearSiteLogs(_ name: String) async {
        await perform("Clearing logs…") { try await $0.clearSiteLogs(name) }
    }

    var mysqlReady: Bool { status?.mysql.enabled == true && status?.mysql.running == true }
    var postgresReady: Bool { status?.postgres?.enabled == true && status?.postgres?.running == true }

    /// Turns PostgreSQL or Redis on or off (server: "postgres" or "redis").
    func setServer(_ server: String, enabled: Bool, port: Int? = nil) async {
        let name = server == "postgres" ? "PostgreSQL" : "Redis"
        await perform(enabled ? "Starting \(name)…" : "Stopping \(name)…") {
            try await $0.setServer(server, enabled: enabled, port: port)
        }
    }

    func postgresCredentials() async -> MySQLCredentials? {
        guard let api else { return nil }
        do {
            return try await api.postgresCredentials()
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    func postgresDatabases() async -> [String] {
        guard let api else { return [] }
        return (try? await api.postgresDatabases()) ?? []
    }

    func databases() async -> [String] {
        guard let api else { return [] }
        do {
            return try await api.databases()
        } catch {
            self.error = error.localizedDescription
            return []
        }
    }

    func siteDatabase(_ site: Site) async -> SiteDatabase? {
        guard let api, !site.database.isEmpty else { return nil }
        do {
            return try await api.siteDatabase(site.name)
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    func createDatabase(_ site: Site, name: String = "", engine: String = "mysql") async {
        await perform("Creating the database…") { _ = try await $0.createSiteDatabase(site.name, database: name, engine: engine) }
    }

    func linkDatabase(_ site: Site, _ db: String, engine: String = "mysql") async {
        await perform("Linking \(db)…") { _ = try await $0.linkSiteDatabase(site.name, database: db, engine: engine) }
    }

    func unlinkDatabase(_ site: Site, drop: Bool) async {
        await perform(drop ? "Deleting \(site.database)…" : "Unlinking…") {
            try await $0.unlinkSiteDatabase(site.name, drop: drop)
        }
    }

    /// Opens Adminer already logged in: as root (every database) with nil,
    /// or as the site's database user. The password never leaves Covo.
    func openAdminer(_ site: Site? = nil, engine: String = "") async {
        guard let api else { return }
        do {
            NSWorkspace.shared.open(try await api.adminerLogin(site: site?.name ?? "", engine: engine))
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Creates a Laravel project. Throws so the sheet can show the error.
    func newLaravel(_ req: CovoAPI.NewLaravel) async throws -> LaravelResult? {
        guard let api else { return nil }
        busy = "Creating the Laravel project…"
        defer { busy = nil }
        do {
            let r = try await api.newLaravel(req)
            try? await refreshAll()
            return r
        } catch {
            try? await refreshAll()
            throw error
        }
    }

    /// Runs wp-cli, Composer or Artisan; throws so the sheet shows errors.
    func runTool(_ site: Site, tool: String, args: [String]) async throws -> ToolResult? {
        guard let api else { return nil }
        return try await api.runTool(site.name, tool: tool, args: args)
    }

    /// Creates a WordPress site. Throws so the sheet can show the error.
    func newWordPress(_ req: CovoAPI.NewWordPress) async throws -> WordPressResult? {
        guard let api else { return nil }
        busy = "Installing WordPress…"
        defer { busy = nil }
        do {
            let r = try await api.newWordPress(req)
            try? await refreshAll()
            return r
        } catch {
            try? await refreshAll()
            throw error
        }
    }

    func sitePHPSettings(_ site: Site) async -> SitePHPSettings? {
        guard let api else { return nil }
        do {
            return try await api.sitePHPSettings(site.name)
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    func setSitePHPSettings(_ site: Site, _ values: [String: String]) async -> Bool {
        await perform("Applying PHP settings…") { _ = try await $0.setSitePHPSettings(site.name, values) }
    }

    func setMail(_ on: Bool) async {
        await perform(on ? "Starting Mailpit…" : "Stopping Mailpit…") { try await $0.setMail(enabled: on) }
    }

    /// Previews or runs the MAMP PRO import. Throws so the sheet shows errors.
    /// Previews or runs an import from any source. Throws for the sheet.
    func importSites(from source: String, _ opts: CovoAPI.ImportOptions) async throws -> ImportPlan? {
        guard let api else { return nil }
        if opts.dryRun { return try await api.importSites(from: source, opts) }
        busy = opts.migrate ? "Importing and migrating…" : "Importing…"
        defer { busy = nil }
        do {
            let plan = try await api.importSites(from: source, opts)
            try? await refreshAll()
            return plan
        } catch {
            try? await refreshAll()
            throw error
        }
    }

    func undoMigration(_ site: Site) async {
        await perform("Undoing the migration of \(site.name)…") { try await $0.undoMigration(site.name) }
    }

    func importMAMP(dryRun: Bool, localhost: Bool) async throws -> ImportPlan? {
        guard let api else { return nil }
        if dryRun { return try await api.importMAMP(dryRun: true, localhost: localhost) }
        busy = "Importing from MAMP PRO…"
        defer { busy = nil }
        do {
            let plan = try await api.importMAMP(dryRun: false, localhost: localhost)
            try? await refreshAll()
            return plan
        } catch {
            try? await refreshAll()
            throw error
        }
    }

    /// Turns on .local names; macOS asks for an administrator password.
    func enableLocal() async throws {
        guard let api else { return }
        busy = "Waiting for macOS…"
        defer { busy = nil }
        try await api.enableDomain("local")
        try? await refreshAll()
    }

    func checkUpdates() async {
        await perform("Checking for updates…") { [weak self] api in
            let st = try await api.checkUpdates()
            await MainActor.run { self?.updates = st }
        }
    }

    /// Installs updates (all ready ones when empty).
    func applyUpdates(_ components: [String] = []) async {
        await perform("Updating…") { _ = try await $0.applyUpdates(components) }
    }

    func phpSupport(_ minor: String) -> PHPSupport? { php?.support(for: minor) }

    /// The newest PHP version php.net fully supports.
    var recommendedPHP: String? {
        php?.minors.first { phpSupport($0)?.status == "active" }
    }

    /// Runs the compatibility check; throws so the sheet shows errors.
    func checkPHP(_ site: Site, target: String, all: Bool) async throws -> PHPCheckResult? {
        guard let api else { return nil }
        return try await api.checkPHP(site.name, target: target, all: all)
    }

    /// A PHP version for pickers: "8.1 · unsupported (download)".
    func phpLabel(_ minor: String, prefix: String = "") -> String {
        var s = prefix + minor
        if phpSupport(minor)?.isEOL == true { s += " · unsupported" }
        if !installed(minor) { s += " (download)" }
        return s
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

    func blueprints() async -> [BlueprintInfo] { (try? await api?.blueprints()) ?? [] }

    func blueprintJSON(_ name: String) async throws -> Data {
        guard let api else { throw APIError(code: "unavailable", message: "Covo's core is not running.") }
        return try await api.blueprintJSON(name)
    }

    func importBlueprint(_ json: Data, name: String, overwrite: Bool) async throws -> BlueprintInfo {
        guard let api else { throw APIError(code: "unavailable", message: "Covo's core is not running.") }
        return try await api.importBlueprint(json, name: name, overwrite: overwrite)
    }

    func deleteBlueprint(_ name: String) async {
        await perform("Deleting…") { try await $0.deleteBlueprint(name) }
    }

    func saveBlueprint(_ site: Site, name: String, overwrite: Bool) async throws -> SavedBlueprint {
        guard let api else { throw APIError(code: "unavailable", message: "Covo's core is not running.") }
        busy = "Reading \(site.name)…"
        defer { busy = nil }
        return try await api.saveBlueprint(site: site.name, name: name, overwrite: overwrite)
    }

    /// Throws so the sheet can offer to accept changed downloads.
    func applyBlueprint(_ name: String, to site: Site, dryRun: Bool, acceptChanges: Bool = false) async throws -> BlueprintResult {
        guard let api else { throw APIError(code: "unavailable", message: "Covo's core is not running.") }
        busy = dryRun ? "Checking…" : "Applying \(name)…"
        defer { busy = nil }
        let r = try await api.applyBlueprint(name, site: site.name, dryRun: dryRun, acceptChanges: acceptChanges)
        if !dryRun { try? await refreshAll() }
        return r
    }

    func hasGitHubToken() async -> Bool { (try? await api?.hasGitHubToken()) ?? false }

    func setGitHubToken(_ token: String) async -> Bool {
        var stored = false
        await perform("Saving…") { stored = try await $0.setGitHubToken(token) }
        return stored
    }

    func mcpPermissions() async -> MCPPermissions? { try? await api?.mcpPermissions() }

    func setMCPPermission(_ scope: String, write: Bool) async -> MCPPermissions? {
        var p: MCPPermissions?
        await perform("Saving…") { p = try await $0.setMCPPermission(scope, write: write) }
        return p
    }

    func mcpActions() async -> [MCPAction] { (try? await api?.mcpActions()) ?? [] }

    /// The site's open agent session, if any.
    func session(for site: Site) -> AgentSession? { sessions.first { $0.site == site.name } }

    func sessionDiff(_ site: Site) async -> SessionDiff? { try? await api?.agentSessionDiff(site.name) }

    /// Keeps an agent session's changes, or undoes them all.
    func closeSession(_ site: Site, keep: Bool) async {
        await perform(keep ? "Keeping the changes…" : "Undoing the agent's session…") {
            _ = try await $0.closeAgentSession(site.name, keep: keep)
        }
    }

    func agentIdleHours() async -> Int? { try? await api?.agentIdleHours() }

    func setAgentIdleHours(_ h: Int) async {
        await perform("Saving…") { try await $0.setAgentIdleHours(h) }
    }

    func agentTools() async -> AgentTools? { try? await api?.agentTools() }

    /// Adds Covo to an agent's own settings (after a backup); nil on error.
    func connectAgent(_ id: String) async -> AgentConnectResult? {
        var r: AgentConnectResult?
        await perform("Connecting…") { r = try await $0.connectAgent(id) }
        return r
    }

    enum AgentStart {
        case started(AgentStartResult)
        /// The agent needs Covo in its own settings first (message says where).
        case needsConnect(String)
        case failed
    }

    /// "Work with an agent": session, temporary permission, agent started.
    func startAgent(_ site: Site, tool: String, terminal: String?, connect: Bool) async -> AgentStart {
        guard let api else { return .failed }
        busy = "Saving \(site.name) and starting the agent…"
        defer { busy = nil }
        do {
            let r = try await api.startAgent(site: site.name, tool: tool, terminal: terminal, connect: connect)
            try? await refreshAll()
            return .started(r)
        } catch let e as APIError where e.code == "confirmation_required" {
            return .needsConnect(e.message)
        } catch {
            self.error = error.localizedDescription
            try? await refreshAll()
            return .failed
        }
    }

    func remotes(_ site: Site) async -> [Remote] {
        guard let api else { return [] }
        return (try? await api.remotes(site.name)) ?? []
    }

    func setRemote(_ r: Remote, password: String?) async -> Bool {
        await perform("Saving \(r.name)…") { _ = try await $0.setRemote(r, password: password) }
    }

    func removeRemote(_ site: Site, _ r: Remote) async {
        await perform("Removing \(r.name)…") { try await $0.removeRemote(site.name, name: r.name) }
    }

    /// Pull or push; throws so the sheet shows the error in place.
    func sync(_ op: String, _ site: Site, _ r: Remote, dryRun: Bool, files: Bool, database: Bool) async throws -> SyncResult? {
        guard let api else { return nil }
        if dryRun { return try await api.sync(op, site: site.name, remote: r.name, dryRun: true, files: files, database: database) }
        busy = op == "pull" ? "Pulling from \(r.name)…" : "Pushing to \(r.name)…"
        defer { busy = nil }
        do {
            let res = try await api.sync(op, site: site.name, remote: r.name, dryRun: false, files: files, database: database)
            try? await refreshAll()
            return res
        } catch {
            try? await refreshAll()
            throw error
        }
    }

    func snapshots(_ site: Site) async -> [Snapshot] {
        guard let api else { return [] }
        return (try? await api.snapshots(site.name)) ?? []
    }

    func createSnapshot(_ site: Site, note: String) async {
        await perform("Taking a snapshot…") { _ = try await $0.createSnapshot(site.name, note: note) }
    }

    func restoreSnapshot(_ site: Site, _ snap: Snapshot) async {
        await perform("Restoring \(site.name)…") { _ = try await $0.restoreSnapshot(site.name, id: snap.id) }
    }

    func deleteSnapshot(_ site: Site, _ snap: Snapshot) async {
        await perform("Deleting the snapshot…") { try await $0.deleteSnapshot(site.name, id: snap.id) }
    }

    func snapshotKeep() async -> Int { (try? await api?.snapshotKeep()) ?? 10 }

    func setSnapshotKeep(_ n: Int) async {
        await perform("Saving…") { try await $0.setSnapshotKeep(n) }
    }

    func sharePhone(_ site: Site, minutes: Int) async {
        await perform("Opening \(site.name) on the network…") { _ = try await $0.sharePhone(site.name, minutes: minutes) }
    }

    func stopPhone() async {
        await perform("Closing the phone access…") { try await $0.stopPhone() }
    }

    /// Changes the web ports; 443 or 80 make macOS ask for a password once.
    func setPorts(https: Int, http: Int) async {
        let standard = https < 1024 || (http != 0 && http < 1024)
        await perform(standard ? "Waiting for macOS…" : "Changing ports…") { [weak self] api in
            let p = try await api.setPorts(https: https, http: http)
            await MainActor.run { self?.ports = p }
        }
    }

    func loadPorts() async {
        guard let api else { return }
        ports = try? await api.ports()
    }

    /// The address of a domain with the current HTTPS port (none for 443).
    func address(_ domain: String) -> String {
        let port = status?.httpsPort ?? 8443
        return port == 443 ? "https://\(domain)" : "https://\(domain):\(port)"
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

    /// Set to ask for confirmation before removing a site (from any view).
    var removing: Site?

    func removeSite(_ site: Site, dropDatabase: Bool = false) async {
        await perform("Removing \(site.name)…") { try await $0.removeSite(site.name, dropDatabase: dropDatabase) }
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

func copyToClipboard(_ s: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(s, forType: .string)
}
