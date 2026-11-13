// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
import BenbowKit
import SwiftUI

/// One service as the app shows it: what it is, a short detail (port,
/// version, sites) and whether it runs.
struct ServiceLine: Identifiable, Hashable {
    enum State { case running, stopped, failed, off }

    var id: String
    var title: String
    var detail: String
    var state: State

    var color: Color {
        switch state {
        case .running: .green
        case .failed: .red
        case .stopped, .off: Color.secondary.opacity(0.5)
        }
    }

    var stateText: String {
        switch state {
        case .running: "Running"
        case .stopped: "Stopped"
        case .failed: "Not running"
        case .off: "Off"
        }
    }
}

extension AppModel {
    /// The services Benbow runs now (from the core's status), then the
    /// optional ones that are turned off.
    var serviceLines: [ServiceLine] {
        guard let st = status else { return [] }
        let anyRunning = st.services.contains(where: \.running)
        var lines = st.services.map { svc in
            ServiceLine(id: svc.name, title: Self.serviceTitle(svc.name), detail: serviceDetail(svc.name, st),
                        state: svc.running ? .running : (anyRunning ? .failed : .stopped))
        }
        let names = Set(st.services.map(\.name))
        let optional: [(String, String, Bool)] = [
            ("mysql", "MySQL 8.4", st.mysql.enabled),
            ("mysql57", "MySQL 5.7", st.mysql57?.enabled == true),
            ("postgres", "PostgreSQL", st.postgres?.enabled == true),
            ("redis", "Redis", st.redis?.enabled == true),
            ("mailpit", "Mail", st.mail?.enabled == true),
        ]
        for (name, title, enabled) in optional where !enabled && !names.contains(name) {
            lines.append(ServiceLine(id: name, title: title, detail: "Turn on in Settings", state: .off))
        }
        return lines
    }

    static func serviceTitle(_ service: String) -> String {
        switch service {
        case "caddy": return "Web server"
        case "mysql": return "MySQL 8.4"
        case "mysql57": return "MySQL 5.7"
        case "postgres": return "PostgreSQL"
        case "redis": return "Redis"
        case "mailpit": return "Mail"
        case "dns": return "DNS for .test"
        case "phone": return "Open on phone"
        default:
            if service.hasPrefix("php-") { return "PHP \(service.dropFirst(4))" }
            return service
        }
    }

    private func serviceDetail(_ service: String, _ st: Status) -> String {
        func server(_ s: MySQLStatus?) -> String {
            [s?.version, s?.port.map { "port \($0)" }].compactMap { $0 }.joined(separator: " · ")
        }
        switch service {
        case "caddy":
            return "HTTPS port \(st.httpsPort ?? 8443)"
        case "mysql": return server(st.mysql)
        case "mysql57": return server(st.mysql57)
        case "postgres": return server(st.postgres)
        case "redis": return server(st.redis)
        case "mailpit":
            return st.mail?.smtpPort.map { "SMTP port \($0)" } ?? ""
        case "dns": return ".test addresses"
        case "phone": return st.phone.map { $0.site } ?? ""
        default:
            guard service.hasPrefix("php-") else { return "" }
            let minor = String(service.dropFirst(4))
            let n = st.sites.filter { $0.php == minor && $0.enabled && !$0.archived && !$0.isApp }.count
            switch n {
            case 0: return "Adminer"
            case 1: return "1 site"
            default: return "\(n) sites"
            }
        }
    }
}

/// Toolbar button next to Start/Stop: how many services are on (a warning
/// when one should run but does not), and the full list in a popover.
struct ServicesButton: View {
    @Environment(AppModel.self) private var model
    @State private var showing = false

    var body: some View {
        let lines = model.serviceLines.filter { $0.state != .off }
        Button {
            showing.toggle()
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "square.stack.3d.up")
                Text(lines.isEmpty ? "Services" : "\(lines.count) Services")
                if lines.contains(where: { $0.state == .failed }) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                }
            }
            .fixedSize()
            .padding(.horizontal, 4)
        }
        .help(lines.map { "\($0.title): \($0.stateText.lowercased())" }.joined(separator: "\n"))
        .popover(isPresented: $showing, arrowEdge: .bottom) {
            ServicesPopover()
        }
    }
}

struct ServicesPopover: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        let lines = model.serviceLines
        let on = lines.filter { $0.state != .off }
        let off = lines.filter { $0.state == .off }
        VStack(alignment: .leading, spacing: 10) {
            Text(model.running ? "Benbow is running" : "Benbow is stopped")
                .font(.headline)
            Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 6) {
                ForEach(on) { row($0) }
            }
            if !off.isEmpty {
                Divider()
                Text("Off").font(.caption).foregroundStyle(.secondary)
                Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 6) {
                    ForEach(off) { row($0) }
                }
            }
            Divider()
            HStack {
                Spacer()
                SettingsLink { Text("Settings…") }
            }
        }
        .padding(14)
        .frame(minWidth: 300)
    }

    private func row(_ line: ServiceLine) -> some View {
        GridRow {
            Circle().fill(line.color).frame(width: 8, height: 8)
            Text(line.title)
                .foregroundStyle(line.state == .off ? .secondary : .primary)
            Text(line.state == .running || line.state == .off ? line.detail : line.stateText)
                .foregroundStyle(line.state == .failed ? .red : .secondary)
                .font(.callout)
        }
    }
}
