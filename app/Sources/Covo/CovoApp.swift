// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import SwiftUI

@main
struct CovoApp: App {
    @State private var model = AppModel()
    @State private var updater = AppUpdater()

    var body: some Scene {
        Window("Covo", id: "main") {
            ContentView()
                .environment(model)
                .environment(updater)
                .task { await model.connect() }
                .frame(minWidth: 760, minHeight: 460)
        }
        .windowToolbarStyle(.unified)
        .commands {
            CommandGroup(after: .appInfo) {
                Button("Check for Updates…") { updater.check() }
                    .disabled(!updater.enabled)
            }
            CommandGroup(after: .newItem) {
                Button("Import from Another Tool…") { model.importing = true }
                    .disabled(model.phase != .ready || model.busy != nil)
            }
        }

        WindowGroup("Logs", id: "logs", for: String.self) { $site in
            if let site {
                LogsView(site: site).environment(model)
            }
        }

        MenuBarExtra {
            MenuBarContent().environment(model)
        } label: {
            MenuBarLabel().environment(model)
        }

        Settings {
            SettingsView().environment(model).environment(updater)
        }
        .windowResizability(.contentMinSize)
    }
}
