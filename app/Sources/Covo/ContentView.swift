// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI
import UniformTypeIdentifiers

struct ContentView: View {
    @Environment(AppModel.self) private var model
    @State private var selection: Site.ID?
    @State private var adding: AddSiteRequest?
    @State private var dropTargeted = false
    @State private var newWordPress = false
    @State private var search = ""
    @State private var duplicating: Site?
    @State private var showArchived = false

    /// Sites whose name, domain, alias or folder match the search.
    private var filtered: [Site] {
        let q = search.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return model.sites }
        return model.sites.filter { s in
            ([s.name, s.domain, s.path, s.database] + s.aliases).contains { $0.localizedCaseInsensitiveContains(q) }
        }
    }

    var body: some View {
        @Bindable var model = model
        Group {
            switch model.phase {
            case .connecting:
                ProgressView("Starting Covo…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .failed(let message):
                ContentUnavailableView {
                    Label("Covo could not start", systemImage: "exclamationmark.triangle")
                } description: {
                    Text(message)
                } actions: {
                    Button("Try Again") { Task { await model.connect() } }
                }
            case .ready:
                split
            }
        }
        .alert("Something went wrong", isPresented: Binding(get: { model.error != nil }, set: { if !$0 { model.error = nil } })) {
            Button("OK") { model.error = nil }
        } message: {
            Text(model.error ?? "")
        }
        .sheet(item: $adding) { req in
            AddSiteSheet(request: req) { name in selection = name }
        }
        .sheet(isPresented: $newWordPress) {
            WordPressSheet { name in selection = name }
        }
        .sheet(isPresented: $model.importing) { ImportSheet() }
        .sheet(item: $model.removing) { site in RemoveSiteSheet(site: site) }
        .sheet(item: $duplicating) { site in
            DuplicateSheet(site: site) { name in selection = name }
        }
    }

    private var split: some View {
        NavigationSplitView {
            List(selection: $selection) {
                ForEach(filtered.filter { !$0.archived }) { site in
                    SiteRow(site: site) { duplicating = site }
                }
                let archived = filtered.filter(\.archived)
                if !archived.isEmpty {
                    Section(isExpanded: $showArchived) {
                        ForEach(archived) { site in
                            SiteRow(site: site) { duplicating = site }
                        }
                    } header: {
                        Text("Archived (\(archived.count))")
                    }
                }
            }
            .searchable(text: $search, placement: .sidebar, prompt: "Search sites")
            .overlay {
                if model.sites.isEmpty {
                    ContentUnavailableView {
                        Label("No Sites", systemImage: "folder.badge.plus")
                    } description: {
                        Text("Drag a folder here, or click +.")
                    }
                }
            }
            .navigationSplitViewColumnWidth(min: 200, ideal: 240)
            .dropDestination(for: URL.self) { urls, _ in
                guard let url = urls.first, url.hasDirectoryPath else { return false }
                adding = AddSiteRequest(folder: url)
                return true
            } isTargeted: { dropTargeted = $0 }
            .overlay {
                if dropTargeted {
                    RoundedRectangle(cornerRadius: 8).strokeBorder(.tint, lineWidth: 2).padding(4)
                }
            }
            .safeAreaInset(edge: .bottom) {
                if let status = model.status, !status.ca.trusted {
                    TrustBanner()
                }
            }
        } detail: {
            if let site = model.sites.first(where: { $0.id == selection }) {
                SiteDetail(site: site).id(site.id)
            } else {
                ContentUnavailableView("Select a Site", systemImage: "globe")
            }
        }
        .toolbar {
            ToolbarItem(placement: .navigation) {
                StartStopButton()
            }
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    Button("Add Folder…") { adding = AddSiteRequest(folder: nil) }
                    Button("New WordPress Site…") { newWordPress = true }
                    Divider()
                    Button("Import from MAMP PRO…") { model.importing = true }
                } label: {
                    Label("Add Site", systemImage: "plus")
                }
                .help("Add a site")
                .disabled(model.busy != nil)
            }
            ToolbarItem(placement: .primaryAction) {
                Button {
                    Task { await model.openAdminer() }
                } label: {
                    Label("Adminer", systemImage: "cylinder.split.1x2")
                }
                .help(model.mysqlReady && model.running
                      ? "Open Adminer with every database, already logged in"
                      : "Adminer needs MySQL on and Covo started")
                .disabled(!model.mysqlReady || !model.running)
            }
            ToolbarItem(placement: .primaryAction) {
                UpdatesBadge()
            }
            if let mail = model.status?.mail, mail.enabled, let u = mail.url, let url = URL(string: u) {
                ToolbarItem(placement: .primaryAction) {
                    Button {
                        NSWorkspace.shared.open(url)
                    } label: {
                        Label("Mail", systemImage: "envelope")
                    }
                    .help("Open the inbox of the mail your sites sent")
                    .disabled(!mail.running)
                }
            }
            ToolbarItem(placement: .primaryAction) {
                SettingsLink {
                    Label("Settings", systemImage: "gearshape")
                }
                .help("Settings (⌘,)")
            }
        }
        .hidingWindowTitle()
        .onChange(of: model.sites) { _, sites in
            if selection == nil || !sites.contains(where: { $0.id == selection }) {
                selection = sites.first?.id
            }
        }
        .onAppear { selection = selection ?? model.sites.first?.id }
    }
}

struct SiteRow: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    let site: Site
    let duplicate: () -> Void

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(site.name)
                Text(site.domain).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            Text("PHP \(site.php)").font(.caption).foregroundStyle(.secondary)
            if site.archived {
                Image(systemName: "archivebox").foregroundStyle(.secondary).help("Archived")
            } else {
                Toggle("On", isOn: Binding(
                    get: { site.enabled },
                    set: { on in Task { await model.setEnabled(site, on) } }
                ))
                .labelsHidden()
                .toggleStyle(.switch)
                .controlSize(.mini)
                .disabled(model.busy != nil)
                .help(site.enabled ? "Stop serving this site" : "Serve this site")
            }
        }
        .opacity(site.enabled ? 1 : 0.55)
        .contextMenu {
            SiteActions(site: site)
            Divider()
            OpenWithItems(folder: site.path)
            Divider()
            Button(action: duplicate) {
                Label("Duplicate…", systemImage: "plus.square.on.square")
            }
            if site.archived {
                Button {
                    Task { await model.setArchived(site, false) }
                } label: {
                    Label("Unarchive", systemImage: "tray.and.arrow.up")
                }
            } else {
                Button {
                    Task { await model.setArchived(site, true) }
                } label: {
                    Label("Archive", systemImage: "archivebox")
                }
            }
            Divider()
            Button(role: .destructive) {
                model.removing = site
            } label: {
                Label("Remove Site…", systemImage: "trash")
            }
            .disabled(model.busy != nil)
        }
    }
}

/// The one global control: starts or stops every site at once. Status and
/// action live inside a single toolbar button so nothing spills out of it.
struct StartStopButton: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        Button {
            Task { await model.toggleRunning() }
        } label: {
            HStack(spacing: 6) {
                if let busy = model.busy {
                    ProgressView().controlSize(.small)
                    Text(busy)
                } else {
                    Image(systemName: model.running ? "stop.fill" : "play.fill")
                    Circle()
                        .fill(model.running ? Color.green : Color.secondary.opacity(0.5))
                        .frame(width: 7, height: 7)
                    Text(model.running ? "All Sites Running" : "All Sites Stopped")
                }
            }
            .fixedSize()
            .padding(.horizontal, 4)
        }
        .help(model.running ? "Stop every site" : "Start every site")
        .disabled(model.busy != nil)
    }
}

struct TrustBanner: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label("HTTPS certificate not trusted", systemImage: "lock.trianglebadge.exclamationmark")
                .font(.callout.weight(.medium))
            Text("Browsers will warn on your sites until macOS trusts Covo's local certificate.")
                .font(.caption).foregroundStyle(.secondary)
            Button("Trust…") { Task { await model.trust() } }
                .disabled(model.busy != nil)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
        .padding(8)
    }
}

func open(_ url: String) {
    if let u = URL(string: url) { NSWorkspace.shared.open(u) }
}

extension View {
    /// The window title would sit next to the global Start/Stop control and
    /// read like the selected site's name; the site is named in the detail.
    @ViewBuilder func hidingWindowTitle() -> some View {
        if #available(macOS 15.0, *) {
            toolbar(removing: .title)
        } else {
            navigationTitle("")
        }
    }
}
