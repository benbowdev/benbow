// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// Confirms removing a site, optionally with its database.
struct RemoveSiteSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    @State private var dropDatabase = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("Remove \(site.name)?", systemImage: "trash")
                .font(.headline)
            Text("Covo stops serving it and forgets its settings and logs. The folder is not touched:")
                .foregroundStyle(.secondary)
            Text(site.path).font(.callout).textSelection(.enabled).lineLimit(2).truncationMode(.middle)
            if !site.database.isEmpty {
                Toggle(isOn: $dropDatabase) {
                    Text("Also delete the database \(site.database)")
                    Text(site.databaseUser != nil
                         ? "With its tables, data and the site's MySQL user. This cannot be undone."
                         : "With its tables and data. This cannot be undone.")
                }
                .toggleStyle(.checkbox)
                if !dropDatabase {
                    Text("The database is kept on Covo's MySQL.").font(.caption).foregroundStyle(.secondary)
                }
            }
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(dropDatabase ? "Remove Site and Database" : "Remove Site", role: .destructive) {
                    let drop = dropDatabase
                    dismiss()
                    Task { await model.removeSite(site, dropDatabase: drop) }
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 440)
    }
}
