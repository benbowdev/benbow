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
            MailSettings().tabItem { Label("Mail", systemImage: "envelope") }
            DomainSettings().tabItem { Label("Domains", systemImage: "globe") }
        }
        .frame(width: 520)
        .disabled(model.phase != .ready)
    }

    @Environment(AppModel.self) private var model
}

struct GeneralSettings: View {
    @Environment(AppModel.self) private var model
    @State private var port = ""
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
            Section {
                LabeledContent("HTTPS port") {
                    HStack {
                        TextField("HTTPS port", text: $port, prompt: Text("8443"))
                            .labelsHidden().frame(width: 80).multilineTextAlignment(.trailing)
                            .onSubmit(save)
                        Button("Apply", action: save).disabled(newPort == nil || model.busy != nil)
                    }
                }
            } footer: {
                Text("Sites are served at https://<name>.localhost:\(current). Changing it starts Covo if it is stopped.")
                    .font(.caption).foregroundStyle(.secondary)
            }
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
                UpdatesList()
            }
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
        .onAppear { port = String(current) }
    }

    private var current: Int { model.status?.httpsPort ?? 8443 }

    private var newPort: Int? {
        guard let p = Int(port), (1024...65535).contains(p), p != current else { return nil }
        return p
    }

    private func save() {
        guard let p = newPort else { return }
        Task { await model.setHTTPSPort(p) }
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
                    LabeledContent("PHP \(minor)") {
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
                    }
                }
            } header: {
                Text("Versions")
            } footer: {
                Text("Versions are also downloaded when a site needs them. Change a site's version in its detail.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                Picker("Version", selection: $minor) {
                    ForEach(model.php?.minors ?? [], id: \.self) { Text("PHP \($0)").tag($0) }
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
