// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import SwiftUI

@main
struct CovoApp: App {
    @State private var model = AppModel()

    var body: some Scene {
        Window("Covo", id: "main") {
            ContentView()
                .environment(model)
                .task { await model.connect() }
                .frame(minWidth: 760, minHeight: 460)
        }
        .windowToolbarStyle(.unified)

        Settings {
            SettingsView().environment(model)
        }
    }
}
