// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
import BenbowKit
import SwiftUI

/// Checks a site's code against a newer PHP version before switching, with
/// PHP_CodeSniffer and PHPCompatibility run by the core.
struct PHPCheckSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site

    @State private var target = ""
    @State private var all = false
    @State private var running = false
    @State private var result: PHPCheckResult?
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("PHP compatibility of \(site.name)").font(.headline)
            HStack {
                Picker("Check against", selection: $target) {
                    ForEach(model.php?.minors ?? [], id: \.self) { Text(model.phpLabel($0, prefix: "PHP ")).tag($0) }
                }
                .fixedSize()
                Toggle("Include WordPress core", isOn: $all)
                    .help("By default only wp-content (themes and plugins) is checked")
                Spacer()
                Button(running ? "Checking…" : "Check") { Task { await run() } }
                    .keyboardShortcut(.defaultAction)
                    .disabled(running || target.isEmpty)
            }
            if running {
                ProgressView("Reading the code… a large site takes a minute or two.")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let result {
                summary(result)
                if !result.files.isEmpty {
                    List {
                        ForEach(result.files) { f in
                            DisclosureGroup {
                                ForEach(Array(f.messages.enumerated()), id: \.offset) { _, m in
                                    HStack(alignment: .firstTextBaseline) {
                                        Image(systemName: m.type == "error" ? "xmark.octagon.fill" : "exclamationmark.triangle.fill")
                                            .foregroundStyle(m.type == "error" ? .red : .orange)
                                        Text("\(m.line)").monospacedDigit().foregroundStyle(.secondary).frame(width: 44, alignment: .trailing)
                                        Text(m.message).textSelection(.enabled)
                                    }
                                    .font(.callout)
                                }
                            } label: {
                                HStack {
                                    Text(f.path).lineLimit(1).truncationMode(.middle)
                                    Spacer()
                                    Text("\(f.messages.count)").foregroundStyle(.secondary).monospacedDigit()
                                    Button {
                                        NSWorkspace.shared.open(URL(fileURLWithPath: site.path).appending(path: f.path))
                                    } label: {
                                        Image(systemName: "arrow.up.forward.app")
                                    }
                                    .buttonStyle(.borderless)
                                    .help("Open the file")
                                }
                            }
                        }
                    }
                    .listStyle(.inset)
                }
            } else if let error {
                Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled)
                Spacer()
            } else {
                Text("Benbow reads the site's PHP files (nothing is changed) and lists what would stop working on the chosen version: removed functions, changed syntax, deprecations. The first check downloads PHP_CodeSniffer and PHPCompatibility.")
                    .font(.callout).foregroundStyle(.secondary)
                Spacer()
            }
            HStack {
                if let result, !result.files.isEmpty {
                    Button("Copy Report") { copyToClipboard(report(result)) }
                }
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
            }
        }
        .padding(20)
        .frame(width: 720, height: 520)
        .onAppear {
            target = model.recommendedPHP ?? model.php?.minors.first ?? ""
        }
    }

    @ViewBuilder private func summary(_ r: PHPCheckResult) -> some View {
        let scope = r.wordpress ? r.scanned.joined(separator: ", ") : "the whole site"
        if r.errors == 0 && r.warnings == 0 {
            Label("No problems found for PHP \(r.target) in \(scope).", systemImage: "checkmark.seal.fill")
                .foregroundStyle(.green)
        } else {
            VStack(alignment: .leading, spacing: 2) {
                Label("\(r.errors) errors, \(r.warnings) warnings in \(r.files.count) files (\(scope))",
                      systemImage: r.errors > 0 ? "xmark.octagon.fill" : "exclamationmark.triangle.fill")
                    .foregroundStyle(r.errors > 0 ? .red : .orange)
                Text("Errors break on PHP \(r.target); warnings are deprecated and will break later. Fix them, or update the theme or plugin, then switch the site's PHP version.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private func report(_ r: PHPCheckResult) -> String {
        var out = "\(r.site): PHP \(r.php) → \(r.target), \(r.errors) errors, \(r.warnings) warnings\n"
        for f in r.files {
            for m in f.messages {
                out += "\(f.path):\(m.line)  \(m.type.uppercased())  \(m.message)\n"
            }
        }
        return out
    }

    private func run() async {
        running = true
        defer { running = false }
        error = nil
        result = nil
        do {
            result = try await model.checkPHP(site, target: target, all: all)
        } catch {
            self.error = error.localizedDescription
        }
    }
}
