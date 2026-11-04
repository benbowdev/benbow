// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// Renames a site: the name in Covo, optionally its address too.
struct RenameSiteSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    let renamed: (String) -> Void
    @State private var name = ""
    @State private var domain = false

    private var clean: String { name.trimmingCharacters(in: .whitespaces).lowercased() }

    /// The address after the rename, when it starts with the old name.
    private var newDomain: String? {
        guard site.domain.hasPrefix(site.name + "."), !clean.isEmpty else { return nil }
        return clean + site.domain.dropFirst(site.name.count)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Rename “\(site.name)”").font(.title3.weight(.semibold))
            TextField("Name", text: $name, prompt: Text(site.name))
                .textFieldStyle(.roundedBorder)
                .onSubmit(rename)
            Text("Lowercase letters, digits and dashes. The folder, the database and the settings stay as they are; snapshots, logs and remotes follow the new name.")
                .font(.caption).foregroundStyle(.secondary)
            if let d = newDomain {
                Toggle(isOn: $domain) {
                    Text("Also change the address to \(d)")
                    if site.framework == "wordpress" {
                        Text("WordPress keeps its address in the database and would send visitors back to \(site.domain): leave this off, or update the address in WordPress too.")
                    } else {
                        Text("Bookmarks to \(site.domain) stop working.")
                    }
                }
            }
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Rename", action: rename)
                    .keyboardShortcut(.defaultAction)
                    .disabled(clean.isEmpty || clean == site.name || model.busy != nil)
            }
        }
        .padding(20)
        .frame(width: 460)
        .onAppear {
            name = site.name
            domain = site.framework != "wordpress"
        }
    }

    private func rename() {
        guard !clean.isEmpty, clean != site.name else { return }
        Task {
            if let n = await model.renameSite(site, to: clean, domain: domain && newDomain != nil) {
                dismiss()
                renamed(n)
            }
        }
    }
}
