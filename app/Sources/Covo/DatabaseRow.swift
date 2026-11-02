// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// The Database line of the site detail: create, link, open, unlink.
struct DatabaseRow: View {
    @Environment(AppModel.self) private var model
    let site: Site
    @State private var linking = false
    @State private var credentials: SiteDatabase?
    @State private var confirmDelete = false

    var body: some View {
        LabeledContent("Database") {
            if site.database.isEmpty {
                none
            } else {
                linked
            }
        }
        .sheet(isPresented: $linking) { LinkDatabaseSheet(site: site) }
        .popover(item: $credentials, arrowEdge: .trailing) { CredentialsView(db: $0) }
        .confirmationDialog("Delete the database \(site.database)?", isPresented: $confirmDelete) {
            Button("Delete Database", role: .destructive) { Task { await model.unlinkDatabase(site, drop: true) } }
        } message: {
            Text("All its tables and data are deleted, with the site's MySQL user. This cannot be undone.")
        }
    }

    @ViewBuilder private var none: some View {
        if model.mysqlReady {
            HStack {
                Text("None").foregroundStyle(.secondary)
                Menu("Add") {
                    Button("Create Database \(defaultName)") { Task { await model.createDatabase(site) } }
                    Button("Link Existing Database…") { linking = true }
                }
                .fixedSize()
                .disabled(model.busy != nil)
            }
        } else {
            Text(model.status?.mysql.enabled == true ? "None (start Covo to add one)" : "None (turn MySQL on in Settings)")
                .foregroundStyle(.secondary)
        }
    }

    private var linked: some View {
        HStack {
            VStack(alignment: .trailing, spacing: 1) {
                Text(site.database).textSelection(.enabled)
                Text("user \(site.databaseUser ?? "root")").font(.caption).foregroundStyle(.secondary)
            }
            Button("Adminer") { Task { await model.openAdminer(site) } }
                .help("Open this database in Adminer, already logged in")
                .disabled(!model.mysqlReady || !model.running)
            Menu {
                Button("Show Credentials…") { Task { credentials = await model.siteDatabase(site) } }
                Button("Copy Password") {
                    Task { if let d = await model.siteDatabase(site) { copyToClipboard(d.password ?? "") } }
                }
                Button("Copy wp-config.php Settings") {
                    Task { if let d = await model.siteDatabase(site) { copyToClipboard(d.wpConfig) } }
                }
                Divider()
                Button("Unlink (Keep Data)") { Task { await model.unlinkDatabase(site, drop: false) } }
                Button("Delete Database…", role: .destructive) { confirmDelete = true }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .disabled(model.busy != nil)
        }
    }

    private var defaultName: String { site.name.replacingOccurrences(of: "-", with: "_") }
}

struct LinkDatabaseSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    @State private var all: [String] = []
    @State private var choice: String?
    @State private var filter = ""

    private var free: [String] {
        let used = Set(model.sites.map(\.database))
        return all.filter { !used.contains($0) && (filter.isEmpty || $0.localizedCaseInsensitiveContains(filter)) }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Link a database to \(site.name)").font(.headline)
            Text("The site connects as root, like with MAMP. Databases already used by a site are not listed.")
                .font(.callout).foregroundStyle(.secondary)
            TextField("Filter", text: $filter)
            List(free, id: \.self, selection: $choice) { Text($0) }
                .frame(minHeight: 220)
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                Button("Link") {
                    guard let db = choice else { return }
                    Task {
                        await model.linkDatabase(site, db)
                        dismiss()
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(choice == nil)
            }
        }
        .padding()
        .frame(width: 420)
        .task { all = await model.databases() }
    }
}

struct CredentialsView: View {
    let db: SiteDatabase

    var body: some View {
        Form {
            row("Database", db.database)
            row("User", db.user)
            row("Password", db.password ?? "", secret: true)
            row("Host", "localhost (or \(db.host):\(db.port))")
            row("Socket", db.socket)
            HStack {
                Spacer()
                Button("Copy wp-config.php Settings") { copyToClipboard(db.wpConfig) }
            }
        }
        .formStyle(.grouped)
        .frame(width: 440)
    }

    private func row(_ title: String, _ value: String, secret: Bool = false) -> some View {
        LabeledContent(title) {
            HStack {
                Text(secret ? String(repeating: "•", count: 12) : value)
                    .lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                Button {
                    copyToClipboard(secret ? value : value.components(separatedBy: " (").first ?? value)
                } label: {
                    Image(systemName: "doc.on.doc")
                }
                .buttonStyle(.borderless)
                .help("Copy")
            }
        }
    }
}

extension SiteDatabase: Identifiable {
    public var id: String { site }
}
