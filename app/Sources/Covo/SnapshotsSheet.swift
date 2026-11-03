// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
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

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Snapshots of \(site.name)").font(.headline)
            Text("A snapshot saves the project folder (without .git and node_modules) and the database. Files are APFS clones: no extra space until they change.")
                .font(.callout).foregroundStyle(.secondary)
            HStack {
                TextField("Note", text: $note, prompt: Text("What is it for? (optional)"))
                Button("Take Snapshot") {
                    Task {
                        await model.createSnapshot(site, note: note)
                        note = ""
                        list = await model.snapshots(site)
                    }
                }
                .disabled(model.busy != nil)
            }
            List(list) { s in
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(s.createdAt.formatted(date: .abbreviated, time: .standard))
                        Text([kindTitle(s.kind), s.note, s.database.map { "database \($0)" }]
                            .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Restore…") { restoring = s }.disabled(model.busy != nil)
                    Button {
                        Task { await model.deleteSnapshot(site, s); list = await model.snapshots(site) }
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
        .task { list = await model.snapshots(site) }
        .confirmationDialog("Restore \(site.name) to this snapshot?", isPresented: Binding(
            get: { restoring != nil }, set: { if !$0 { restoring = nil } })) {
            Button("Restore", role: .destructive) {
                if let s = restoring {
                    Task { await model.restoreSnapshot(site, s); list = await model.snapshots(site) }
                }
            }
        } message: {
            Text("The project files and the database go back to that moment. The current state is saved as a new snapshot first, so this can be undone.")
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
