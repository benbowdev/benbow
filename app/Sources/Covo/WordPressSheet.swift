// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// New WordPress Site: folder, database, wp-config.php and installation in
/// one step, then the admin login, shown once.
struct WordPressSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let created: (String) -> Void

    @State private var name = ""
    @State private var title = ""
    @State private var php = "8.4"
    @State private var parent = FileManager.default.homeDirectoryForCurrentUser.appending(path: "Sites")
    @State private var adminUser = "admin"
    @State private var adminEmail = ""
    @State private var choosing = false
    @State private var working = false
    @State private var error: String?
    @State private var result: WordPressResult?

    var body: some View {
        if let result {
            done(result)
        } else {
            form
        }
    }

    private var folder: URL { parent.appending(path: name.isEmpty ? "<name>" : name) }

    private var form: some View {
        Form {
            Section {
                TextField("Name", text: $name, prompt: Text("my-blog"))
                    .onChange(of: name) { _, new in
                        let clean = AppModel.siteName(from: new)
                        if clean != new && !new.hasSuffix(" ") && !new.hasSuffix("-") { name = clean }
                    }
                TextField("Title", text: $title, prompt: Text(name.isEmpty ? "My Blog" : name))
                Picker("PHP", selection: $php) {
                    ForEach(model.php?.minors ?? ["8.4"], id: \.self) { Text($0).tag($0) }
                }
                LabeledContent("Folder") {
                    HStack {
                        Text(folder.path).lineLimit(1).truncationMode(.middle).foregroundStyle(.secondary)
                        Button("Change…") { choosing = true }
                    }
                }
                LabeledContent("Address") {
                    Text(name.isEmpty ? "—" : "https://\(name).localhost:\(model.status?.httpsPort ?? 8443)")
                        .foregroundStyle(.secondary)
                }
            } footer: {
                Text("Covo downloads WordPress (checksum verified), creates the folder, a database with its own user, wp-config.php, and installs it.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section("Administrator") {
                TextField("User", text: $adminUser)
                TextField("Email", text: $adminEmail, prompt: Text(name.isEmpty ? "admin@<name>.localhost" : "admin@\(name).localhost"))
            }
            if !model.mysqlReady {
                Text(model.status?.mysql.enabled == true ? "Start Covo first: WordPress needs MySQL running." : "Turn MySQL on in Settings first.")
                    .foregroundStyle(.orange).font(.callout)
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout)
            }
        }
        .formStyle(.grouped)
        .frame(width: 480)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button("Cancel") { dismiss() }.disabled(working)
            }
            ToolbarItem(placement: .confirmationAction) {
                Button("Create") { Task { await create() } }
                    .disabled(name.isEmpty || working || !model.mysqlReady)
            }
        }
        .overlay {
            if working {
                ProgressView("Installing WordPress…")
                    .padding()
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
            }
        }
        .fileImporter(isPresented: $choosing, allowedContentTypes: [.folder]) { r in
            if case .success(let url) = r { parent = url }
        }
        .onAppear {
            if let minors = model.php?.minors, !minors.contains(php) { php = minors.first ?? php }
        }
    }

    private func done(_ r: WordPressResult) -> some View {
        Form {
            Section {
                LabeledContent("Site") { Link(r.url, destination: URL(string: r.url)!) }
                LabeledContent("Admin") { Link(r.adminUrl, destination: URL(string: r.adminUrl)!) }
                LabeledContent("User") { Text(r.adminUser).textSelection(.enabled) }
                LabeledContent("Password") {
                    HStack {
                        Text(r.adminPassword).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                        Button {
                            copyToClipboard(r.adminPassword)
                        } label: {
                            Image(systemName: "doc.on.doc")
                        }
                        .buttonStyle(.borderless)
                        .help("Copy")
                    }
                }
            } header: {
                Text("WordPress \(r.wordpress) is ready").font(.headline)
            } footer: {
                Text("Save the password now (e.g. in your password manager): Covo does not keep it. You can reset it later with `covo wp \(r.name) user update \(r.adminUser) --prompt=user_pass`.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .frame(width: 480)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Done") {
                    created(r.name)
                    dismiss()
                }
            }
            ToolbarItem(placement: .primaryAction) {
                Button("Open Admin") { open(r.adminUrl) }
            }
        }
    }

    private func create() async {
        working = true
        defer { working = false }
        error = nil
        do {
            result = try await model.newWordPress(
                name: name, path: folder.path, php: php, title: title,
                adminUser: adminUser, adminEmail: adminEmail)
        } catch {
            self.error = error.localizedDescription
        }
    }
}
