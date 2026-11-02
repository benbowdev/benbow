// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

struct SiteDetail: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    let site: Site
    @State private var advanced = false
    @State private var confirmRemove = false
    @State private var choosingFolder = false
    /// A folder without an index file, waiting for "Use Anyway".
    @State private var pendingFolder: URL?
    @State private var domain = ""
    @State private var alias = ""
    @State private var editingPHP = false

    var body: some View {
        Form {
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
                LabeledContent("Name", value: site.name)
                LabeledContent("Folder") {
                    HStack {
                        Text(site.path).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                        Button {
                            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: site.path)
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
                            OpenWithItems(folder: site.path)
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
                }
                Picker("PHP", selection: Binding(
                    get: { site.php },
                    set: { minor in
                        guard minor != site.php else { return }
                        Task { await model.updateSiteShowingErrors(site, php: minor) }
                    }
                )) {
                    ForEach(phpChoices, id: \.self) { minor in
                        Text(model.installed(minor) ? minor : "\(minor) (download)").tag(minor)
                    }
                }
                .disabled(model.busy != nil)
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
                    HStack {
                        Spacer()
                        if !site.archived {
                            Button("Archive") { Task { await model.setArchived(site, true) } }
                                .help("Put the site away: not served, listed apart; nothing is deleted")
                                .disabled(model.busy != nil)
                        }
                        Button("Remove Site…", role: .destructive) { confirmRemove = true }
                            .disabled(model.busy != nil)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .onAppear { domain = site.domain }
        .onChange(of: site.domain) { _, new in domain = new }
        .sheet(isPresented: $editingPHP) { SitePHPSheet(site: site) }
        .confirmationDialog("Remove \(site.name)?", isPresented: $confirmRemove) {
            Button("Remove", role: .destructive) { Task { await model.removeSite(site) } }
        } message: {
            Text("Covo stops serving it. The folder \(site.path) is not touched.")
        }
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
