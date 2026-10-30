// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

struct AddSiteRequest: Identifiable {
    let id = UUID()
    var folder: URL?
}

struct AddSiteSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let request: AddSiteRequest
    let added: (String) -> Void

    @State private var folder: URL?
    @State private var name = ""
    @State private var php = ""
    @State private var choosing = false
    @State private var noIndex: String?
    @State private var error: String?
    @State private var working = false

    var body: some View {
        Form {
            LabeledContent("Folder") {
                HStack {
                    Text(folder?.path ?? "None").lineLimit(1).truncationMode(.middle)
                        .foregroundStyle(folder == nil ? .secondary : .primary)
                    Button("Choose…") { choosing = true }
                }
            }
            TextField("Name", text: $name)
            Picker("PHP", selection: $php) {
                ForEach(model.php?.minors ?? [], id: \.self) { Text($0).tag($0) }
            }
            LabeledContent("Address") {
                Text(name.isEmpty ? "—" : "https://\(name).localhost:\(model.status?.httpsPort ?? 8443)")
                    .foregroundStyle(.secondary)
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout)
            }
        }
        .formStyle(.grouped)
        .frame(width: 460)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button("Cancel") { dismiss() }
            }
            ToolbarItem(placement: .confirmationAction) {
                Button("Add Site") { Task { await add(force: false) } }
                    .disabled(folder == nil || name.isEmpty || php.isEmpty || working)
            }
        }
        .overlay {
            if working {
                ProgressView(model.busy ?? "Adding…").padding().background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
            }
        }
        .fileImporter(isPresented: $choosing, allowedContentTypes: [.folder]) { result in
            if case .success(let url) = result { pick(url) }
        }
        .alert("No index file", isPresented: Binding(get: { noIndex != nil }, set: { if !$0 { noIndex = nil } })) {
            Button("Add Anyway") { Task { await add(force: true) } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("\(folder?.lastPathComponent ?? "This folder") has no index.php or index.html. Add it anyway?")
        }
        .onAppear {
            php = model.php?.installed.first?.minor ?? model.php?.minors.first ?? ""
            if let f = request.folder { pick(f) }
        }
    }

    private func pick(_ url: URL) {
        folder = url
        // A folder named "public" or "htdocs" says little: use its parent's name.
        let generic = ["public", "htdocs", "www", "web", "public_html"]
        let base = generic.contains(url.lastPathComponent.lowercased()) ? url.deletingLastPathComponent() : url
        name = AppModel.siteName(from: base.lastPathComponent)
    }

    private func add(force: Bool) async {
        guard let folder else { return }
        working = true
        defer { working = false }
        do {
            try await model.addSite(name: name, php: php, path: folder.path, force: force)
            added(name)
            dismiss()
        } catch let e as APIError where e.code == "confirmation_required" {
            noIndex = e.message
        } catch {
            self.error = error.localizedDescription
        }
    }
}
