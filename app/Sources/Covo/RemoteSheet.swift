// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// Production and staging copies of a site: pull from either, push to
/// staging only. A snapshot is taken first; Covo shows what will change.
struct RemoteSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site

    @State private var remotes: [Remote] = []
    @State private var editing: Remote?
    @State private var preview: (op: String, remote: Remote, result: SyncResult)?
    @State private var done: SyncResult?
    @State private var error: String?
    @State private var files = true
    @State private var database = true
    @State private var working = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Production and staging of \(site.name)").font(.headline)
            if let editing {
                RemoteForm(site: site, remote: editing) { saved in
                    self.editing = nil
                    if saved { Task { remotes = await model.remotes(site) } }
                }
            } else {
                list
            }
            if let error {
                Text(error).font(.callout).foregroundStyle(.red).textSelection(.enabled)
            }
            HStack {
                if working || model.busy != nil { ProgressView().controlSize(.small); Text(model.busy ?? "Checking…").foregroundStyle(.secondary) }
                Spacer()
                if editing == nil {
                    Button("Add Remote…") { editing = Remote(site: site.name, name: "production", kind: "production", transport: "ssh", host: "", port: nil, user: "", path: "", url: "", webroot: nil, paths: [], excludes: []) }
                    Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
                }
            }
        }
        .padding(20)
        .frame(width: 640, height: 520)
        .task { remotes = await model.remotes(site) }
    }

    @ViewBuilder private var list: some View {
        if let p = preview {
            VStack(alignment: .leading, spacing: 8) {
                Text(p.op == "pull" ? "Pull from \(p.remote.name)" : "Push to \(p.remote.name)").font(.title3.weight(.semibold))
                ForEach(p.result.steps, id: \.self) { Label($0, systemImage: "arrow.right.circle").font(.callout) }
                if !p.result.files.isEmpty {
                    Text(p.result.files.prefix(12).joined(separator: "\n") + (p.result.files.count > 12 ? "\n… and \(p.result.files.count - 12) more" : ""))
                        .font(.system(.caption, design: .monospaced)).foregroundStyle(.secondary)
                }
                Text(p.op == "pull"
                     ? "A snapshot of \(site.name) is taken first: restore it to undo the pull."
                     : "This replaces the staging site's database and files. A snapshot of \(site.name) is taken first.")
                    .font(.caption).foregroundStyle(.secondary)
                HStack {
                    Spacer()
                    Button("Cancel") { preview = nil }
                    Button(p.op == "pull" ? "Pull" : "Push", role: p.op == "push" ? .destructive : nil) {
                        Task { await run(p.op, p.remote, dryRun: false) }
                    }
                    .keyboardShortcut(.defaultAction)
                    .disabled(model.busy != nil)
                }
            }
            Spacer()
        } else if let d = done {
            VStack(alignment: .leading, spacing: 8) {
                Label("Done", systemImage: "checkmark.circle.fill").foregroundStyle(.green).font(.title3.weight(.semibold))
                ForEach(d.steps, id: \.self) { Text($0).font(.callout) }
                Button("Back") { done = nil }
            }
            Spacer()
        } else {
            HStack {
                Toggle("Database", isOn: $database)
                Toggle("Files", isOn: $files)
            }
            List(remotes) { r in
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        HStack {
                            Text(r.name).font(.body.weight(.medium))
                            Text(r.kind).font(.caption).padding(.horizontal, 5).background(.quaternary, in: Capsule())
                        }
                        Text("\(r.transport) · \(r.user)@\(r.host):\(r.path)").font(.caption).foregroundStyle(.secondary)
                        Text(r.url).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Pull…") { Task { await run("pull", r, dryRun: true) } }
                    if r.kind == "staging" {
                        Button("Push…") { Task { await run("push", r, dryRun: true) } }
                    }
                    Menu {
                        Button("Edit…") { editing = r }
                        Button("Remove", role: .destructive) { Task { await model.removeRemote(site, r); remotes = await model.remotes(site) } }
                    } label: { Image(systemName: "ellipsis.circle") }
                    .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
                }
                .disabled(working || model.busy != nil)
            }
            .overlay {
                if remotes.isEmpty {
                    Text("No remotes yet. Add the production site (pull only) or a staging site (pull and push).")
                        .foregroundStyle(.secondary).multilineTextAlignment(.center).padding()
                }
            }
        }
    }

    private func run(_ op: String, _ r: Remote, dryRun: Bool) async {
        working = dryRun
        defer { working = false }
        error = nil
        do {
            guard let res = try await model.sync(op, site, r, dryRun: dryRun, files: files, database: database) else { return }
            if dryRun {
                preview = (op, r, res)
            } else {
                preview = nil
                done = res
            }
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// Adding or changing a remote.
struct RemoteForm: View {
    @Environment(AppModel.self) private var model
    let site: Site
    @State var remote: Remote
    let done: (Bool) -> Void
    @State private var port = ""
    @State private var password = ""
    @State private var paths = ""

    var body: some View {
        Form {
            Picker("Kind", selection: $remote.kind) {
                Text("Production (pull only)").tag("production")
                Text("Staging (pull and push)").tag("staging")
            }
            TextField("Name", text: $remote.name, prompt: Text(remote.kind))
            Picker("Connection", selection: $remote.transport) {
                Text("SSH").tag("ssh")
                Text("SFTP (no shell)").tag("sftp")
                Text("FTPS").tag("ftps")
                Text("FTP (unencrypted)").tag("ftp")
            }
            TextField("Server", text: $remote.host, prompt: Text("example.com"))
            TextField("Port", text: $port, prompt: Text(remote.transport.hasPrefix("ftp") ? "21" : "22"))
            TextField("User", text: $remote.user)
            SecureField("Password", text: $password, prompt: Text(remote.transport.hasPrefix("ftp") ? "required, kept in the keychain" : "only without SSH keys (kept in the keychain)"))
            TextField("Project folder", text: $remote.path, prompt: Text("/home/user/public_html"))
            TextField("Site address", text: $remote.url, prompt: Text("https://example.com"))
            TextField("Folders to sync", text: $paths, prompt: Text(site.framework == "wordpress" ? "wp-content/uploads" : site.framework == "laravel" ? "storage/app/public" : "everything"))
            if remote.transport == "ftp" {
                Label("Plain FTP sends the password and the files unencrypted.", systemImage: "exclamationmark.triangle").foregroundStyle(.orange).font(.caption)
            }
            if remote.transport != "ssh" {
                Text("Without SSH, Covo reaches the database through a small temporary PHP file it uploads next to the site, protected by a one-time key, and deletes right after (it also stops working by itself after 10 minutes).")
                    .font(.caption).foregroundStyle(.secondary)
            }
            HStack {
                Spacer()
                Button("Cancel") { done(false) }
                Button("Save") {
                    Task {
                        remote.port = Int(port)
                        remote.paths = paths.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
                        if remote.name.isEmpty { remote.name = remote.kind }
                        if await model.setRemote(remote, password: password.isEmpty ? nil : password) { done(true) }
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(remote.host.isEmpty || remote.user.isEmpty || remote.path.isEmpty || remote.url.isEmpty || model.busy != nil)
            }
        }
        .formStyle(.grouped)
        .onAppear {
            port = remote.port.map(String.init) ?? ""
            paths = remote.paths.joined(separator: ", ")
        }
    }
}
