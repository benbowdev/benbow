// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import BenbowKit
import SwiftUI

/// Settings → Snapshots: every site's snapshots with the space they really
/// take, deleting old ones, and how many to keep.
struct SnapshotsSettings: View {
    @Environment(AppModel.self) private var model
    @State private var usage: SnapshotsUsage?
    @State private var loading = false
    @State private var days = 30
    @State private var plan: PruneResult?

    var body: some View {
        Form {
            Section {
                if let usage {
                    LabeledContent("Space used") {
                        Text("\(bytes(usage.privateBytes)) for \(usage.snapshots.count) snapshot\(usage.snapshots.count == 1 ? "" : "s")")
                    }
                    HStack {
                        Picker("Delete snapshots older than", selection: $days) {
                            ForEach([7, 14, 30, 60, 90], id: \.self) { Text("\($0) days").tag($0) }
                        }
                        Button("Delete…") { Task { plan = await model.pruneSnapshots(olderThanDays: days, dryRun: true) } }
                            .disabled(model.busy != nil || usage.snapshots.isEmpty)
                    }
                } else if loading {
                    HStack { ProgressView().controlSize(.small); Text("Measuring…").foregroundStyle(.secondary) }
                }
            } header: {
                Text("Space")
            } footer: {
                Text("Files in a snapshot are APFS clones of the site: they take space only once the site's files change. The size shown is what deleting gives back, database dump included. Snapshots of an open AI agent session are never deleted here.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("All snapshots") {
                if let usage, !usage.snapshots.isEmpty {
                    ForEach(usage.snapshots) { s in
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(s.site)
                                Text([s.createdAt.formatted(date: .abbreviated, time: .shortened), kindTitle(s.kind), s.note]
                                    .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer()
                            Text(bytes(s.privateBytes)).monospacedDigit().foregroundStyle(.secondary)
                            if s.protected == true {
                                Image(systemName: "lock.fill").foregroundStyle(.secondary)
                                    .help("Used by an open AI agent session: Keep or Undo it first")
                            } else {
                                Button {
                                    Task { await model.deleteSnapshot(site: s.site, id: s.id); await load() }
                                } label: { Image(systemName: "trash") }
                                .buttonStyle(.borderless)
                                .disabled(model.busy != nil)
                                .help("Delete this snapshot")
                            }
                        }
                    }
                } else if usage != nil {
                    Text("No snapshots").foregroundStyle(.secondary)
                }
            }

            SnapshotKeepRow()
        }
        .formStyle(.grouped)
        .task { await load() }
        .sheet(item: Binding(get: { plan.map(PrunePlan.init) }, set: { if $0 == nil { plan = nil } })) { p in
            PruneConfirmSheet(plan: p.result, days: days) {
                Task {
                    _ = await model.pruneSnapshots(olderThanDays: days, dryRun: false)
                    plan = nil
                    await load()
                }
            }
        }
    }

    private func load() async {
        loading = true
        usage = await model.snapshotUsage()
        loading = false
    }
}

private struct PrunePlan: Identifiable {
    let result: PruneResult
    var id: String { result.removed.map(\.key).joined(separator: ",") + "\(result.freed)" }
}

/// Lists what a clean-up deletes before doing it.
private struct PruneConfirmSheet: View {
    @Environment(\.dismiss) private var dismiss
    let plan: PruneResult
    let days: Int
    let confirm: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if plan.removed.isEmpty {
                Text("No snapshots older than \(days) days").font(.headline)
                if plan.kept > 0 {
                    Text("\(plan.kept) are kept because an AI agent session uses them.").foregroundStyle(.secondary)
                }
                HStack { Spacer(); Button("OK") { dismiss() }.keyboardShortcut(.defaultAction) }
            } else {
                Text("Delete \(plan.removed.count) snapshot\(plan.removed.count == 1 ? "" : "s") older than \(days) days?").font(.headline)
                Text("This gives back \(bytes(plan.freed)). Deleted snapshots cannot be restored.")
                    .font(.callout).foregroundStyle(.secondary)
                List(plan.removed) { s in
                    HStack {
                        Text(s.site)
                        Text(s.createdAt.formatted(date: .abbreviated, time: .shortened)).foregroundStyle(.secondary)
                        Spacer()
                        Text(bytes(s.privateBytes)).monospacedDigit().foregroundStyle(.secondary)
                    }
                }
                .frame(height: 180)
                HStack {
                    Spacer()
                    Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                    Button("Delete", role: .destructive) { confirm(); dismiss() }
                }
            }
        }
        .padding(20)
        .frame(width: 480)
    }
}

private func bytes(_ n: Int64) -> String { n.formatted(.byteCount(style: .file)) }

private func kindTitle(_ kind: String) -> String {
    switch kind {
    case "pre-restore": "Before a restore"
    case "pre-pull": "Before a pull"
    case "mcp": "Before an AI agent change"
    case "session": "AI agent session start"
    case "checkpoint": "AI agent checkpoint"
    default: "Manual"
    }
}
