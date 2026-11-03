// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// What a new site starts from.
enum SiteSource: String, CaseIterable, Identifiable {
    case existing, empty, wordpress
    var id: String { rawValue }
    var title: String {
        switch self {
        case .existing: "Existing Folder"
        case .empty: "Empty Folder"
        case .wordpress: "WordPress"
        }
    }
}

struct NewSiteRequest: Identifiable {
    let id = UUID()
    var source: SiteSource = .existing
    var folder: URL?
    /// The group the site goes in (nil: top level).
    var group: Int64?
}

/// New Site: everything a site needs in one sheet. The core creates it in
/// one step and undoes it all if any part fails.
struct NewSiteSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let request: NewSiteRequest
    let added: (String) -> Void

    @State private var source: SiteSource = .existing
    @State private var name = ""
    @State private var domain = ""
    @State private var domainEdited = false
    @State private var aliases = ""
    @State private var php = ""
    @State private var group: Int64?
    @State private var folder: URL?
    @State private var parent = FileManager.default.homeDirectoryForCurrentUser.appending(path: "Sites")
    @State private var database: DatabaseChoice = .none
    @State private var existingDB = ""
    @State private var databases: [String] = []
    @State private var title = ""
    @State private var adminUser = "admin"
    @State private var adminEmail = ""
    @State private var ini: [String: String] = [:]
    @State private var showINI = false
    @State private var choosing = false
    @State private var noIndex = false
    @State private var working = false
    @State private var error: String?
    @State private var result: WordPressResult?

    enum DatabaseChoice: Hashable { case none, new, existing }

    var body: some View {
        if let result {
            WordPressDone(result: result) {
                added(result.name)
                dismiss()
            }
        } else {
            form
        }
    }

    // MARK: Derived values

    private var effectiveDomain: String {
        domainEdited ? domain : (name.isEmpty ? "" : "\(name).localhost")
    }

    private var path: URL? {
        switch source {
        case .existing: folder
        case .empty, .wordpress: name.isEmpty ? nil : parent.appending(path: name)
        }
    }

    private var aliasList: [String] {
        aliases.split(whereSeparator: { $0 == "," || $0 == " " }).map(String.init)
    }

    private var needsMySQL: Bool { source == .wordpress || database != .none }

    private var canCreate: Bool {
        !name.isEmpty && !php.isEmpty && path != nil && !working
            && (!needsMySQL || model.mysqlReady)
            && (source == .wordpress || database != .existing || !existingDB.isEmpty)
    }

    // MARK: Form

    private var form: some View {
        Form {
            Section {
                Picker("Start from", selection: $source) {
                    ForEach(SiteSource.allCases) { Text($0.title).tag($0) }
                }
                .pickerStyle(.segmented)
                folderRow
            } footer: {
                Text(sourceHelp).font(.caption).foregroundStyle(.secondary)
            }

            Section("Site") {
                TextField("Name", text: $name, prompt: Text("my-site"))
                    .onChange(of: name) { _, new in
                        let clean = AppModel.siteName(from: new)
                        if clean != new && !new.hasSuffix(" ") && !new.hasSuffix("-") { name = clean }
                    }
                TextField("Domain", text: Binding(
                    get: { effectiveDomain },
                    set: { domain = $0; domainEdited = !$0.isEmpty }
                ), prompt: Text("my-site.localhost"))
                TextField("Aliases", text: $aliases, prompt: Text("www.my-site.localhost (optional)"))
                LabeledContent("Address") {
                    Text(effectiveDomain.isEmpty ? "—" : "https://\(effectiveDomain):\(model.status?.httpsPort ?? 8443)")
                        .foregroundStyle(.secondary).textSelection(.enabled)
                }
                Picker("PHP", selection: $php) {
                    ForEach(model.php?.minors ?? [], id: \.self) { minor in
                        Text(model.phpLabel(minor)).tag(minor)
                    }
                }
                PHPSupportNote(minor: php)
                GroupPicker(selection: $group)
            }

            if source == .wordpress {
                Section("WordPress") {
                    TextField("Title", text: $title, prompt: Text(name.isEmpty ? "My Site" : name))
                    TextField("Admin user", text: $adminUser)
                    TextField("Admin email", text: $adminEmail,
                              prompt: Text("admin@\(effectiveDomain.isEmpty ? "my-site.localhost" : effectiveDomain)"))
                }
            } else {
                Section {
                    Picker("Database", selection: $database) {
                        Text("None").tag(DatabaseChoice.none)
                        Text("New database").tag(DatabaseChoice.new)
                        Text("Existing database").tag(DatabaseChoice.existing)
                    }
                    if database == .existing {
                        Picker("Use", selection: $existingDB) {
                            Text("Choose…").tag("")
                            ForEach(databases, id: \.self) { Text($0).tag($0) }
                        }
                    }
                } footer: {
                    Text(databaseHelp).font(.caption).foregroundStyle(needsMySQL && !model.mysqlReady ? .orange : .secondary)
                }
            }

            Section(isExpanded: $showINI) {
                INIRow(key: "memory_limit", title: "Memory limit", prompt: "512M", values: $ini)
                INIRow(key: "upload_max_filesize", title: "Max upload size", prompt: "128M", values: $ini)
                INIRow(key: "max_execution_time", title: "Max execution time (s)", prompt: "300", values: $ini)
                Picker("Show errors in the page", selection: Binding(
                    get: { ini["display_errors"] ?? "" },
                    set: { ini["display_errors"] = $0 }
                )) {
                    Text("Like PHP \(php)").tag("")
                    Text("On").tag("on")
                    Text("Off").tag("off")
                }
            } header: {
                Text("PHP Settings")
            }

            if let error {
                Text(error).foregroundStyle(.red).font(.callout)
            }
        }
        .formStyle(.grouped)
        .frame(width: 520)
        .frame(minHeight: 560)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button("Cancel") { dismiss() }.disabled(working)
            }
            ToolbarItem(placement: .confirmationAction) {
                Button(source == .wordpress ? "Create" : "Add Site") { Task { await create(force: false) } }
                    .disabled(!canCreate)
            }
        }
        .overlay {
            if working {
                ProgressView(model.busy ?? "Adding…")
                    .padding()
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
            }
        }
        .fileImporter(isPresented: $choosing, allowedContentTypes: [.folder]) { r in
            guard case .success(let url) = r else { return }
            if source == .existing { pick(url) } else { parent = url }
        }
        .alert("No index file", isPresented: $noIndex) {
            Button("Add Anyway") { Task { await create(force: true) } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("\(folder?.lastPathComponent ?? "This folder") has no index.php or index.html. Add it anyway?")
        }
        .onAppear {
            source = request.source
            group = request.group
            let minors = model.php?.minors ?? []
            php = minors.first(where: model.installed) ?? minors.first ?? ""
            if let f = request.folder { pick(f) }
        }
        .task(id: database) {
            if database == .existing, model.mysqlReady, databases.isEmpty {
                databases = await model.databases()
            }
        }
    }

    @ViewBuilder private var folderRow: some View {
        switch source {
        case .existing:
            LabeledContent("Folder") {
                HStack {
                    Text(folder?.path ?? "None").lineLimit(1).truncationMode(.middle)
                        .foregroundStyle(folder == nil ? .secondary : .primary)
                    Button("Choose…") { choosing = true }
                }
            }
        case .empty, .wordpress:
            LabeledContent("Folder") {
                HStack {
                    Text(parent.appending(path: name.isEmpty ? "<name>" : name).path)
                        .lineLimit(1).truncationMode(.middle).foregroundStyle(.secondary)
                    Button("Change…") { choosing = true }
                }
            }
        }
    }

    private var sourceHelp: String {
        switch source {
        case .existing: "Serve a folder you already have. Covo never changes its files."
        case .empty: "Covo creates a new empty folder for the site."
        case .wordpress: "Covo downloads WordPress (checksum verified), creates the folder, a database with its own user and wp-config.php, and installs it."
        }
    }

    private var databaseHelp: String {
        if needsMySQL && !model.mysqlReady {
            return model.status?.mysql.enabled == true ? "Start Covo first: databases need MySQL running." : "Turn MySQL on in Settings first."
        }
        switch database {
        case .none: return "You can add one later from the site."
        case .new: return "A database named after the site, with its own user; the password stays in your keychain."
        case .existing: return "The site connects as root, like MAMP."
        }
    }

    private func pick(_ url: URL) {
        folder = url
        // A folder named "public" or "htdocs" says little: use its parent's name.
        let generic = ["public", "htdocs", "www", "web", "public_html"]
        let base = generic.contains(url.lastPathComponent.lowercased()) ? url.deletingLastPathComponent() : url
        name = AppModel.siteName(from: base.lastPathComponent)
    }

    private var iniValues: [String: String] {
        ini.filter { !$0.value.trimmingCharacters(in: .whitespaces).isEmpty }
    }

    private func create(force: Bool) async {
        guard let path else { return }
        working = true
        defer { working = false }
        error = nil
        let dom = domainEdited ? domain : nil
        do {
            if source == .wordpress {
                var req = CovoAPI.NewWordPress(name: name, path: path.path, php: php, title: title,
                                               adminUser: adminUser, adminEmail: adminEmail)
                req.domain = dom
                req.aliases = aliasList
                req.groupId = group
                req.phpSettings = iniValues
                result = try await model.newWordPress(req)
                return
            }
            var req = CovoAPI.NewSite(name: name, domain: dom, php: php, path: path.path)
            req.force = force
            req.createFolder = source == .empty
            req.aliases = aliasList
            req.groupId = group
            req.phpSettings = iniValues
            req.newDatabase = database == .new
            req.linkDatabase = database == .existing ? existingDB : nil
            try await model.addSite(req)
            added(name)
            dismiss()
        } catch let e as APIError where e.code == "confirmation_required" && source == .existing {
            noIndex = true
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// One php.ini value of a new site; empty follows the PHP version.
private struct INIRow: View {
    let key: String
    let title: String
    let prompt: String
    @Binding var values: [String: String]

    var body: some View {
        TextField(title, text: Binding(get: { values[key] ?? "" }, set: { values[key] = $0 }),
                  prompt: Text("\(prompt) (default)"))
    }
}

/// Picks a group of the site list, shown as an indented tree.
struct GroupPicker: View {
    @Environment(AppModel.self) private var model
    @Binding var selection: Int64?
    var title = "Group"

    var body: some View {
        Picker(title, selection: $selection) {
            Text("None").tag(Int64?.none)
            ForEach(model.groups) { g in
                Text(String(repeating: "    ", count: g.path.filter { $0 == "/" }.count) + g.name)
                    .tag(Int64?.some(g.id))
            }
        }
    }
}

/// The new WordPress site and its admin login, shown once.
struct WordPressDone: View {
    let result: WordPressResult
    let done: () -> Void

    var body: some View {
        let r = result
        Form {
            Section {
                LabeledContent("Site") { Link(r.url, destination: URL(string: r.url)!) }
                LabeledContent("Admin") { Link(r.adminUrl, destination: URL(string: r.adminUrl)!) }
                LabeledContent("User") { Text(r.adminUser).textSelection(.enabled) }
                LabeledContent("Password") {
                    HStack {
                        Text(r.adminPassword).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                        Button {
                            copyToClipboard(r.adminPassword)
                        } label: {
                            Image(systemName: "doc.on.doc")
                        }
                        .buttonStyle(.borderless)
                        .help("Copy")
                    }
                }
            } header: {
                Text("WordPress \(r.wordpress) is ready").font(.headline)
            } footer: {
                Text("Save the password now (e.g. in your password manager): Covo does not keep it. You can reset it later with `covo wp \(r.name) user update \(r.adminUser) --prompt=user_pass`.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .frame(width: 520)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Done", action: done)
            }
            ToolbarItem(placement: .primaryAction) {
                Button("Open Admin") { open(r.adminUrl) }
            }
        }
    }
}
