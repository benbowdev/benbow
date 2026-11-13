// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import BenbowKit
import SwiftUI

/// Clone Site: a full copy under a new name (folder as an APFS clone,
/// database, address), or with "Same folder" just a new name and PHP version
/// on the same folder, e.g. to try a branch on another PHP version.
struct CloneSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    let created: (String) -> Void
    @State private var name = ""
    @State private var php = ""
    @State private var sameFolder = false
    @State private var error: String?

    private var folder: String {
        let root = URL(fileURLWithPath: site.projectRoot)
        return root.deletingLastPathComponent().appendingPathComponent(name.isEmpty ? "—" : name).path
    }

    var body: some View {
        Form {
            Section {
                TextField("Name", text: $name)
                Picker("PHP", selection: $php) {
                    ForEach(model.php?.minors ?? [site.php], id: \.self) { Text(model.phpLabel($0)).tag($0) }
                }
                PHPSupportNote(minor: php)
                Toggle("Same folder", isOn: $sameFolder)
                LabeledContent("Folder") {
                    Text(sameFolder ? site.path : folder).lineLimit(1).truncationMode(.middle).foregroundStyle(.secondary)
                }
                LabeledContent("Address") {
                    Text(name.isEmpty ? "—" : model.address("\(name).localhost"))
                        .foregroundStyle(.secondary)
                }
            } header: {
                Text("Clone \(site.name)")
            }
            Section {
                if sameFolder {
                    ExplainBox(title: "A new address for the same folder", steps: [
                        "Serves \(site.path) at the new address, with the PHP version you pick.",
                        "Nothing is copied: files and database stay shared with \(site.name).",
                    ])
                } else {
                    ExplainBox(title: "What Benbow will do", steps: [
                        "Clone the project folder into \(folder) (APFS: instant, no space until files change).",
                        site.database.isEmpty ? "The site has no database: nothing else to copy."
                            : "Copy the database \(site.database) into a new one with its own user.",
                        site.database.isEmpty ? nil : "Point wp-config.php or .env of the copy at it, and replace the address in WordPress's database.",
                    ].compactMap { $0 }, caution: "\(site.name) is only read, never changed.")
                }
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout)
            }
        }
        .formStyle(.grouped)
        .toggleStyle(.benbowSwitch)
        .frame(width: 500)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) {
                Button(sameFolder ? "Add" : "Clone") {
                    Task {
                        do {
                            _ = try await model.cloneSite(site, name: name, php: php, folder: nil, sameFolder: sameFolder)
                            created(name)
                            dismiss()
                        } catch {
                            self.error = error.localizedDescription
                        }
                    }
                }
                .disabled(name.isEmpty || model.busy != nil)
            }
        }
        .onAppear {
            php = site.php
            var n = 2
            while model.sites.contains(where: { $0.name == "\(site.name)-\(n)" }) { n += 1 }
            name = String("\(site.name)-\(n)".prefix(63))
        }
    }
}
