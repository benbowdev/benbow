// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
import CovoKit
import SwiftUI

/// The menu bar extra: global Start/Stop and the sites, for quick opening.
struct MenuBarContent: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        if model.phase == .ready {
            Text(model.busy ?? (model.running ? "All sites running" : "All sites stopped"))
            Button(model.running ? "Stop All Sites" : "Start All Sites") {
                Task { await model.toggleRunning() }
            }
            .disabled(model.busy != nil)
            Divider()
            let sites = model.sites.filter(\.enabled)
            if sites.isEmpty {
                Text("No sites")
            }
            ForEach(sites) { site in
                Button(site.domain) { open(site.url) }
                    .disabled(!model.running)
            }
            Divider()
            if model.status?.mysql.enabled == true {
                Button("Adminer") { Task { await model.openAdminer() } }
                    .disabled(!model.running || !model.mysqlReady)
            }
            if let mail = model.status?.mail, mail.enabled, let u = mail.url {
                Button("Mail Inbox") { open(u) }.disabled(!mail.running)
            }
        } else {
            Text("Covo is starting…")
        }
        Button("Open Covo") {
            openWindow(id: "main")
            NSApp.activate(ignoringOtherApps: true)
        }
        Divider()
        Button("Quit Covo") { NSApp.terminate(nil) }
            .keyboardShortcut("q")
    }
}

/// The black C from the brand kit, tinted by macOS for light and dark bars.
struct MenuBarLabel: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        if let image = Self.image {
            Image(nsImage: image)
                .opacity(model.running ? 1 : 0.5)
        } else {
            Image(systemName: "server.rack")
        }
    }

    static let image: NSImage? = {
        guard let image = NSImage(named: "covo-menubar-template") else { return nil }
        image.isTemplate = true
        image.size = NSSize(width: 16, height: 16) // pixel art: native size, no smoothing
        return image
    }()
}
