// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import BenbowKit
import SwiftUI

/// The state of a dev server as a colored dot and a word.
struct AppStateLabel: View {
    let state: AppStatus?

    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(word)
        }
    }

    private var word: String {
        switch state?.state {
        case "running": "Running"
        case "starting": "Starting…"
        case "error": "Error"
        case "exited": "Ended"
        case "waiting": "Not listening"
        default: "Stopped"
        }
    }

    private var color: Color {
        switch state?.state {
        case "running": .green
        case "starting": .yellow
        case "error": .red
        case "exited", "waiting": .orange
        default: .secondary
        }
    }
}

/// The message and last output lines of a dev server that failed.
struct AppProblem: View {
    let state: AppStatus

    var body: some View {
        if let msg = state.message, !msg.isEmpty {
            Text(msg).font(.callout).foregroundStyle(state.state == "error" ? .red : .secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        if let tail = state.logTail, !tail.isEmpty {
            DisclosureGroup("Last lines of the output") {
                Text(tail.joined(separator: "\n"))
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .font(.callout)
        }
    }
}

/// The app's lines in the site detail: its command (or proxied port) and
/// its dev server's state, in place of the PHP version.
struct AppRows: View {
    @Environment(AppModel.self) private var model
    let site: Site
    @State private var editing = false

    var body: some View {
        LabeledContent {
            HStack {
                if site.appMode == "proxy" {
                    Text(verbatim: "Port \(site.appPort ?? 0), run by you").foregroundStyle(.secondary)
                } else {
                    Text(site.command ?? "").font(.system(.body, design: .monospaced))
                        .lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                }
                Button("Edit…") { editing = true }.disabled(model.busy != nil)
            }
        } label: {
            Text(site.appMode == "proxy" ? "Dev server" : "Command")
            if site.appMode != "proxy", let port = site.appPort {
                Text(verbatim: "Port \(port) ($PORT, {port})").font(.caption)
            }
        }
        LabeledContent {
            HStack {
                AppStateLabel(state: site.app)
                if site.appMode != "proxy" {
                    Button("Restart") { Task { await model.restartApp(site) } }
                        .disabled(!model.running || !site.enabled || model.busy != nil)
                        .help("Stop every process of the command and start it again")
                }
            }
        } label: {
            Text("Status")
        }
        if let st = site.app, st.state == "error" || st.state == "exited" || st.state == "waiting" {
            AppProblem(state: st)
        }
        Text("Benbow runs your command with your own Node, Python or other tools and never installs them.")
            .font(.caption).foregroundStyle(.secondary)
            .sheet(isPresented: $editing) { AppEditSheet(site: site) }
    }
}

/// Changes an app's command or proxied port.
struct AppEditSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    @State private var mode = "run"
    @State private var command = ""
    @State private var port = ""
    @State private var proxyPort = ""
    @State private var detection: AppDetection?
    @State private var error: String?

    var body: some View {
        Form {
            Section {
                Picker("Dev server", selection: $mode) {
                    Text("Benbow starts it").tag("run")
                    Text("I run it myself").tag("proxy")
                }
                .pickerStyle(.segmented)
                if mode == "run" {
                    TextField("Command", text: $command, prompt: Text("npm run dev"))
                        .font(.system(.body, design: .monospaced))
                    TextField("Port", text: $port, prompt: Text(verbatim: "\(site.appPort ?? 41000)"))
                    if let d = detection, !d.command.isEmpty, d.command != command {
                        LabeledContent("Benbow proposes") {
                            HStack {
                                Text(d.command).font(.system(.callout, design: .monospaced)).lineLimit(1)
                                Button("Use") { command = d.command }
                            }
                        }
                    }
                } else {
                    TextField("Port", text: $proxyPort, prompt: Text("3000"))
                }
            } footer: {
                Text(mode == "run"
                     ? "The command runs in your login shell, in \(site.path). $PORT and {port} are the port above; changing them restarts the app."
                     : "Benbow only puts the address and HTTPS in front of the dev server you start on this port.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout)
            }
        }
        .formStyle(.grouped)
        .frame(width: 520)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) {
                Button("Save") { Task { await save() } }.disabled(!valid || model.busy != nil)
            }
        }
        .onAppear {
            mode = site.appMode ?? "run"
            command = site.command ?? ""
            proxyPort = site.appMode == "proxy" ? "\(site.appPort ?? 0)" : ""
        }
        .task { detection = await model.detectApp(site.path) }
    }

    private var valid: Bool {
        if mode == "run" {
            return !command.trimmingCharacters(in: .whitespaces).isEmpty && (port.isEmpty || Int(port) != nil)
        }
        return Int(proxyPort).map { (1024...65535).contains($0) } ?? false
    }

    private func save() async {
        error = nil
        do {
            if mode == "run" {
                try await model.updateApp(site, command: command.trimmingCharacters(in: .whitespaces), port: Int(port))
            } else {
                try await model.updateApp(site, proxyPort: Int(proxyPort))
            }
            dismiss()
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// An app's own environment variables (Advanced): names only, values go
/// into the login keychain and never come back to the app.
struct AppEnvEditor: View {
    @Environment(AppModel.self) private var model
    let site: Site
    @State private var env: AppEnv?
    @State private var name = ""
    @State private var value = ""
    @State private var error: String?

    var body: some View {
        LabeledContent {
            VStack(alignment: .trailing, spacing: 4) {
                ForEach(env?.names ?? [], id: \.self) { n in
                    HStack {
                        Text(n).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                        Button {
                            Task { await save(unset: [n]) }
                        } label: {
                            Image(systemName: "minus.circle.fill")
                        }
                        .buttonStyle(.borderless)
                        .help("Remove \(n)")
                        .disabled(model.busy != nil)
                    }
                }
                HStack {
                    TextField("Name", text: $name, prompt: Text("API_KEY"))
                        .labelsHidden().frame(width: 140)
                        .font(.system(.body, design: .monospaced))
                    SecureField("Value", text: $value, prompt: Text("value"))
                        .labelsHidden().frame(width: 160)
                    Button("Add") { Task { await save(set: [name.trimmingCharacters(in: .whitespaces): value]) } }
                        .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || model.busy != nil)
                }
            }
        } label: {
            Text("Variables")
            Text("Kept in your login keychain; only the app's process sees the values. A change restarts the app.")
        }
        if let provided = env?.provided, !provided.isEmpty {
            Text("Benbow also sets \(provided.joined(separator: ", ")); yours win.")
                .font(.caption).foregroundStyle(.secondary)
        }
        if let error {
            Text(error).font(.callout).foregroundStyle(.red)
        }
        Color.clear.frame(height: 0)
            .task(id: site.name) { env = await model.appEnv(site) }
    }

    private func save(set: [String: String] = [:], unset: [String] = []) async {
        error = nil
        do {
            if let new = try await model.setAppEnv(site, set: set, unset: unset) {
                env = new
                name = ""
                value = ""
            }
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// A PHP site's companion (Advanced): a dev server started with the site,
/// e.g. Vite next to Laravel.
struct CompanionRow: View {
    @Environment(AppModel.self) private var model
    let site: Site
    @State private var command = ""
    @State private var error: String?

    private var proposal: String {
        site.framework == "laravel" ? "npm run dev -- --port {port} --strictPort" : "npm run dev"
    }

    var body: some View {
        LabeledContent {
            HStack {
                TextField("Companion", text: $command, prompt: Text(proposal))
                    .labelsHidden()
                    .font(.system(.body, design: .monospaced))
                    .multilineTextAlignment(.trailing)
                    .onSubmit { Task { await save(command) } }
                Button("Save") { Task { await save(command) } }
                    .disabled(command == (site.companionCommand ?? "") || model.busy != nil)
                if site.companionCommand != nil {
                    Button("Remove") { Task { await save("") } }.disabled(model.busy != nil)
                }
            }
        } label: {
            Text("Companion")
            Text(site.framework == "laravel"
                 ? "Started with the site, e.g. Vite: Benbow gives it HTTPS on this site's domain."
                 : "A dev server started and stopped with the site.")
        }
        if let co = site.companion {
            LabeledContent {
                HStack {
                    AppStateLabel(state: co)
                    Button("Restart") { Task { await model.restartApp(site) } }
                        .disabled(!model.running || !site.enabled || model.busy != nil)
                }
            } label: {
                Text("Companion status")
                Text(verbatim: "Port \(co.port) ($PORT, {port})")
            }
            if co.state == "error" || co.state == "exited" {
                AppProblem(state: co)
            }
        }
        if let error {
            Text(error).font(.callout).foregroundStyle(.red)
        }
        Color.clear.frame(height: 0)
            .onAppear { command = site.companionCommand ?? "" }
            .onChange(of: site.companionCommand) { _, new in command = new ?? "" }
    }

    private func save(_ cmd: String) async {
        error = nil
        do {
            try await model.updateApp(site, companion: cmd.trimmingCharacters(in: .whitespaces))
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// "Send Host: localhost" (Advanced, apps).
struct HostHeaderToggle: View {
    @Environment(AppModel.self) private var model
    let site: Site

    var body: some View {
        Toggle(isOn: Binding(
            get: { site.appHost == "localhost" },
            set: { on in Task { try? await model.updateApp(site, appHost: on ? "localhost" : "") } }
        )) {
            Text("Send Host: localhost")
            Text("For dev servers that refuse other host names. Links they build then use localhost.")
        }
        .disabled(model.busy != nil)
    }
}
