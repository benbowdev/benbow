// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import BenbowKit
import SwiftUI

/// A WordPress site and blueprints: apply one (snapshot first), or save the
/// site as a new one.
struct BlueprintSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site

    @State private var list: [BlueprintInfo] = []
    @State private var chosen = ""
    @State private var plan: BlueprintResult?
    @State private var done: BlueprintResult?
    @State private var changed: String?
    @State private var newName = ""
    @State private var saved: SavedBlueprint?
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Blueprints for \(site.name)").font(.headline)
            Form {
                Section {
                    Picker("Blueprint", selection: $chosen) {
                        Text("Choose…").tag("")
                        ForEach(list) { b in Text(b.name).tag(b.name) }
                    }
                    .onChange(of: chosen) { _, name in
                        plan = nil; done = nil; changed = nil
                        guard !name.isEmpty else { return }
                        Task { plan = try? await model.applyBlueprint(name, to: site, dryRun: true) }
                    }
                    if let r = done ?? plan {
                        resultView(r, applied: done != nil)
                    }
                    if let changed {
                        Label(changed, systemImage: "exclamationmark.triangle").foregroundStyle(.orange).font(.callout)
                    }
                    HStack {
                        Spacer()
                        if changed != nil {
                            Button("Accept Changes and Apply") { Task { await apply(accept: true) } }
                        }
                        Button("Apply") { Task { await apply(accept: false) } }
                            .disabled(chosen.isEmpty || done != nil || model.busy != nil)
                    }
                } header: {
                    Text("Apply")
                } footer: {
                    Text(BlueprintSheet.notice + " A snapshot is taken first. Themes and plugins already in the site are left alone.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Section {
                    HStack {
                        TextField("Name", text: $newName, prompt: Text("e.g. agency-starter"))
                        Button("Save") { Task { await save() } }
                            .disabled(newName.isEmpty || model.busy != nil)
                    }
                    if let saved {
                        Text("Saved \(saved.blueprint.name): \(saved.blueprint.themes) themes, \(saved.blueprint.plugins) plugins.")
                            .font(.callout)
                        ForEach(saved.warnings ?? [], id: \.self) { w in
                            Label(w, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                        }
                    }
                } header: {
                    Text("Save this site as a blueprint")
                } footer: {
                    Text("Benbow keeps the WordPress version and language, themes and plugins from wordpress.org (at their installed versions), permalinks, timezone and must-use plugins. wp-config constants are not copied: they can hold secrets.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .formStyle(.grouped)
            .toggleStyle(.benbowSwitch)
            if let error {
                Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled)
            }
            HStack {
                if let busy = model.busy { ProgressView().controlSize(.small); Text(busy).foregroundStyle(.secondary) }
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
            }
        }
        .padding(20)
        .frame(width: 600, height: 560)
        .task { list = await model.blueprints() }
    }

    static let notice = "Blueprints install third-party code into your site: use only sources you trust. Benbow records each download's SHA-256 the first time and stops if it changes."

    @ViewBuilder
    private func resultView(_ r: BlueprintResult, applied: Bool) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(applied ? "Installed" : "Will install").font(.caption).foregroundStyle(.secondary)
            if r.installed.isEmpty { Text("Nothing new").font(.callout) }
            ForEach(r.installed, id: \.self) { Text($0).font(.callout) }
            ForEach(r.warnings ?? [], id: \.self) { w in
                Text(w).font(.caption).foregroundStyle(.secondary)
            }
            if let s = r.snapshot {
                Text("Snapshot \(s) has the site as it was before.").font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private func apply(accept: Bool) async {
        error = nil
        do {
            done = try await model.applyBlueprint(chosen, to: site, dryRun: false, acceptChanges: accept)
            changed = nil
        } catch let e as APIError where e.code == "checksum_mismatch" {
            changed = e.message
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func save() async {
        error = nil
        do {
            saved = try await model.saveBlueprint(site, name: newName, overwrite: false)
            list = await model.blueprints()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
