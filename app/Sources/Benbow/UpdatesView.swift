// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import BenbowKit
import SwiftUI

/// The updates list, shown in the toolbar popover and in Settings.
struct UpdatesList: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        let st = model.updates
        VStack(alignment: .leading, spacing: 8) {
            if let updates = st?.updates, !updates.isEmpty {
                ForEach(updates) { u in
                    HStack(alignment: .firstTextBaseline) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(u.title)
                            Text("\(u.current) → \(u.available)").font(.caption).foregroundStyle(.secondary)
                            if let why = u.blocked {
                                Text(why).font(.caption).foregroundStyle(.orange)
                            }
                        }
                        Spacer()
                        Button("Update") { Task { await model.applyUpdates([u.component]) } }
                            .disabled(u.blocked != nil || model.busy != nil)
                    }
                }
            } else {
                Text("Everything is up to date.").foregroundStyle(.secondary)
            }
            if let err = st?.error, !err.isEmpty {
                Text("Last check failed: \(err)").font(.caption).foregroundStyle(.red)
            }
            HStack {
                Text(checkedText(st?.checkedAt)).font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("Check Now") { Task { await model.checkUpdates() } }
                    .disabled(model.busy != nil)
                if (st?.ready.count ?? 0) > 1 {
                    Button("Update All") { Task { await model.applyUpdates() } }
                        .disabled(model.busy != nil)
                }
            }
            Text("Signed by Benbow, checked before installing. The previous version is kept: `benbow update rollback <component>` goes back.")
                .font(.caption2).foregroundStyle(.secondary)
        }
    }

    private func checkedText(_ d: Date?) -> String {
        guard let d else { return "Not checked yet" }
        return "Checked " + d.formatted(.relative(presentation: .named))
    }
}

/// The one discreet badge in the toolbar for every update: a newer Benbow
/// (Sparkle) and component updates. Only there when something is ready.
struct UpdatesBadge: View {
    @Environment(AppModel.self) private var model
    @Environment(AppUpdater.self) private var updater
    @State private var showing = false

    static func count(_ model: AppModel, _ updater: AppUpdater) -> Int {
        (model.updates?.ready.count ?? 0) + (updater.available == nil ? 0 : 1)
    }

    var body: some View {
        let n = Self.count(model, updater)
        Button {
            showing = true
        } label: {
            Label("\(n) Update\(n == 1 ? "" : "s")", systemImage: "arrow.down.circle")
                .labelStyle(.titleAndIcon)
        }
        .help("Updates are available")
        .popover(isPresented: $showing, arrowEdge: .bottom) {
            VStack(alignment: .leading, spacing: 12) {
                if let version = updater.available {
                    HStack(alignment: .firstTextBaseline) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Benbow \(version)")
                            Text("A new version of the app").font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Install…") {
                            showing = false
                            updater.check()
                        }
                    }
                    Divider()
                    Text("Components").font(.headline)
                }
                UpdatesList()
            }
            .padding()
            .frame(width: 360)
        }
    }
}

/// Settings → General → Updates: the daemon's daily component check.
struct ComponentAutoRow: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        Toggle(isOn: Binding(
            get: { model.updates?.auto ?? true },
            set: { on in Task { await model.setAutoUpdates(on) } }
        )) {
            Text("Check for component updates daily")
            Text("Once a day from GitHub (benbowdev/benbow-binaries). Only the daily total of checks is counted, with no identifier. Off: Benbow asks nothing by itself; Check Now still works.")
        }
        .disabled(model.busy != nil)
    }
}
