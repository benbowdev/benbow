// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

struct SiteDetail: View {
    @Environment(AppModel.self) private var model
    let site: Site
    @State private var advanced = false
    @State private var confirmRemove = false
    @State private var choosingFolder = false
    /// A folder without an index file, waiting for "Use Anyway".
    @State private var pendingFolder: URL?
    @State private var domain = ""

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
                        Button("Change…") { choosingFolder = true }
                            .disabled(model.busy != nil)
                    }
                }
                Picker("PHP", selection: Binding(
                    get: { site.php },
                    set: { minor in
                        guard minor != site.php else { return }
                        Task { await model.updateSiteShowingErrors(site, php: minor) }
                    }
                )) {
                    ForEach(phpChoices, id: \.self) { minor in
                        Text(model.installed(minor) ? minor : "\(minor) (download)").tag(minor)
                    }
                }
                .disabled(model.busy != nil)
                LabeledContent("HTTPS", value: site.https ? "On" : "Off")
                LabeledContent("Database") { Text("None").foregroundStyle(.secondary) }
            } header: {
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(site.name).font(.title2.weight(.semibold)).foregroundStyle(.primary)
                        Text(site.url).font(.callout).foregroundStyle(.secondary).textSelection(.enabled)
                    }
                    Spacer()
                    Button("Open") { open(site.url) }
                        .keyboardShortcut("o")
                        .disabled(!model.running)
                        .help(model.running ? "Open in your browser" : "Start Covo first")
                }
            }

            Section {
                DisclosureGroup("Advanced", isExpanded: $advanced) {
                    LabeledContent("Domain") {
                        HStack {
                            TextField("Domain", text: $domain, prompt: Text(site.domain))
                                .labelsHidden()
                                .multilineTextAlignment(.trailing)
                                .onSubmit(saveDomain)
                            Button("Save", action: saveDomain)
                                .disabled(domain.isEmpty || domain == site.domain || model.busy != nil)
                        }
                    }
                    Text(domainHint).font(.caption).foregroundStyle(.secondary)
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
        .onAppear { domain = site.domain }
        .onChange(of: site.domain) { _, new in domain = new }
        .confirmationDialog("Remove \(site.name)?", isPresented: $confirmRemove) {
            Button("Remove", role: .destructive) { Task { await model.removeSite(site) } }
        } message: {
            Text("Covo stops serving it. The folder \(site.path) is not touched.")
        }
        .fileImporter(isPresented: $choosingFolder, allowedContentTypes: [.folder]) { result in
            if case .success(let url) = result { Task { await changeFolder(url, force: false) } }
        }
        .alert("No index file", isPresented: Binding(get: { pendingFolder != nil }, set: { if !$0 { pendingFolder = nil } })) {
            Button("Use Anyway") {
                if let url = pendingFolder { Task { await changeFolder(url, force: true) } }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("\(pendingFolder?.lastPathComponent ?? "This folder") has no index.php or index.html. Use it anyway?")
        }
    }

    /// Every PHP version Covo can serve, plus the site's own if it is older.
    private var phpChoices: [String] {
        var minors = model.php?.minors ?? []
        if !minors.contains(site.php) { minors.append(site.php) }
        return minors
    }

    private var domainHint: String {
        var suffixes = [".localhost"]
        if model.domainEnabled("test") { suffixes.append(".test") }
        if model.domainEnabled("local") { suffixes.append(".local") }
        return "Must end in \(suffixes.joined(separator: ", ")). More suffixes in Settings → Domains."
    }

    private func saveDomain() {
        let d = domain.trimmingCharacters(in: .whitespaces).lowercased()
        guard !d.isEmpty, d != site.domain else { return }
        Task { await model.updateSiteShowingErrors(site, domain: d) }
    }

    private func changeFolder(_ url: URL, force: Bool) async {
        do {
            try await model.updateSite(site, path: url.path, force: force)
        } catch let e as APIError where e.code == "confirmation_required" {
            pendingFolder = url
        } catch {
            model.error = error.localizedDescription
        }
    }
}
