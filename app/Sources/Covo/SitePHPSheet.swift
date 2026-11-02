// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// php.ini values of one site. Each can follow the PHP version (Settings →
/// PHP) or be the site's own.
struct SitePHPSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site

    @State private var loaded: SitePHPSettings?
    @State private var own: [String: Bool] = [:]
    @State private var values: [String: String] = [:]

    private let rows: [(key: String, title: String, prompt: String)] = [
        ("memory_limit", "Memory limit", "512M"),
        ("upload_max_filesize", "Max upload size", "128M"),
        ("max_execution_time", "Max execution time (s)", "300"),
        ("display_errors", "Show errors in the page", ""),
    ]

    var body: some View {
        Form {
            Section {
                ForEach(rows, id: \.key) { row in
                    LabeledContent(row.title) {
                        HStack {
                            if row.key == "display_errors" {
                                Toggle("", isOn: Binding(
                                    get: { values[row.key] == "on" },
                                    set: { values[row.key] = $0 ? "on" : "off" }
                                ))
                                .labelsHidden()
                                .disabled(own[row.key] != true)
                            } else {
                                TextField(row.title, text: binding(row.key), prompt: Text(row.prompt))
                                    .labelsHidden()
                                    .frame(width: 90)
                                    .multilineTextAlignment(.trailing)
                                    .disabled(own[row.key] != true)
                            }
                            Picker("", selection: Binding(
                                get: { own[row.key] == true },
                                set: { own[row.key] = $0 }
                            )) {
                                Text("PHP \(site.php)").tag(false)
                                Text("This site").tag(true)
                            }
                            .labelsHidden()
                            .fixedSize()
                        }
                    }
                }
            } header: {
                Text("PHP settings of \(site.name)")
            } footer: {
                Text("“PHP \(site.php)” follows Settings → PHP. Sizes like 512M or 1G; post_max_size follows the upload size; 0 s = no time limit.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .frame(width: 520)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button("Cancel") { dismiss() }
            }
            ToolbarItem(placement: .confirmationAction) {
                Button("Apply") {
                    Task { if await model.setSitePHPSettings(site, changes) { dismiss() } }
                }
                .disabled(loaded == nil || changes.isEmpty || model.busy != nil)
            }
        }
        .task { await load() }
    }

    private func binding(_ key: String) -> Binding<String> {
        Binding(get: { values[key] ?? "" }, set: { values[key] = $0 })
    }

    /// What differs from what was loaded: new or changed own values, and ""
    /// for values that go back to the PHP version.
    private var changes: [String: String] {
        guard let loaded else { return [:] }
        var out: [String: String] = [:]
        for row in rows {
            let was = loaded.overrides[row.key]
            if own[row.key] == true {
                let v = (values[row.key] ?? "").trimmingCharacters(in: .whitespaces)
                if v != was { out[row.key] = v }
            } else if was != nil {
                out[row.key] = ""
            }
        }
        return out
    }

    private func load() async {
        guard let s = await model.sitePHPSettings(site) else { return }
        loaded = s
        let effective = [
            "memory_limit": s.memoryLimit,
            "upload_max_filesize": s.uploadMaxFilesize,
            "max_execution_time": String(s.maxExecutionTime),
            "display_errors": s.displayErrors ? "on" : "off",
        ]
        for row in rows {
            own[row.key] = s.overrides[row.key] != nil
            values[row.key] = s.overrides[row.key] ?? effective[row.key]
        }
    }
}
