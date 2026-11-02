// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// File → Import from MAMP PRO…: preview, choose the names, import.
/// MAMP is only read (a copy of its settings), never changed.
struct ImportSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss

    @State private var keepLocal = true
    @State private var preview: ImportPlan?
    @State private var result: ImportPlan?
    @State private var error: String?
    @State private var loading = false
    @State private var working = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let result {
                done(result)
            } else {
                header
                Divider()
                list
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled)
            }
            footer
        }
        .padding()
        .frame(width: 680, height: 560)
        .task(id: keepLocal) { await load() }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Import from MAMP PRO").font(.title3.weight(.semibold))
            Text("Covo reads a copy of MAMP PRO's settings and creates the same sites, with the same folders. MAMP is not changed and can keep running.")
                .font(.callout).foregroundStyle(.secondary)
            Picker("Addresses", selection: $keepLocal) {
                Text("Keep the .local names (as in MAMP)").tag(true)
                Text("Use name.localhost (no setup)").tag(false)
            }
            .pickerStyle(.radioGroup)
            if keepLocal, preview?.needsLocal == true {
                Label("Keeping .local names turns on Covo's MAMP compatibility mode: macOS asks once for your administrator password.",
                      systemImage: "lock")
                    .font(.caption).foregroundStyle(.orange)
            }
        }
    }

    @ViewBuilder private var list: some View {
        if loading && preview == nil {
            ProgressView("Reading MAMP PRO…").frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let p = preview {
            List {
                Section("\(p.create) to create") {
                    ForEach(Array(p.items.enumerated()).filter { $0.element.creates }, id: \.offset) { _, it in
                        VStack(alignment: .leading, spacing: 2) {
                            HStack {
                                Text(it.domain ?? it.mamp.serverName).font(.body.weight(.medium))
                                Spacer()
                                Text("PHP \(it.php ?? "")").foregroundStyle(.secondary)
                            }
                            Text(it.mamp.documentRoot).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                            if let aliases = it.aliases, !aliases.isEmpty {
                                Text("also " + aliases.joined(separator: ", ")).font(.caption).foregroundStyle(.secondary)
                            }
                            ForEach(it.warnings, id: \.self) { w in
                                Label(w, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                            }
                        }
                    }
                }
                if p.skip > 0 {
                    Section("\(p.skip) skipped") {
                        ForEach(Array(p.items.enumerated()).filter { !$0.element.creates }, id: \.offset) { _, it in
                            HStack {
                                Text(it.mamp.serverName)
                                Spacer()
                                Text(it.reason ?? "").font(.caption).foregroundStyle(.secondary)
                            }
                            .foregroundStyle(.secondary)
                        }
                    }
                }
            }
            if !p.databases.isEmpty {
                Text("Databases are not copied. A site is linked to its database when MAMP linked exactly one and it already exists on Covo's MySQL.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        } else {
            Spacer()
        }
    }

    private func done(_ r: ImportPlan) -> some View {
        let linked = r.items.compactMap(\.database)
        return VStack(alignment: .leading, spacing: 10) {
            Label("Imported \(r.create) site\(r.create == 1 ? "" : "s") from MAMP PRO", systemImage: "checkmark.circle.fill")
                .font(.title3.weight(.semibold)).foregroundStyle(.green)
            if !linked.isEmpty {
                Text("\(linked.count) linked to their database: \(linked.joined(separator: ", ")).")
                    .foregroundStyle(.secondary)
            }
            Text(r.applied ? "They are being served now." : "Start Covo to serve them.")
                .foregroundStyle(.secondary)
            Spacer()
        }
    }

    private var footer: some View {
        HStack {
            if working { ProgressView().controlSize(.small); Text(model.busy ?? "Importing…").foregroundStyle(.secondary) }
            Spacer()
            if result != nil {
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            } else {
                Button("Cancel", role: .cancel) { dismiss() }.disabled(working)
                Button("Import \(preview?.create ?? 0) Sites") { Task { await run() } }
                    .keyboardShortcut(.defaultAction)
                    .disabled((preview?.create ?? 0) == 0 || working || loading)
            }
        }
    }

    private func load() async {
        loading = true
        defer { loading = false }
        error = nil
        do {
            preview = try await model.importMAMP(dryRun: true, localhost: !keepLocal)
        } catch {
            preview = nil
            self.error = error.localizedDescription
        }
    }

    private func run() async {
        working = true
        defer { working = false }
        error = nil
        do {
            if keepLocal, preview?.needsLocal == true {
                try await model.enableLocal()
            }
            result = try await model.importMAMP(dryRun: false, localhost: !keepLocal)
        } catch {
            self.error = error.localizedDescription
        }
    }
}
