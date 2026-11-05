// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI
import UniformTypeIdentifiers

struct ContentView: View {
    @Environment(AppModel.self) private var model
    @State private var selection: Site.ID?
    @State private var adding: NewSiteRequest?
    @State private var dropTargeted = false
    @State private var naming: GroupNaming?
    @State private var siteTitle = ""
    @State private var groupName = ""
    @AppStorage("collapsedGroups") private var collapsedStore = ""
    @State private var search = ""
    @State private var duplicating: Site?
    @State private var showArchived = false
    @State private var phoneSite: Site?

    /// Sites whose name, domain, alias or folder match the search.
    private var filtered: [Site] {
        let q = search.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return model.sites }
        return model.sites.filter { s in
            ([s.name, s.title ?? "", s.domain, s.path, s.database] + s.aliases).contains { $0.localizedCaseInsensitiveContains(q) }
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
            NewSiteSheet(request: req) { name in reveal(name) }
        }
        .alert("Title for “\(model.titling?.name ?? "")”", isPresented: Binding(get: { model.titling != nil }, set: { if !$0 { model.titling = nil } })) {
            TextField("Title", text: $siteTitle, prompt: Text(model.titling?.name ?? ""))
            Button("Save") {
                // Read the text now: closing the alert clears it.
                let title = siteTitle
                if let site = model.titling { Task { await model.setTitle(site, title) } }
                model.titling = nil
            }
            Button("Cancel", role: .cancel) { model.titling = nil }
        } message: {
            Text("Shown in the list instead of the name. The name and the address do not change. Empty: the name.")
        }
        .onChange(of: model.titling) { _, site in siteTitle = site?.title ?? "" }
        .alert(naming?.title ?? "", isPresented: Binding(get: { naming != nil }, set: { if !$0 { naming = nil } })) {
            TextField("Name", text: $groupName)
            Button(naming?.action ?? "OK") { commitGroupName() }
                .disabled(groupName.trimmingCharacters(in: .whitespaces).isEmpty)
            Button("Cancel", role: .cancel) { naming = nil }
        } message: {
            Text("Groups only organize the list: sites are served the same way.")
        }
        .sheet(isPresented: $model.importing) { ImportSheet() }
        .sheet(item: $model.removing) { site in RemoveSiteSheet(site: site) }
        .sheet(item: $phoneSite) { site in PhoneSheet(site: site) }
        .sheet(item: $duplicating) { site in
            DuplicateSheet(site: site) { name in selection = name }
        }
    }

    private var split: some View {
        NavigationSplitView {
            List(selection: $selection) {
                if searching {
                    ForEach(filtered.filter { !$0.archived }) { site in
                        siteRow(site, showGroup: true)
                    }
                } else {
                    GroupTree(parent: nil, sites: model.sites.filter { !$0.archived }, collapsed: collapsedBinding,
                              actions: sidebarActions)
                }
                let archived = filtered.filter(\.archived)
                if !archived.isEmpty {
                    Section(isExpanded: $showArchived) {
                        ForEach(archived) { site in
                            siteRow(site, showGroup: true)
                        }
                    } header: {
                        Text("Archived (\(archived.count))")
                    }
                }
            }
            .dropDestination(for: String.self) { items, _ in
                sidebarActions.drop(items, nil)
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
                adding = NewSiteRequest(source: .existing, folder: url)
                return true
            } isTargeted: { dropTargeted = $0 }
            .overlay {
                if dropTargeted {
                    RoundedRectangle(cornerRadius: 8).strokeBorder(.tint, lineWidth: 2).padding(4)
                }
            }
            .safeAreaInset(edge: .bottom, spacing: 0) {
                VStack(spacing: 0) {
                    if let status = model.status, !status.ca.trusted {
                        TrustBanner()
                    }
                    Divider()
                    HStack(spacing: 0) {
                        Menu {
                            Button("New Site…") { adding = NewSiteRequest() }
                            Button("New WordPress Site…") { adding = NewSiteRequest(source: .wordpress) }
                            Button("New Laravel Project…") { adding = NewSiteRequest(source: .laravel) }
                            Button("New Group…") { askGroupName(.create(parent: nil)) }
                            Divider()
                            Button("Import from Another Tool…") { model.importing = true }
                        } label: {
                            Image(systemName: "plus").frame(width: 24, height: 20)
                        }
                        .menuStyle(.borderlessButton)
                        .menuIndicator(.hidden)
                        .fixedSize()
                        .help("Add a site, a group, or import")
                        .disabled(model.busy != nil)
                        Spacer()
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 6)
                }
                .background(.bar)
            }
        } detail: {
            Group {
                if let site = model.sites.first(where: { $0.id == selection }) {
                    SiteDetail(site: site).id(site.id)
                } else {
                    ContentUnavailableView("Select a Site", systemImage: "globe")
                }
            }
            .toolbar { trailingToolbar }
        }
        .toolbar {
            ToolbarItem(placement: .navigation) {
                StartStopButton()
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

    /// Right side of the window: databases, updates, phone, mail, settings.
    @ToolbarContentBuilder private var trailingToolbar: some ToolbarContent {
        if #available(macOS 26.0, *) {
            ToolbarSpacer(.flexible)
        }
        ToolbarItem(placement: .automatic) {
            Button {
                Task { await model.openAdminer() }
            } label: {
                Label("Adminer", systemImage: "cylinder.split.1x2")
            }
            .help((model.mysqlReady || model.postgresReady) && model.running
                  ? "Open Adminer with every database, already logged in"
                  : "Adminer needs MySQL or PostgreSQL on and Covo started")
            .disabled(!(model.mysqlReady || model.postgresReady) || !model.running)
        }
        ToolbarItem(placement: .automatic) {
            AppUpdateBadge()
        }
        ToolbarItem(placement: .automatic) {
            UpdatesBadge()
        }
        if let phone = model.status?.phone {
            ToolbarItem(placement: .automatic) {
                Button {
                    phoneSite = model.sites.first { $0.name == phone.site }
                } label: {
                    Label("\(phone.site) on phone (\(phone.minutesLeft) min)", systemImage: "iphone.radiowaves.left.and.right")
                }
                .labelStyle(.titleAndIcon)
                .help("\(phone.site) is open on your local network: \(phone.url)")
            }
        }
        if let mail = model.status?.mail, mail.enabled, let u = mail.url, let url = URL(string: u) {
            ToolbarItem(placement: .automatic) {
                Button {
                    NSWorkspace.shared.open(url)
                } label: {
                    Label("Mail", systemImage: "envelope")
                }
                .help("Open the inbox of the mail your sites sent")
                .disabled(!mail.running)
            }
        }
        ToolbarItem(placement: .automatic) {
            SettingsLink {
                Label("Settings", systemImage: "gearshape")
            }
            .help("Settings (⌘,)")
        }
    }
}

extension ContentView {
    private var searching: Bool { !search.trimmingCharacters(in: .whitespaces).isEmpty }

    private func siteRow(_ site: Site, showGroup: Bool = false) -> some View {
        SiteRow(site: site, groupPath: showGroup ? model.groups.first { $0.id == site.groupId }?.path : nil,
                duplicate: { duplicating = site })
    }

    /// Groups the user collapsed (groups start expanded).
    private var collapsedBinding: Binding<Set<Int64>> {
        Binding(
            get: { Set(collapsedStore.split(separator: ",").compactMap { Int64($0) }) },
            set: { collapsedStore = $0.sorted().map(String.init).joined(separator: ",") }
        )
    }

    private var sidebarActions: SidebarActions {
        SidebarActions(
            newSite: { group in adding = NewSiteRequest(group: group) },
            newGroup: { parent in askGroupName(.create(parent: parent)) },
            rename: { g in askGroupName(.rename(g)) },
            duplicate: { site in duplicating = site },
            drop: { items, target in
                let groups = model.groups
                var handled = false
                for item in items {
                    if let name = item.removingPrefix("covo-site:"), let site = model.sites.first(where: { $0.name == name }) {
                        handled = true
                        Task { await model.moveSite(site, into: target) }
                    } else if let raw = item.removingPrefix("covo-group:"), let id = Int64(raw),
                              let g = groups.first(where: { $0.id == id }) {
                        handled = true
                        Task { await model.moveGroup(g, into: target) }
                    }
                }
                if let target { expand(target) }
                return handled
            },
            expand: { expand($0) }
        )
    }

    private func askGroupName(_ n: GroupNaming) {
        if case .rename(let g) = n { groupName = g.name } else { groupName = "" }
        naming = n
    }

    private func commitGroupName() {
        guard let n = naming else { return }
        let name = groupName.trimmingCharacters(in: .whitespaces)
        naming = nil
        Task {
            switch n {
            case .create(let parent):
                if let parent { expand(parent) }
                await model.addGroup(name, parent: parent)
            case .rename(let g):
                await model.renameGroup(g, to: name)
            }
        }
    }

    private func expand(_ id: Int64) {
        var c = collapsedBinding.wrappedValue
        var cur: Int64? = id
        while let g = cur {
            c.remove(g)
            cur = model.groups.first { $0.id == g }?.parentId
        }
        collapsedBinding.wrappedValue = c
    }

    /// Selects a new site, opening the groups it is in.
    private func reveal(_ name: String) {
        if let g = model.sites.first(where: { $0.name == name })?.groupId { expand(g) }
        selection = name
    }
}

enum GroupNaming {
    case create(parent: Int64?)
    case rename(SiteGroup)

    var title: String {
        switch self {
        case .create: "New Group"
        case .rename(let g): "Rename “\(g.name)”"
        }
    }

    var action: String {
        switch self {
        case .create: "Create"
        case .rename: "Rename"
        }
    }
}

/// What the sidebar rows can ask of the window.
struct SidebarActions {
    var newSite: (Int64?) -> Void
    var newGroup: (Int64?) -> Void
    var rename: (SiteGroup) -> Void
    var duplicate: (Site) -> Void
    /// Dropped "covo-site:<name>" / "covo-group:<id>" items into a group
    /// (nil: top level). Returns whether anything moved.
    var drop: ([String], Int64?) -> Bool
    /// Opens a group (and the groups it is in).
    var expand: (Int64) -> Void
}

/// One level of the site list: its groups (expandable, nested to any
/// depth), then its sites.
struct GroupTree: View {
    @Environment(AppModel.self) private var model
    let parent: Int64?
    let sites: [Site]
    @Binding var collapsed: Set<Int64>
    let actions: SidebarActions

    var body: some View {
        let nodes = model.level(parent, sites: sites)
        ForEach(nodes) { node in
            switch node {
            case .group(let g):
                DisclosureGroup(isExpanded: Binding(
                    get: { !collapsed.contains(g.id) },
                    set: { if $0 { collapsed.remove(g.id) } else { collapsed.insert(g.id) } }
                )) {
                    GroupTree(parent: g.id, sites: sites, collapsed: $collapsed, actions: actions)
                } label: {
                    // Groups are not selected: the detail shows sites.
                    GroupRow(group: g, sites: inside(g.id), actions: actions)
                        .selectionDisabled()
                }
            case .site(let site):
                SiteRow(site: site, groupPath: nil, duplicate: { actions.duplicate(site) })
                    // The list selects by site name (ForEach's id is "site:<name>").
                    .tag(site.id)
            }
        }
        // Dropped between rows: macOS draws the insertion line. Groups and
        // sites mix freely.
        .onInsert(of: [.plainText]) { index, providers in
            sidebarItems(providers) { items in
                Task { await model.insert(items, into: parent, at: index, shown: nodes) }
            }
        }
    }

    /// Sites in a group and its subgroups.
    private func inside(_ id: Int64) -> [Site] {
        let children = model.groups.filter { $0.parentId == id }.map(\.id)
        return sites.filter { $0.groupId == id } + children.flatMap { inside($0) }
    }
}

struct GroupRow: View {
    @Environment(AppModel.self) private var model
    let group: SiteGroup
    /// Its sites and its subgroups' (archived ones left out).
    let sites: [Site]
    let actions: SidebarActions
    @State private var targeted = false

    private var anyOn: Bool { sites.contains(where: \.enabled) }

    var body: some View {
        HStack {
            Label(group.name, systemImage: "folder")
            Spacer()
            Text("\(sites.count)").font(.caption).foregroundStyle(.secondary).monospacedDigit()
            if !sites.isEmpty {
                Toggle("On", isOn: Binding(
                    get: { anyOn },
                    set: { on in Task { await model.setGroupEnabled(group, on) } }
                ))
                .labelsHidden()
                .toggleStyle(.covoSwitch)
                .controlSize(.mini)
                .disabled(model.busy != nil)
                .help(anyOn ? "Stop serving every site in \(group.name)" : "Serve every site in \(group.name)")
            }
        }
        .padding(.vertical, 1)
        .background(targeted ? Color.accentColor.opacity(0.2) : .clear, in: RoundedRectangle(cornerRadius: 4))
        .draggable(SidebarItem.group(group.id).token)
        .dropDestination(for: String.self) { items, _ in
            actions.drop(items, group.id)
        } isTargeted: { targeted = $0 }
        .contextMenu {
            Button { actions.newSite(group.id) } label: { Label("New Site Here…", systemImage: "plus") }
            Button { actions.newGroup(group.id) } label: { Label("New Group Inside…", systemImage: "folder.badge.plus") }
            Divider()
            Button { Task { await model.setGroupEnabled(group, true) } } label: {
                Label("Turn All Sites On", systemImage: "play.circle")
            }
            .disabled(sites.allSatisfy(\.enabled))
            Button { Task { await model.setGroupEnabled(group, false) } } label: {
                Label("Turn All Sites Off", systemImage: "stop.circle")
            }
            .disabled(!anyOn)
            Divider()
            Button { actions.rename(group) } label: { Label("Rename…", systemImage: "pencil") }
            MoveToMenu(current: group.parentId, excluding: group) { target in
                Task { await model.moveGroup(group, into: target) }
            }
            Divider()
            Button(role: .destructive) {
                Task { await model.removeGroup(group) }
            } label: {
                Label("Remove Group (Keep Sites)", systemImage: "folder.badge.minus")
            }
            .help("Its sites and groups move up one level")
        }
    }
}

/// "Move To" submenu: the top level and every group (except a group and
/// its own subgroups when moving a group).
struct MoveToMenu: View {
    @Environment(AppModel.self) private var model
    let current: Int64?
    var excluding: SiteGroup?
    let move: (Int64?) -> Void

    var body: some View {
        Menu {
            Button("Top Level") { move(nil) }.disabled(current == nil)
            let targets = model.groups.filter { g in
                guard let ex = excluding else { return true }
                return g.id != ex.id && !g.path.hasPrefix(ex.path + "/")
            }
            if !targets.isEmpty { Divider() }
            ForEach(targets) { g in
                Button(String(repeating: "    ", count: g.path.filter { $0 == "/" }.count) + g.name) { move(g.id) }
                    .disabled(g.id == current)
            }
        } label: {
            Label("Move To", systemImage: "folder")
        }
        .disabled(model.busy != nil)
    }
}

extension String {
    func removingPrefix(_ p: String) -> String? {
        hasPrefix(p) ? String(dropFirst(p.count)) : nil
    }
}

struct SiteRow: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow
    let site: Site
    /// Shown under the name in search results.
    var groupPath: String?
    let duplicate: () -> Void

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(site.displayName)
                Text(groupPath.map { "\($0) · \(site.domain)" } ?? site.domain)
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if let session = model.session(for: site) {
                AgentBadge(session: session)
            }
            Text("PHP \(site.php)").font(.caption).foregroundStyle(.secondary)
            if site.archived {
                Image(systemName: "archivebox").foregroundStyle(.secondary).help("Archived")
            } else {
                Toggle("On", isOn: Binding(
                    get: { site.enabled },
                    set: { on in Task { await model.setEnabled(site, on) } }
                ))
                .labelsHidden()
                .toggleStyle(.covoSwitch)
                .controlSize(.mini)
                .disabled(model.busy != nil)
                .help(site.enabled ? "Stop serving this site" : "Serve this site")
            }
        }
        .opacity(site.enabled ? 1 : 0.55)
        .draggable(SidebarItem.site(site.name).token)
        .contextMenu {
            SiteActions(site: site)
            Divider()
            OpenWithItems(folder: site.path)
            Divider()
            Button {
                model.titling = site
            } label: {
                Label("Edit Title…", systemImage: "pencil")
            }
            Button(action: duplicate) {
                Label("Duplicate…", systemImage: "plus.square.on.square")
            }
            MoveToMenu(current: site.groupId) { target in
                Task { await model.moveSite(site, into: target) }
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
