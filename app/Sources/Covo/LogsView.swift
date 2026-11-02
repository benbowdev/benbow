// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
import CovoKit
import SwiftUI

/// One site's logs, refreshed every 2 seconds while the window is open.
struct LogsView: View {
    @Environment(AppModel.self) private var model
    let site: String
    @State private var kind = "php"
    @State private var log: SiteLog?
    @State private var failure: String?

    var body: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 1) {
                        ForEach(Array((log?.lines ?? []).enumerated()), id: \.offset) { _, line in
                            Text(line)
                                .font(.system(.callout, design: .monospaced))
                                .foregroundStyle(color(line))
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        Color.clear.frame(height: 1).id("end")
                    }
                    .textSelection(.enabled)
                    .padding(10)
                }
                .onChange(of: log?.lines) { _, _ in proxy.scrollTo("end", anchor: .bottom) }
            }
            .overlay {
                if let failure {
                    ContentUnavailableView("Logs unavailable", systemImage: "exclamationmark.triangle", description: Text(failure))
                } else if log?.lines.isEmpty == true {
                    ContentUnavailableView(kind == "php" ? "No PHP errors" : "No requests yet",
                                           systemImage: "doc.text",
                                           description: Text(kind == "php"
                                               ? "Errors, warnings and notices of \(site) appear here."
                                               : "Open the site: each request appears here."))
                }
            }
        }
        .navigationTitle("\(site) — Logs")
        .toolbar {
            ToolbarItem(placement: .principal) {
                Picker("Log", selection: $kind) {
                    Text("PHP Errors").tag("php")
                    Text("Requests").tag("access")
                }
                .pickerStyle(.segmented)
                .labelsHidden()
            }
            ToolbarItemGroup(placement: .primaryAction) {
                Button {
                    if let p = log?.path { NSWorkspace.shared.selectFile(p, inFileViewerRootedAtPath: "") }
                } label: {
                    Label("Show in Finder", systemImage: "folder")
                }
                .disabled(log?.size ?? 0 == 0)
                Button {
                    Task {
                        await model.clearSiteLogs(site)
                        await load()
                    }
                } label: {
                    Label("Clear", systemImage: "trash")
                }
                .help("Empty both logs of \(site)")
            }
        }
        .frame(minWidth: 640, minHeight: 360)
        .task(id: kind) {
            log = nil
            while !Task.isCancelled {
                await load()
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }

    private func load() async {
        do {
            let new = try await model.siteLogs(site, kind: kind)
            if new != log { log = new }
            failure = nil
        } catch {
            failure = error.localizedDescription
        }
    }

    private func color(_ line: String) -> Color {
        if kind == "php" {
            if line.contains("Fatal error") || line.contains("Parse error") { return .red }
            if line.contains("Warning") || line.contains("Deprecated") { return .orange }
            return .primary
        }
        // "date time  STATUS  …"
        let parts = line.split(separator: " ", omittingEmptySubsequences: true)
        if parts.count > 2, let status = Int(parts[2]) {
            if status >= 500 { return .red }
            if status >= 400 { return .orange }
        }
        return .primary
    }
}
