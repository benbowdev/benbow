// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

struct SiteDetail: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    let site: Site
    @State private var advanced = false
    @State private var choosingFolder = false
    /// A folder without an index file, waiting for "Use Anyway".
    @State private var pendingFolder: URL?
    @State private var domain = ""
    @State private var alias = ""
    @State private var editingPHP = false
    @State private var checkingPHP = false
    @State private var runningTool = false
    @State private var phoneSheet = false
    @State private var undoingMigration = false
    @State private var snapshotsSheet = false
    @State private var blueprintSheet = false
    @State private var remoteSheet = false
    @State private var agentSheet = false

    private var toolTitle: String {
        switch site.framework {
        case "laravel": "Artisan and Composer"
        case "wordpress": "wp-cli and Composer"
        default: "Composer"
        }
    }

    var body: some View {
        Form {
            if let session = model.session(for: site) {
                Section {
                    AgentSessionBanner(site: site, session: session)
                }
            }
            Section {
                if site.archived {
                    LabeledContent {
                        Button("Unarchive") { Task { await model.setArchived(site, false) } }
                            .disabled(model.busy != nil)
                    } label: {
                        Label("Archived", systemImage: "archivebox")
                        Text("Not served. Folder, database and settings are kept.")
                    }
                } else {
                    Toggle(isOn: Binding(
                        get: { site.enabled },
                        set: { on in Task { await model.setEnabled(site, on) } }
                    )) {
                        Text("Serve this site")
                        if !site.enabled { Text("Off: kept in the list, not served.") }
                    }
                    .disabled(model.busy != nil)
                }
                LabeledContent("Name") {
                    HStack(spacing: 6) {
                        if let fw = site.framework {
                            Text(fw == "laravel" ? "Laravel" : fw == "wordpress" ? "WordPress" : fw)
                                .font(.caption.weight(.medium))
                                .padding(.horizontal, 6).padding(.vertical, 1)
                                .background(.quaternary, in: Capsule())
                        }
                        Text(site.name)
                    }
                }
                LabeledContent {
                    HStack {
                        Text(site.projectRoot).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                        Button {
                            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: site.projectRoot)
                        } label: {
                            if let f = ToolApp.finder {
                                Image(nsImage: f.icon)
                            } else {
                                Image(systemName: "folder")
                            }
                        }
                        .buttonStyle(.borderless)
                        .help("Show in Finder")
                        Menu {
                            OpenWithItems(folder: site.projectRoot)
                        } label: {
                            Image(systemName: "terminal")
                        }
                        .menuStyle(.borderlessButton)
                        .menuIndicator(.hidden)
                        .fixedSize()
                        .help("Open the folder in a terminal or editor")
                        Button("Change…") { choosingFolder = true }
                            .disabled(model.busy != nil)
                    }
                } label: {
                    Text("Folder")
                    if site.appRoot != nil {
                        Text("Served from public/").font(.caption)
                    }
                }
                Picker("PHP", selection: Binding(
                    get: { site.php },
                    set: { minor in
                        guard minor != site.php else { return }
                        Task { await model.updateSiteShowingErrors(site, php: minor) }
                    }
                )) {
                    ForEach(phpChoices, id: \.self) { minor in
                        Text(model.phpLabel(minor)).tag(minor)
                    }
                }
                .disabled(model.busy != nil)
                PHPSupportNote(minor: site.php)
                if model.phpSupport(site.php)?.isEOL == true, let rec = model.recommendedPHP {
                    HStack {
                        Spacer()
                        Button("Check Compatibility with PHP \(rec)…") { checkingPHP = true }
                    }
                }
                LabeledContent("HTTPS", value: site.https ? "On" : "Off")
                DatabaseRow(site: site)
            } header: {
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(site.name).font(.title2.weight(.semibold)).foregroundStyle(.primary)
                        Text(site.url).font(.callout).foregroundStyle(.secondary).textSelection(.enabled)
                    }
                    Spacer()
                    Button {
                        agentSheet = true
                    } label: {
                        Label("Work with an Agent…", systemImage: "sparkles")
                    }
                    .disabled(site.archived || model.busy != nil)
                    .help("Start your AI agent (Claude Code, Codex, Cursor…) on this site; Covo keeps its starting state")
                    Button {
                        openWindow(id: "logs", value: site.name)
                    } label: {
                        Label("Logs", systemImage: "doc.text.magnifyingglass")
                    }
                    .help("PHP errors and requests of this site")
                    Button {
                        open(site.url)
                    } label: {
                        if let b = ToolApp.browser {
                            Label { Text("Open") } icon: { Image(nsImage: b.icon) }
                        } else {
                            Label("Open", systemImage: "safari")
                        }
                    }
                        .keyboardShortcut("o")
                        .disabled(!model.running || !site.enabled)
                        .help(!site.enabled ? "Turn the site on first" : model.running ? "Open in your browser" : "Start Covo first")
                }
            }

            Section("Tools") {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 116), spacing: 8)], spacing: 8) {
                    ToolTile(title: "Snapshots", symbol: "clock.arrow.circlepath",
                             help: "Save and restore the files and the database.") { snapshotsSheet = true }
                    ToolTile(title: "Open on Phone", symbol: "iphone",
                             help: "This site on your Wi-Fi for a while, with a QR code.",
                             active: model.status?.phone?.site == site.name) { phoneSheet = true }
                        .disabled(site.archived)
                    ToolTile(title: "Production", symbol: "arrow.up.arrow.down",
                             help: "Pull the live site's database and uploads; push to staging.") { remoteSheet = true }
                    ToolTile(title: toolTitle, symbol: "apple.terminal",
                             help: "Commands with this site's PHP version, in its project folder.") { runningTool = true }
                    ToolTile(title: "PHP Check", symbol: "checkmark.seal",
                             help: "What in the code would break on a newer PHP version.") { checkingPHP = true }
                    if site.framework == "wordpress" {
                        ToolTile(title: "Blueprints", symbol: "square.stack.3d.up",
                                 help: "Install a blueprint's themes and plugins, or save this site as one.") { blueprintSheet = true }
                    }
                }
                .padding(.vertical, 2)
            }

            Section {
                DisclosureGroup("Advanced", isExpanded: $advanced) {
                    LabeledContent("Domain") {
                        HStack {
                            TextField("Domain", text: $domain, prompt: Text(site.domain))
                                .labelsHidden()
                                .multilineTextAlignment(.trailing)
                                .onSubmit(saveDomain)
                            Button("Save", action: saveDomain)
                                .disabled(domain.isEmpty || domain == site.domain || model.busy != nil)
                        }
                    }
                    LabeledContent("Aliases") {
                        VStack(alignment: .trailing, spacing: 4) {
                            ForEach(site.aliases, id: \.self) { alias in
                                HStack {
                                    Text(alias).textSelection(.enabled)
                                    Button {
                                        Task { await model.setAliases(site, site.aliases.filter { $0 != alias }) }
                                    } label: {
                                        Image(systemName: "minus.circle.fill")
                                    }
                                    .buttonStyle(.borderless)
                                    .help("Remove alias")
                                    .disabled(model.busy != nil)
                                }
                            }
                            HStack {
                                TextField("New alias", text: $alias, prompt: Text("www.\(site.domain)"))
                                    .labelsHidden()
                                    .multilineTextAlignment(.trailing)
                                    .onSubmit(addAlias)
                                Button("Add", action: addAlias)
                                    .disabled(alias.trimmingCharacters(in: .whitespaces).isEmpty || model.busy != nil)
                            }
                        }
                    }
                    Text(domainHint).font(.caption).foregroundStyle(.secondary)
                    LabeledContent("PHP settings") {
                        Button("Edit…") { editingPHP = true }
                            .disabled(model.busy != nil)
                    }
                    if let x = model.xdebug(for: site.php), x.available {
                        Toggle(isOn: Binding(
                            get: { x.enabled },
                            set: { on in Task { await model.setXdebug(php: site.php, enabled: on) } }
                        )) {
                            Text("Xdebug")
                            Text("For every site on PHP \(site.php). Your IDE listens on port 9003.")
                        }
                        .disabled(model.busy != nil)
                    }
                    if let m = site.migration {
                        LabeledContent {
                            Button("Undo Migration…") { undoingMigration = true }
                                .disabled(model.busy != nil)
                        } label: {
                            Text("Migrated from \(m.source)")
                            Text("Database \(m.database)\(m.urlFrom.map { " · was \($0)" } ?? "")")
                        }
                        ExplainBox(
                            title: "This site was moved from \(m.source)",
                            steps: [
                                "Covo copied its database into Covo's MySQL (\(m.database)); the original in \(m.source) was only read.",
                                "\(m.configPath.map { ($0 as NSString).lastPathComponent } ?? "The configuration") now points at Covo's copy; the original is backed up in Covo's folder.",
                            ],
                            caution: "The folder is shared with \(m.source): opened there, this site does not find its database until you undo the migration."
                        )
                        .confirmationDialog("Undo the migration of \(site.name)?", isPresented: $undoingMigration) {
                            Button("Undo Migration", role: .destructive) { Task { await model.undoMigration(site) } }
                        } message: {
                            Text(m.createdDatabase
                                 ? "\(m.configPath.map { ($0 as NSString).lastPathComponent } ?? "The configuration") is restored from its backup and Covo's copy of the database \(m.database) is deleted, with any change made since."
                                 : "\(m.configPath.map { ($0 as NSString).lastPathComponent } ?? "The configuration") is restored from its backup; the database is kept.")
                        }
                    }
                    HStack {
                        Text("Archive keeps everything but stops serving the site.")
                            .font(.caption).foregroundStyle(.secondary)
                        Spacer()
                        if !site.archived {
                            Button("Archive") { Task { await model.setArchived(site, true) } }
                                .help("Put the site away: not served, listed apart; nothing is deleted")
                                .disabled(model.busy != nil)
                        }
                        Button("Remove Site…", role: .destructive) { model.removing = site }
                            .disabled(model.busy != nil)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .onAppear { domain = site.domain }
        .onChange(of: site.domain) { _, new in domain = new }
        .sheet(isPresented: $editingPHP) { SitePHPSheet(site: site) }
        .sheet(isPresented: $checkingPHP) { PHPCheckSheet(site: site) }
        .sheet(isPresented: $runningTool) { ToolSheet(site: site) }
        .sheet(isPresented: $phoneSheet) { PhoneSheet(site: site) }
        .sheet(isPresented: $snapshotsSheet) { SnapshotsSheet(site: site) }
        .sheet(isPresented: $blueprintSheet) { BlueprintSheet(site: site) }
        .sheet(isPresented: $remoteSheet) { RemoteSheet(site: site) }
        .sheet(isPresented: $agentSheet) { WorkWithAgentSheet(site: site) }
        .fileImporter(isPresented: $choosingFolder, allowedContentTypes: [.folder]) { result in
            if case .success(let url) = result { Task { await changeFolder(url, force: false) } }
        }
        .alert("No index file", isPresented: Binding(get: { pendingFolder != nil }, set: { if !$0 { pendingFolder = nil } })) {
            Button("Use Anyway") {
                if let url = pendingFolder { Task { await changeFolder(url, force: true) } }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("\(pendingFolder?.lastPathComponent ?? "This folder") has no index.php or index.html. Use it anyway?")
        }
    }

    /// Every PHP version Covo can serve, plus the site's own if it is older.
    private var phpChoices: [String] {
        var minors = model.php?.minors ?? []
        if !minors.contains(site.php) { minors.append(site.php) }
        return minors
    }

    private var domainHint: String {
        var suffixes = [".localhost"]
        if model.domainEnabled("test") { suffixes.append(".test") }
        if model.domainEnabled("local") { suffixes.append(".local") }
        return "Domain and aliases must end in \(suffixes.joined(separator: ", ")). More suffixes in Settings → Domains."
    }

    private func addAlias() {
        let a = alias.trimmingCharacters(in: .whitespaces).lowercased()
        guard !a.isEmpty else { return }
        Task {
            await model.setAliases(site, site.aliases + [a])
            if model.error == nil { alias = "" }
        }
    }

    private func saveDomain() {
        let d = domain.trimmingCharacters(in: .whitespaces).lowercased()
        guard !d.isEmpty, d != site.domain else { return }
        Task { await model.updateSiteShowingErrors(site, domain: d) }
    }

    private func changeFolder(_ url: URL, force: Bool) async {
        do {
            try await model.updateSite(site, path: url.path, force: force)
        } catch let e as APIError where e.code == "confirmation_required" {
            pendingFolder = url
        } catch {
            model.error = error.localizedDescription
        }
    }
}

/// One of the site's tools: an icon over a short name, the description on hover.
private struct ToolTile: View {
    let title: String
    let symbol: String
    let help: String
    var active = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 6) {
                Image(systemName: symbol)
                    .font(.title3)
                    .foregroundStyle(active ? Color.green : Color.accentColor)
                    .frame(height: 22)
                Text(title)
                    .font(.caption.weight(.medium))
                    .lineLimit(2)
                    .multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity, minHeight: 58)
            .padding(.vertical, 6)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
            .overlay {
                if active { RoundedRectangle(cornerRadius: 8).strokeBorder(.green.opacity(0.6)) }
            }
            .contentShape(RoundedRectangle(cornerRadius: 8))
        }
        .buttonStyle(.plain)
        .help(active ? "\(help) Showing now." : help)
    }
}
