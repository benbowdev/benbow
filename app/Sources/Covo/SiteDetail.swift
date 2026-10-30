// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

struct SiteDetail: View {
    @Environment(AppModel.self) private var model
    let site: Site
    @State private var advanced = false
    @State private var confirmRemove = false

    var body: some View {
        Form {
            Section {
                LabeledContent("Name", value: site.name)
                LabeledContent("Folder") {
                    HStack {
                        Text(site.path).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                        Button {
                            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: site.path)
                        } label: {
                            Image(systemName: "arrow.right.circle.fill")
                        }
                        .buttonStyle(.borderless)
                        .help("Show in Finder")
                    }
                }
                LabeledContent("PHP", value: site.php)
                LabeledContent("HTTPS", value: site.https ? "On" : "Off")
                LabeledContent("Database") { Text("None").foregroundStyle(.secondary) }
            } header: {
                HStack {
                    Text(site.url).font(.headline).textSelection(.enabled)
                    Spacer()
                    Button("Open") { open(site.url) }
                        .keyboardShortcut("o")
                        .disabled(!model.running)
                        .help(model.running ? "Open in your browser" : "Start Covo first")
                }
            }

            Section {
                DisclosureGroup("Advanced", isExpanded: $advanced) {
                    LabeledContent("Domain", value: site.domain)
                    if let x = model.xdebug(for: site.php), x.available {
                        Toggle(isOn: Binding(
                            get: { x.enabled },
                            set: { on in Task { await model.setXdebug(php: site.php, enabled: on) } }
                        )) {
                            Text("Xdebug")
                            Text("For every site on PHP \(site.php). Your IDE listens on port 9003.")
                        }
                        .disabled(model.busy != nil)
                    }
                    HStack {
                        Spacer()
                        Button("Remove Site…", role: .destructive) { confirmRemove = true }
                            .disabled(model.busy != nil)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .navigationTitle(site.name)
        .confirmationDialog("Remove \(site.name)?", isPresented: $confirmRemove) {
            Button("Remove", role: .destructive) { Task { await model.removeSite(site) } }
        } message: {
            Text("Covo stops serving it. The folder \(site.path) is not touched.")
        }
    }
}
