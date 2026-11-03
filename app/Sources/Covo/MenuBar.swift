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
            ForEach(model.status?.services ?? [], id: \.name) { svc in
                Label {
                    Text("\(Self.title(svc.name))  ·  \(svc.running ? "running" : "stopped")")
                } icon: {
                    Image(nsImage: StatusDot.image(color(svc)))
                }
            }
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
            if let phone = model.status?.phone {
                Divider()
                Text("\(phone.site) on phone · \(phone.minutesLeft) min left")
                Button("Stop Sharing \(phone.site)") { Task { await model.stopPhone() } }
            }
            if let n = model.updates?.ready.count, n > 0 {
                Divider()
                Button("Install \(n) Update\(n == 1 ? "" : "s")") { Task { await model.applyUpdates() } }
                    .disabled(model.busy != nil)
            }
            Divider()
            if model.status?.mysql.enabled == true {
                Button("Adminer") { Task { await model.openAdminer() } }
                    .disabled(!model.running || !(model.mysqlReady || model.postgresReady))
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

extension MenuBarContent {
    /// Traffic light: green running, yellow while Covo is changing things,
    /// red when a service should run but does not, grey when all is stopped.
    func color(_ svc: Service) -> NSColor {
        if model.busy != nil { return .systemYellow }
        if svc.running { return .systemGreen }
        let anyRunning = model.status?.services.contains(where: \.running) ?? false
        return anyRunning ? .systemRed : .systemGray
    }

    static func title(_ service: String) -> String {
        switch service {
        case "caddy": return "Web server"
        case "mysql": return "MySQL"
        case "mailpit": return "Mailpit"
        case "dns": return "DNS for .test"
        default:
            if let v = service.split(separator: "-").last, service.hasPrefix("php-") { return "PHP \(v)" }
            return service
        }
    }
}

/// A coloured dot for menu items (not a template image, so macOS keeps the
/// colour).
enum StatusDot {
    static func image(_ color: NSColor) -> NSImage {
        let image = NSImage(size: NSSize(width: 10, height: 10), flipped: false) { rect in
            color.setFill()
            NSBezierPath(ovalIn: rect.insetBy(dx: 1, dy: 1)).fill()
            return true
        }
        image.isTemplate = false
        return image
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
