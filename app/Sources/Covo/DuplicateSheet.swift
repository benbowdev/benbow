// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// Duplicate Site: the same folder and PHP version under a new name, e.g.
/// to try a branch on another PHP version.
struct DuplicateSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    let created: (String) -> Void
    @State private var name = ""
    @State private var php = ""
    @State private var error: String?

    var body: some View {
        Form {
            Section {
                TextField("Name", text: $name)
                Picker("PHP", selection: $php) {
                    ForEach(model.php?.minors ?? [site.php], id: \.self) { Text($0).tag($0) }
                }
                LabeledContent("Folder") {
                    Text(site.path).lineLimit(1).truncationMode(.middle).foregroundStyle(.secondary)
                }
                LabeledContent("Address") {
                    Text(name.isEmpty ? "—" : "https://\(name).localhost:\(model.status?.httpsPort ?? 8443)")
                        .foregroundStyle(.secondary)
                }
            } header: {
                Text("Duplicate \(site.name)")
            } footer: {
                Text("Same folder, new address. The database, aliases and PHP settings are not copied.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout)
            }
        }
        .formStyle(.grouped)
        .frame(width: 460)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) {
                Button("Duplicate") {
                    Task {
                        do {
                            try await model.addSite(name: name, php: php, path: site.path, force: true)
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
