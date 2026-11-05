// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
import CovoKit
import ServiceManagement
import SwiftUI

/// Covo → Settings… (⌘,). Things set once, not per site.
struct SettingsView: View {
    var body: some View {
        TabView {
            GeneralSettings().tabItem { Label("General", systemImage: "gearshape") }
            PHPSettingsTab().tabItem { Label("PHP", systemImage: "chevron.left.forwardslash.chevron.right") }
            MySQLSettings().tabItem { Label("MySQL", systemImage: "cylinder.split.1x2") }
            ServerSettings(server: "postgres").tabItem { Label("PostgreSQL", systemImage: "cylinder") }
            ServerSettings(server: "redis").tabItem { Label("Redis", systemImage: "bolt.horizontal") }
            BlueprintsSettings().tabItem { Label("Blueprints", systemImage: "square.stack.3d.up") }
            AgentsSettings().tabItem { Label("AI Agents", systemImage: "sparkles") }
            MailSettings().tabItem { Label("Mail", systemImage: "envelope") }
            DomainSettings().tabItem { Label("Domains", systemImage: "globe") }
        }
        // Wide enough for all nine tabs in the toolbar (narrower, macOS hides
        // the last ones behind an overflow chevron); resizable beyond that.
        .frame(minWidth: 760, idealWidth: 760, maxWidth: .infinity,
               minHeight: 420, idealHeight: 560, maxHeight: .infinity)
        .disabled(model.phase != .ready)
    }

    @Environment(AppModel.self) private var model
}

struct GeneralSettings: View {
    @Environment(AppModel.self) private var model
    @State private var loginItem = SMAppService.mainApp.status == .enabled
    @State private var loginError: String?
    @AppStorage(AppModel.startOnLaunchKey) private var startOnLaunch = false

    private func setLoginItem(_ on: Bool) {
        loginError = nil
        do {
            if on {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
        } catch {
            loginError = error.localizedDescription
        }
        switch SMAppService.mainApp.status {
        case .enabled:
            loginItem = true
        case .requiresApproval:
            loginItem = false
            loginError = "macOS needs your approval in System Settings → General → Login Items."
            SMAppService.openSystemSettingsLoginItems()
        default:
            loginItem = false
        }
    }

    var body: some View {
        Form {
            PortsSection()
            Section("Startup") {
                Toggle(isOn: Binding(
                    get: { loginItem },
                    set: { setLoginItem($0) }
                )) {
                    Text("Open Covo at login")
                    Text("Listed in System Settings → General → Login Items.")
                }
                Toggle("Start all sites when Covo opens", isOn: $startOnLaunch)
                if let loginError {
                    Text(loginError).font(.caption).foregroundStyle(.red)
                }
            }
            Section("Updates") {
                AppUpdateRow()
                UpdatesList()
            }
            SnapshotKeepRow()
            Section("Certificate") {
                LabeledContent("Covo local CA") {
                    if model.status?.ca.trusted == true {
                        HStack {
                            Label("Trusted", systemImage: "checkmark.seal.fill").foregroundStyle(.green)
                            Button("Remove Trust") { Task { await model.untrust() } }
                        }
                    } else {
                        Button("Trust…") { Task { await model.trust() } }
                    }
                }
                .disabled(model.busy != nil)
                Text("Only valid for .localhost, .test and .local names. Trusting asks macOS for confirmation.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section("Data") {
                LabeledContent("Covo folder") {
                    Button("Show in Finder") {
                        if let home = model.status?.home { NSWorkspace.shared.open(URL(fileURLWithPath: home)) }
                    }
                }
            }
        }
        .formStyle(.grouped)
        .toggleStyle(.covoSwitch)
    }
}

/// HTTPS and HTTP ports, like MAMP: standard (no port in addresses) or any
/// port from 1024.
struct PortsSection: View {
    @Environment(AppModel.self) private var model
    @State private var httpsStandard = false
    @State private var httpsCustom = ""
    @State private var httpMode = HTTPMode.off
    @State private var httpCustom = ""
    @State private var loaded = false

    enum HTTPMode: Hashable { case off, standard, custom }

    var body: some View {
        Section {
            Picker("HTTPS", selection: $httpsStandard) {
                Text("443 – no port in addresses").tag(true)
                Text("Custom port").tag(false)
            }
            if !httpsStandard {
                LabeledContent("HTTPS port") {
                    TextField("HTTPS port", text: $httpsCustom, prompt: Text("8443"))
                        .labelsHidden().frame(width: 80).multilineTextAlignment(.trailing)
                }
            }
            Picker("HTTP", selection: $httpMode) {
                Text("Off").tag(HTTPMode.off)
                Text("80 – redirects to HTTPS").tag(HTTPMode.standard)
                Text("Custom port – redirects to HTTPS").tag(HTTPMode.custom)
            }
            if httpMode == .custom {
                LabeledContent("HTTP port") {
                    TextField("HTTP port", text: $httpCustom, prompt: Text("8080"))
                        .labelsHidden().frame(width: 80).multilineTextAlignment(.trailing)
                }
            }
            HStack {
                Text("Sites: \(model.address("<name>.localhost"))").foregroundStyle(.secondary)
                Spacer()
                Button("Apply") { Task { await apply() } }
                    .disabled(wanted == nil || model.busy != nil)
            }
            if let err = model.ports?.error {
                Text(err).font(.caption).foregroundStyle(.red)
            }
        } header: {
            Text("Ports")
        } footer: {
            Text("443 and 80 need your administrator password once: Covo's helper forwards them to Covo, only while Covo is running, and never takes them from another program (e.g. MAMP on 80). WordPress keeps its address in its database: after changing the HTTPS port, update it with “covo wp <site> search-replace”.")
                .font(.caption).foregroundStyle(.secondary)
        }
        .task {
            await model.loadPorts()
            load()
        }
        .onChange(of: model.ports) { _, _ in if !loaded { load() } }
    }

    private func load() {
        guard let p = model.ports else { return }
        loaded = true
        httpsStandard = p.httpsPort == 443
        httpsCustom = p.httpsPort == 443 ? "8443" : String(p.httpsPort)
        switch p.httpPort ?? 0 {
        case 0: httpMode = .off
        case 80: httpMode = .standard
        case let n: httpMode = .custom; httpCustom = String(n)
        }
    }

    /// The ports to set, or nil if invalid or unchanged.
    private var wanted: (https: Int, http: Int)? {
        let https = httpsStandard ? 443 : (Int(httpsCustom) ?? 0)
        let http: Int
        switch httpMode {
        case .off: http = 0
        case .standard: http = 80
        case .custom: http = Int(httpCustom) ?? 0
        }
        guard https == 443 || (1024...65535).contains(https) else { return nil }
        guard httpMode != .custom || (1024...65535).contains(http), http != https else { return nil }
        if let p = model.ports, p.httpsPort == https, (p.httpPort ?? 0) == http { return nil }
        return (https, http)
    }

    private func apply() async {
        guard let w = wanted else { return }
        await model.setPorts(https: w.https, http: w.http)
        loaded = false
        load()
    }
}

struct PHPSettingsTab: View {
    @Environment(AppModel.self) private var model
    @State private var minor = ""
    @State private var saved: CovoKit.PHPSettings?
    @State private var draft: CovoKit.PHPSettings?

    var body: some View {
        Form {
            Section {
                ForEach(model.php?.minors ?? [], id: \.self) { minor in
                    LabeledContent {
                        if let v = model.php?.installed.first(where: { $0.minor == minor }) {
                            HStack {
                                Text(v.version).foregroundStyle(.secondary)
                                let n = model.sites.filter { $0.php == minor }.count
                                if n > 0 {
                                    Text("· \(n) site\(n == 1 ? "" : "s")").foregroundStyle(.secondary)
                                }
                            }
                        } else {
                            Button("Download") { Task { await model.installPHP(minor) } }
                                .disabled(model.busy != nil)
                        }
                    } label: {
                        let s = model.phpSupport(minor)
                        VStack(alignment: .leading, spacing: 2) {
                            HStack(spacing: 4) {
                                Text("PHP \(minor)")
                                if s?.isEOL == true {
                                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                                        .help("No security fixes since \(PHPSupportNote.date(s?.securityUntil))")
                                }
                            }
                            switch s?.status {
                            case "eol":
                                Text("Too old: no security fixes since \(PHPSupportNote.date(s?.securityUntil)). Not recommended.")
                                    .font(.caption).foregroundStyle(.orange)
                            case "security":
                                Text("Security fixes only, until \(PHPSupportNote.date(s?.securityUntil))")
                                    .font(.caption).foregroundStyle(.secondary)
                            default:
                                if let until = s?.securityUntil {
                                    Text("Supported until \(PHPSupportNote.date(until))")
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                }
            } header: {
                Text("Versions")
            } footer: {
                Text("Versions are also downloaded when a site needs them. Change a site's version in its detail. Old versions are offered for old sites only: they no longer get security fixes from php.net.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                Picker("Version", selection: $minor) {
                    ForEach(model.php?.minors ?? [], id: \.self) { Text(model.phpLabel($0, prefix: "PHP ")).tag($0) }
                }
                if let d = Binding($draft) {
                    TextField("Memory limit", text: d.memoryLimit, prompt: Text("512M"))
                    TextField("Max upload size", text: d.uploadMaxFilesize, prompt: Text("128M"))
                    TextField("Max execution time (s)", value: d.maxExecutionTime, format: .number, prompt: Text("300"))
                    Toggle("Show errors in the page", isOn: d.displayErrors)
                    HStack {
                        Spacer()
                        Button("Revert") { draft = saved }.disabled(draft == saved)
                        Button("Apply") { Task { await apply() } }
                            .keyboardShortcut(.defaultAction)
                            .disabled(draft == saved || model.busy != nil)
                    }
                }
            } header: {
                Text("php.ini")
            } footer: {
                Text("Sizes like 512M or 1G (-1 = no memory limit). post_max_size follows the upload size. 0 s = no time limit. Applies to every site on this version.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .toggleStyle(.covoSwitch)
        .onAppear {
            if minor.isEmpty { minor = model.php?.installed.first?.minor ?? model.php?.minors.first ?? "" }
        }
        .task(id: minor) { await load() }
    }

    private func load() async {
        guard !minor.isEmpty else { return }
        saved = await model.phpSettings(minor)
        draft = saved
    }

    private func apply() async {
        guard let d = draft else { return }
        if await model.setPHPSettings(d) { await load() }
    }
}

struct MySQLSettings: View {
    @Environment(AppModel.self) private var model
    @State private var credentials: MySQLCredentials?
    @State private var port = ""

    private var mysql: MySQLStatus? { model.status?.mysql }

    var body: some View {
        Form {
            Section {
                Toggle(isOn: Binding(
                    get: { mysql?.enabled ?? false },
                    set: { on in Task { await model.setMySQL(enabled: on) } }
                )) {
                    Text("MySQL \(mysql?.version ?? "8.4")")
                    Text(statusText).foregroundStyle(.secondary)
                }
                .disabled(model.busy != nil)
                LabeledContent("Port") {
                    HStack {
                        TextField("Port", text: $port, prompt: Text("3307"))
                            .labelsHidden().frame(width: 80).multilineTextAlignment(.trailing)
                        Button("Apply") {
                            if let p = newPort { Task { await model.setMySQL(enabled: true, port: p) } }
                        }
                        .disabled(newPort == nil || model.busy != nil)
                    }
                }
            }
            if mysql?.enabled == true {
                Section("Connection") {
                    if let c = credentials {
                        row("Host", c.host)
                        row("Port", String(c.port))
                        row("User", c.user)
                        LabeledContent("Password") {
                            HStack {
                                Text(String(repeating: "•", count: 10)).foregroundStyle(.secondary)
                                copy(c.password)
                            }
                        }
                        row("Socket", c.socket)
                        LabeledContent("Adminer") {
                            Button("Open") { Task { await model.openAdminer() } }
                                .help("Every database, already logged in as root")
                        }
                    } else {
                        Button("Show Connection Details") {
                            Task { credentials = await model.mysqlCredentials() }
                        }
                    }
                }
            }
        }
        .formStyle(.grouped)
        .toggleStyle(.covoSwitch)
        .onAppear { port = String(mysql?.port ?? 3307) }
        .onChange(of: mysql?.port) { _, p in port = String(p ?? 3307) }
    }

    private var statusText: String {
        guard let m = mysql, m.enabled else { return "Off" }
        let at = "\(m.host ?? "127.0.0.1"):\(m.port ?? 3307)"
        return m.running ? "Running on \(at)" : "On, starts with Covo (\(at))"
    }

    private var newPort: Int? {
        guard let p = Int(port), (1024...65535).contains(p), p != (mysql?.port ?? 3307) else { return nil }
        return p
    }

    private func row(_ title: String, _ value: String) -> some View {
        LabeledContent(title) {
            HStack {
                Text(value).textSelection(.enabled).lineLimit(1).truncationMode(.middle)
                copy(value)
            }
        }
    }

    private func copy(_ value: String) -> some View {
        Button {
            copyToClipboard(value)
        } label: {
            Image(systemName: "doc.on.doc")
        }
        .buttonStyle(.borderless)
        .help("Copy")
    }
}

struct MailSettings: View {
    @Environment(AppModel.self) private var model
    private var mail: MailStatus? { model.status?.mail }

    var body: some View {
        Form {
            Section {
                Toggle(isOn: Binding(
                    get: { mail?.enabled ?? false },
                    set: { on in Task { await model.setMail(on) } }
                )) {
                    Text("Catch outgoing mail")
                    Text("Every mail your sites send (password resets, orders, forms) lands in a local inbox instead of being delivered.")
                }
                .disabled(model.busy != nil)
                if let m = mail, m.enabled {
                    LabeledContent("Inbox") {
                        if let u = m.url, let url = URL(string: u) {
                            Button("Open") { NSWorkspace.shared.open(url) }.disabled(!m.running)
                        }
                    }
                    LabeledContent("SMTP") {
                        Text("127.0.0.1:\(m.smtpPort ?? 1025)").textSelection(.enabled).foregroundStyle(.secondary)
                    }
                }
            } footer: {
                Text("Mailpit \(mail?.version ?? "") · PHP's mail() is sent to it automatically; other code can use the SMTP address.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .toggleStyle(.covoSwitch)
    }
}

struct DomainSettings: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        Form {
            Section {
                LabeledContent(".localhost") { Text("Always on").foregroundStyle(.secondary) }
                zone("test", ".test", "Names like shop.test. Adds a resolver file for .test and a small local DNS server.")
                zone("local", ".local", "MAMP compatibility mode: names like shop.local, written to /etc/hosts by a small Covo helper.")
            } footer: {
                Text("Turning .test or .local on or off asks for your administrator password.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .toggleStyle(.covoSwitch)
    }

    private func zone(_ zone: String, _ title: String, _ detail: String) -> some View {
        Toggle(isOn: Binding(
            get: { model.domainEnabled(zone) },
            set: { on in Task { await model.setDomain(zone, enabled: on) } }
        )) {
            Text(title)
            Text(detail)
        }
        .disabled(model.busy != nil)
    }
}

/// PostgreSQL or Redis, both optional and off by default.
struct ServerSettings: View {
    @Environment(AppModel.self) private var model
    /// "postgres" or "redis".
    let server: String
    @State private var credentials: MySQLCredentials?
    @State private var port = ""

    private var title: String { server == "postgres" ? "PostgreSQL" : "Redis" }
    private var defaultPort: Int { server == "postgres" ? 5433 : 6380 }
    private var status: MySQLStatus? { server == "postgres" ? model.status?.postgres : model.status?.redis }

    var body: some View {
        Form {
            Section {
                Toggle(isOn: Binding(
                    get: { status?.enabled ?? false },
                    set: { on in Task { await model.setServer(server, enabled: on) } }
                )) {
                    Text("\(title) \(status?.version ?? (server == "postgres" ? "18" : "8"))")
                    Text(statusText).foregroundStyle(.secondary)
                }
                .disabled(model.busy != nil)
                LabeledContent("Port") {
                    HStack {
                        TextField("Port", text: $port, prompt: Text(String(defaultPort)))
                            .labelsHidden().frame(width: 80).multilineTextAlignment(.trailing)
                        Button("Apply") {
                            if let p = newPort { Task { await model.setServer(server, enabled: true, port: p) } }
                        }
                        .disabled(newPort == nil || model.busy != nil)
                    }
                }
            } footer: {
                Text(server == "postgres"
                     ? "Off until you need it. Databases live in Covo's folder; the postgres password is kept in your keychain. Sites get their own database and user from the site detail or New Site."
                     : "Off until you need it. One Redis for every site on 127.0.0.1, no password: nothing outside this Mac reaches it. New Laravel projects use it for the cache. Redis 8 is distributed under the AGPLv3.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if server == "postgres", status?.enabled == true {
                Section("Connection") {
                    if let c = credentials {
                        LabeledContent("Host", value: "\(c.host):\(c.port)")
                        LabeledContent("User", value: c.user)
                        LabeledContent("Password") {
                            HStack {
                                Text(String(repeating: "•", count: 10)).foregroundStyle(.secondary)
                                Button { copyToClipboard(c.password) } label: { Image(systemName: "doc.on.doc") }
                                    .buttonStyle(.borderless).help("Copy")
                            }
                        }
                        LabeledContent("Adminer") {
                            Button("Open") { Task { await model.openAdminer(engine: "pgsql") } }
                                .help("Every database, already logged in as postgres")
                                .disabled(!model.running)
                        }
                    } else {
                        Button("Show Connection Details") {
                            Task { credentials = await model.postgresCredentials() }
                        }
                    }
                }
            }
        }
        .formStyle(.grouped)
        .toggleStyle(.covoSwitch)
        .onAppear { port = String(status?.port ?? defaultPort) }
        .onChange(of: status?.port) { _, p in port = String(p ?? defaultPort) }
    }

    private var statusText: String {
        guard let s = status, s.enabled else { return "Off" }
        let at = "\(s.host ?? "127.0.0.1"):\(s.port ?? defaultPort)"
        return s.running ? "Running on \(at)" : "On, starts with Covo (\(at))"
    }

    private var newPort: Int? {
        guard let p = Int(port), (1024...65535).contains(p), p != (status?.port ?? defaultPort) else { return nil }
        return p
    }
}

/// How many snapshots Covo keeps per site.
struct SnapshotKeepRow: View {
    @Environment(AppModel.self) private var model
    @State private var keep = 10

    var body: some View {
        Section {
            Stepper("Keep the last \(keep) snapshots of each site", value: $keep, in: 1...100)
                .onChange(of: keep) { _, n in Task { await model.setSnapshotKeep(n) } }
        } header: {
            Text("Snapshots")
        } footer: {
            Text("Older ones are deleted when a new one is taken. Snapshots live in Covo's folder; removing a site deletes its snapshots.")
                .font(.caption).foregroundStyle(.secondary)
        }
        .task { keep = await model.snapshotKeep() }
    }
}

/// AI agents (MCP): how to connect them, what they may change, what they did.
struct AgentsSettings: View {
    @Environment(AppModel.self) private var model
    @State private var perms: MCPPermissions?
    @State private var actions: [MCPAction] = []
    @State private var tools: AgentTools?
    @State private var idle = 24
    @State private var connecting: AgentTool?
    @State private var connected: AgentConnectResult?

    var body: some View {
        Form {
            Section {
                let list = (tools?.agents ?? []).filter(\.installed)
                if tools == nil {
                    ProgressView()
                } else if list.isEmpty {
                    Text("No AI agent found on this Mac (Claude Code, Codex, Gemini CLI, Cursor, Copilot CLI, Claude Desktop).")
                        .foregroundStyle(.secondary)
                }
                ForEach(list) { a in
                    LabeledContent {
                        if a.connected {
                            Label("Connected", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                        } else if a.canConnect {
                            Button("Connect…") { connecting = a }.disabled(model.busy != nil)
                        } else {
                            Text("Connected when started from Covo").foregroundStyle(.secondary)
                        }
                    } label: {
                        Text(a.name)
                        Text(a.connected ? (a.configPath ?? "") : a.perRun ? "“Work with an Agent” connects it for that run only." : "Needs Covo in its own settings.")
                    }
                }
                if let c = connected {
                    Text(c.restart == true ? "Done. Restart the app to see Covo." : c.already == true ? "Covo was already there." : "Done.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                DisclosureGroup("Set it up by hand") {
                    let bin = tools?.covoBin ?? Daemon.binary()?.path ?? "covo"
                    command("Claude Code", "claude mcp add -s user covo -- '\(bin)' mcp")
                    command("Other agents", "\"covo\": { \"command\": \"\(bin)\", \"args\": [\"mcp\"] }")
                }
            } header: {
                Text("Connect")
            } footer: {
                Text("Covo adds itself only to the agent's own settings in your home folder, after a backup in Covo's folder; never to a project. Agents can read sites, logs and databases, fetch pages with their PHP errors, read the mail PHP sent, and run wp-cli, Composer and Artisan.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section {
                Toggle(isOn: Binding(get: { perms?.all ?? false }, set: { on in Task { perms = await model.setMCPPermission("*", write: on) } })) {
                    Text("Allow changes everywhere")
                    Text("Including creating sites and starting or stopping Covo.")
                }
                ForEach(model.sites) { site in
                    let temporary = perms?.temporary?.contains(site.name) == true
                    Toggle(isOn: Binding(
                        get: { perms?.all == true || perms?.sites.contains(site.name) == true || temporary },
                        set: { on in Task { perms = await model.setMCPPermission(site.name, write: on) } }
                    )) {
                        Text(site.name)
                        if temporary { Text("Allowed until its agent session closes (Work with an Agent).") }
                    }
                    .disabled(perms?.all == true || temporary)
                }
            } header: {
                Text("What agents may change")
            } footer: {
                Text("Off by default: agents can only read. While an agent works on a site, Covo keeps the site's starting state (files and database) until you keep or undo the changes. Covo never lets agents delete sites or databases.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section {
                Picker("Close idle sessions after", selection: Binding(get: { idle }, set: { h in idle = h; Task { await model.setAgentIdleHours(h) } })) {
                    Text("8 hours").tag(8)
                    Text("24 hours").tag(24)
                    Text("3 days").tag(72)
                    Text("1 week").tag(168)
                    Text("Never").tag(0)
                }
            } header: {
                Text("Agent sessions")
            } footer: {
                Text("A session nobody used for that long closes by itself and keeps the changes; its starting snapshot stays like any other snapshot.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section("What agents did") {
                if actions.isEmpty {
                    Text("Nothing yet.").foregroundStyle(.secondary)
                }
                ForEach(actions.prefix(50)) { a in
                    HStack(alignment: .firstTextBaseline) {
                        Image(systemName: a.error != nil ? "xmark.circle" : a.dryRun == true ? "eye" : "checkmark.circle")
                            .foregroundStyle(a.error != nil ? .red : .secondary)
                        VStack(alignment: .leading, spacing: 1) {
                            Text("\(a.tool)\(a.site.map { " · \($0)" } ?? "")").font(.callout)
                            Text(a.error ?? (a.snapshot.map { "checkpoint \($0) taken before" } ?? (a.dryRun == true ? "dry run" : "")))
                                .font(.caption).foregroundStyle(.secondary).lineLimit(2)
                        }
                        Spacer()
                        Text(a.at.formatted(date: .abbreviated, time: .shortened)).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .toggleStyle(.covoSwitch)
        .task {
            perms = await model.mcpPermissions()
            actions = await model.mcpActions()
            tools = await model.agentTools()
            idle = await model.agentIdleHours() ?? 24
        }
        .sheet(item: $connecting) { a in
            VStack(alignment: .leading, spacing: 12) {
                Text("Connect \(a.name) to Covo?").font(.title3.weight(.semibold))
                ExplainBox(
                    title: "What Covo changes",
                    steps: a.id == "claude" || a.id == "codex"
                        ? ["Covo runs \(a.name)'s own command (\(a.id) mcp add) to add a “covo” MCP server for your user.",
                           "A copy of \(a.configPath ?? "its settings") is saved first in Covo's folder (data/backups/agents)."]
                        : ["Covo saves a copy of \(a.configPath ?? "its settings") in Covo's folder (data/backups/agents).",
                           "It adds a “covo” entry under mcpServers, keeping everything else."],
                    caution: "Agents stay read-only until you allow changes below or start them with Work with an Agent."
                )
                HStack {
                    Spacer()
                    Button("Cancel", role: .cancel) { connecting = nil }.keyboardShortcut(.cancelAction)
                    Button("Connect") {
                        Task {
                            connected = await model.connectAgent(a.id)
                            tools = await model.agentTools()
                            connecting = nil
                        }
                    }
                    .keyboardShortcut(.defaultAction)
                }
            }
            .padding()
            .frame(width: 500)
        }
    }

    private func command(_ title: String, _ text: String) -> some View {
        LabeledContent(title) {
            HStack {
                Text(text).font(.system(.caption, design: .monospaced)).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                Button { copyToClipboard(text) } label: { Image(systemName: "doc.on.doc") }
                    .buttonStyle(.borderless).help("Copy")
            }
        }
    }
}

/// Saved blueprints: import, export, delete; the GitHub token for private
/// repositories.
struct BlueprintsSettings: View {
    @Environment(AppModel.self) private var model
    @State private var list: [BlueprintInfo] = []
    @State private var importing = false
    @State private var deleting: BlueprintInfo?
    @State private var token = ""
    @State private var hasToken = false
    @State private var error: String?

    var body: some View {
        Form {
            Section {
                if list.isEmpty {
                    Text("No blueprints. Save one from a WordPress site (Advanced → Blueprints) or import a JSON file.")
                        .foregroundStyle(.secondary)
                }
                ForEach(list) { b in
                    HStack {
                        VStack(alignment: .leading, spacing: 1) {
                            Text(b.name)
                            Text([b.description ?? "", "\(b.themes) themes, \(b.plugins) plugins"].filter { !$0.isEmpty }.joined(separator: " · "))
                                .font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Export…") { Task { await export(b) } }
                        Button { deleting = b } label: { Image(systemName: "trash") }
                            .buttonStyle(.borderless).help("Delete this blueprint")
                    }
                }
                HStack {
                    Spacer()
                    Button("Import…") { importing = true }
                }
                if let error {
                    Text(error).foregroundStyle(.red).font(.caption).textSelection(.enabled)
                }
            } header: {
                Text("Blueprints")
            } footer: {
                Text(BlueprintSheet.notice + " New WordPress sites can start from a blueprint (New Site → WordPress).")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section {
                HStack {
                    SecureField("Token", text: $token, prompt: Text(hasToken ? "Stored in the keychain" : "ghp_… or github_pat_…"))
                    Button("Save") { Task { hasToken = await model.setGitHubToken(token); token = "" } }
                        .disabled(token.isEmpty)
                    if hasToken {
                        Button("Remove") { Task { hasToken = await model.setGitHubToken("") } }
                    }
                }
            } header: {
                Text("Private GitHub repositories")
            } footer: {
                Text("A fine-grained token with read access to the repositories' contents. Kept in your login keychain.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .toggleStyle(.covoSwitch)
        .task { await reload() }
        .fileImporter(isPresented: $importing, allowedContentTypes: [.json]) { result in
            if case .success(let url) = result { Task { await importFile(url) } }
        }
        .confirmationDialog("Delete blueprint \(deleting?.name ?? "")?", isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } })) {
            Button("Delete", role: .destructive) {
                if let b = deleting { Task { await model.deleteBlueprint(b.name); await reload() } }
            }
        } message: {
            Text("Sites made from it are not changed.")
        }
    }

    private func reload() async {
        list = await model.blueprints()
        hasToken = await model.hasGitHubToken()
    }

    private func importFile(_ url: URL) async {
        error = nil
        let access = url.startAccessingSecurityScopedResource()
        defer { if access { url.stopAccessingSecurityScopedResource() } }
        do {
            let data = try Data(contentsOf: url)
            let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any]
            let name = (obj?["name"] as? String).flatMap { $0.isEmpty ? nil : $0 }
                ?? url.deletingPathExtension().lastPathComponent.lowercased()
            _ = try await model.importBlueprint(data, name: name, overwrite: false)
            await reload()
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func export(_ b: BlueprintInfo) async {
        error = nil
        do {
            let data = try await model.blueprintJSON(b.name)
            let panel = NSSavePanel()
            panel.nameFieldStringValue = b.name + ".json"
            panel.allowedContentTypes = [.json]
            if panel.runModal() == .OK, let url = panel.url {
                try data.write(to: url)
            }
        } catch {
            self.error = error.localizedDescription
        }
    }
}
