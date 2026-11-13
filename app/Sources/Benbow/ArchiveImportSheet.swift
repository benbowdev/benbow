// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import BenbowKit
import SwiftUI

extension URL: @retroactive Identifiable {
    public var id: String { absoluteString }
}

/// Whether a dropped file is a site archive Benbow can import.
func isSiteArchive(_ url: URL) -> Bool {
    let name = url.lastPathComponent.lowercased()
    return !url.hasDirectoryPath && [".zip", ".tar.gz", ".tgz", ".tar"].contains { name.hasSuffix($0) }
}

/// File → Import Site Archive… (or a .zip dropped on the window): shows what
/// Benbow found in the archive, then creates the site.
struct ArchiveImportSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let url: URL
    let created: (String) -> Void

    @State private var plan: ArchiveImportResult?
    @State private var name = ""
    @State private var php = ""
    @State private var error: String?
    @State private var done: ArchiveImportResult?

    var body: some View {
        Form {
            if let done {
                Section {
                    Label("\(done.name) is ready", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                    if let site = done.site { LabeledContent("Address") { Text(site.url).textSelection(.enabled) } }
                    if let from = done.urlFrom { LabeledContent("Replaced") { Text(from).foregroundStyle(.secondary) } }
                }
                if !done.warnings.isEmpty {
                    Section("Check") {
                        ForEach(done.warnings, id: \.self) { Text($0).font(.callout) }
                    }
                }
            } else if let plan {
                Section {
                    TextField("Name", text: $name)
                    Picker("PHP", selection: $php) {
                        ForEach(model.php?.minors ?? [plan.php], id: \.self) { Text(model.phpLabel($0)).tag($0) }
                    }
                    if let reason = plan.phpReason { Text("Suggested: PHP \(plan.php), \(reason).").font(.caption).foregroundStyle(.secondary) }
                    PHPSupportNote(minor: php)
                    LabeledContent("Folder") {
                        Text(folder).lineLimit(1).truncationMode(.middle).foregroundStyle(.secondary)
                    }
                } header: {
                    Text("Import \(url.lastPathComponent)")
                }
                Section {
                    ExplainBox(title: "What Benbow found and will do", steps: [
                        "Site: \(plan.root.isEmpty ? "the top of the archive" : plan.root)\(found(plan)), copied into \(folder).",
                        plan.sql.map { "Database: \($0), loaded into a new database with its own user. The dump is kept out of the site's folder." }
                            ?? "No .sql dump in the archive: the site gets no database.",
                        plan.sql == nil ? nil : "Point wp-config.php or .env at the new database and replace WordPress's old address.",
                    ].compactMap { $0 }, caution: "The archive is only read.")
                    ForEach(plan.warnings, id: \.self) { Text($0).font(.caption).foregroundStyle(.orange) }
                }
            } else if error == nil {
                HStack { ProgressView().controlSize(.small); Text("Reading \(url.lastPathComponent)…").foregroundStyle(.secondary) }
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout)
            }
        }
        .formStyle(.grouped)
        .frame(width: 520)
        .toolbar {
            if done != nil {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            } else {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Import") { Task { await run() } }
                        .disabled(plan == nil || name.isEmpty || model.busy != nil)
                }
            }
        }
        .task {
            do {
                plan = try await model.importArchive(url, name: nil, php: nil, folder: nil, dryRun: true)
                name = plan?.name ?? ""
                php = plan?.php ?? "8.4"
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private var folder: String {
        FileManager.default.homeDirectoryForCurrentUser.appending(path: "Sites").appending(path: name.isEmpty ? "—" : name).path
    }

    private func found(_ p: ArchiveImportResult) -> String {
        if let wp = p.wordpress { return " (WordPress \(wp))" }
        switch p.framework {
        case "wordpress": return " (WordPress)"
        case "laravel": return " (Laravel)"
        default: return ""
        }
    }

    private func run() async {
        error = nil
        do {
            done = try await model.importArchive(url, name: name, php: php, folder: nil, dryRun: false)
            if let done { created(done.name) }
        } catch {
            self.error = error.localizedDescription
        }
    }
}
