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
    }

    private var split: some View {
        NavigationSplitView {
            List(model.sites, selection: $selection) { site in
                SiteRow(site: site)
            }
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
                } label: {
                    Label("Add Site", systemImage: "plus")
                }
                .help("Add a site")
                .disabled(model.busy != nil)
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

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(site.name)
                Text(site.domain).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            Text("PHP \(site.php)").font(.caption).foregroundStyle(.secondary)
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
        .opacity(site.enabled ? 1 : 0.55)
        .contextMenu {
            Button("Open in Browser") { open(site.url) }
            Button("Show in Finder") { NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: site.path) }
            Button("Logs") { openWindow(id: "logs", value: site.name) }
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
