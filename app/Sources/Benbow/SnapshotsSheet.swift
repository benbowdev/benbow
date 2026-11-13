// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import BenbowKit
import SwiftUI

/// A site's snapshots: take, restore (the current state is saved first),
/// delete.
struct SnapshotsSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site

    @State private var list: [Snapshot] = []
    @State private var note = ""
    @State private var restoring: Snapshot?
    @State private var restorePHP = false
    /// Real size by snapshot id (what deleting it gives back).
    @State private var sizes: [String: Int64] = [:]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Snapshots of \(site.name)").font(.headline)
            Text("A snapshot saves the project folder (without .git and node_modules), the database and the PHP version. Files are APFS clones: no extra space until they change.")
                .font(.callout).foregroundStyle(.secondary)
            HStack {
                TextField("Note", text: $note, prompt: Text("What is it for? (optional)"))
                Button("Take Snapshot") {
                    Task {
                        await model.createSnapshot(site, note: note)
                        note = ""
                        await reload()
                    }
                }
                .disabled(model.busy != nil)
            }
            List(list) { s in
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(s.createdAt.formatted(date: .abbreviated, time: .standard))
                        Text([kindTitle(s.kind), s.note, s.database.map { "database \($0)" }, s.php.map { "PHP \($0)" }]
                            .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    if let size = sizes[s.id] {
                        Text(size.formatted(.byteCount(style: .file))).font(.callout).monospacedDigit().foregroundStyle(.secondary)
                            .help("Space given back by deleting it: the database dump and the files that changed since")
                    }
                    Button("Restore…") { restoring = s }.disabled(model.busy != nil)
                    Button {
                        Task { await model.deleteSnapshot(site, s); await reload() }
                    } label: { Image(systemName: "trash") }
                    .buttonStyle(.borderless)
                    .disabled(model.busy != nil)
                    .help("Delete this snapshot")
                }
            }
            .overlay { if list.isEmpty { Text("No snapshots yet").foregroundStyle(.secondary) } }
            HStack {
                if let busy = model.busy { ProgressView().controlSize(.small); Text(busy).foregroundStyle(.secondary) }
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
            }
        }
        .padding(20)
        .frame(width: 600, height: 460)
        .task { await reload() }
        .sheet(item: $restoring) { s in
            RestoreSnapshotSheet(site: site, snapshot: s) { php in
                Task { await model.restoreSnapshot(site, s, php: php); await reload() }
            }
        }
    }

    private func reload() async {
        list = await model.snapshots(site)
        if let u = await model.snapshotUsage(site) {
            sizes = Dictionary(u.snapshots.map { ($0.id, $0.privateBytes) }, uniquingKeysWith: { a, _ in a })
        }
    }

    private func kindTitle(_ kind: String) -> String {
        switch kind {
        case "pre-restore": "Before a restore"
        case "pre-pull": "Before a pull"
        case "mcp": "Before an AI agent change"
        default: "Manual"
        }
    }
}

/// Confirms a restore; offers to bring back the PHP version and php.ini
/// values when the snapshot saved different ones.
struct RestoreSnapshotSheet: View {
    @Environment(\.dismiss) private var dismiss
    let site: Site
    let snapshot: Snapshot
    let restore: (Bool) -> Void
    @State private var php = false

    private var phpDiffers: Bool {
        guard let p = snapshot.php else { return false }
        return p != site.php || (snapshot.phpSettings ?? [:]) != (site.phpSettings ?? [:])
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Restore \(site.name) to \(snapshot.createdAt.formatted(date: .abbreviated, time: .shortened))?").font(.headline)
            Text("The project files and the database go back to that moment. The current state is saved as a new snapshot first, so this can be undone.")
                .font(.callout).foregroundStyle(.secondary)
            if phpDiffers, let p = snapshot.php {
                Toggle(isOn: $php) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(p == site.php ? "Also bring back its php.ini values" : "Also go back to PHP \(p) and its php.ini values")
                        Text("The site now runs PHP \(site.php)\(p == site.php ? " with other php.ini values" : "").")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
                .toggleStyle(.benbowSwitch)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Restore", role: .destructive) { restore(php); dismiss() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 460)
    }
}
