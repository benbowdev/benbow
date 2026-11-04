// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// A small mark on a site an AI agent is working on.
struct AgentBadge: View {
    let session: AgentSession

    var body: some View {
        if session.isWorking() {
            Image(systemName: "sparkles")
                .foregroundStyle(.purple)
                .help("\(session.agentName) is working on this site since \(session.openedAt.formatted(date: .omitted, time: .shortened)). Keep or undo the changes in its detail.")
        } else {
            Image(systemName: "checklist")
                .foregroundStyle(.orange)
                .help("\(session.agentName) has finished: review the changes, then keep them or undo them all in the site's detail.")
        }
    }
}

/// Shown at the top of a site's detail while an agent session is open.
struct AgentSessionBanner: View {
    @Environment(AppModel.self) private var model
    let site: Site
    let session: AgentSession
    @State private var activity = false
    @State private var undoing = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Image(systemName: working ? "sparkles" : "checklist")
                    .foregroundStyle(working ? .purple : .orange)
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.callout.weight(.semibold))
                    Text(detail).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
            }
            HStack {
                Button("What Changed…") { activity = true }
                Spacer()
                Button("Undo All…") { undoing = true }
                    .disabled(model.busy != nil || !session.databaseSaved)
                Button("Keep Changes") { Task { await model.closeSession(site, keep: true) } }
                    .disabled(model.busy != nil)
            }
        }
        .padding(10)
        .background((working ? Color.purple : Color.orange).opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
        .sheet(isPresented: $activity) { AgentActivitySheet(site: site, session: session) }
        .sheet(isPresented: $undoing) { UndoSessionSheet(site: site, session: session) }
    }

    private var working: Bool { session.isWorking() }

    private var title: String {
        let since = session.openedAt.formatted(date: .omitted, time: .shortened)
        if working { return "\(session.agentName) is working on this site since \(since)" }
        return "\(session.agentName) has finished (session started at \(since)): review the changes, then keep or undo them"
    }

    private var detail: String {
        var parts: [String] = []
        if session.launchedTool?.isEmpty == false {
            parts.append(working ? "the agent is running" : "the agent's terminal session has ended")
        }
        if !session.databaseSaved {
            parts.append("saving the starting database…")
        } else {
            parts.append("files and database saved as they were at the start")
        }
        if session.writeGrant == true { parts.append("agents may change this site until you close the session") }
        if !session.checkpoints.isEmpty { parts.append("\(session.checkpoints.count) checkpoint(s)") }
        return parts.joined(separator: " · ")
    }
}

/// "Undo All": what it does, before doing it.
struct UndoSessionSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    let session: AgentSession

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Undo everything since \(session.openedAt.formatted(date: .omitted, time: .shortened))?")
                .font(.title3.weight(.semibold))
            ExplainBox(
                title: "What Undo All does",
                steps: [
                    "Covo saves the site as it is now (a pre-restore snapshot), so this can be undone too.",
                    "It puts the project folder back as it was when \(session.agentName) started: files the agent added are removed, changed and deleted ones come back.",
                    "It puts the database \(site.database ?? "") back as it was at the start.",
                ],
                caution: "This also undoes the changes you made yourself during the session: Covo does not know who changed what. .git and node_modules are never touched."
            )
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Undo All", role: .destructive) {
                    Task {
                        await model.closeSession(site, keep: false)
                        dismiss()
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(model.busy != nil)
            }
        }
        .padding()
        .frame(width: 520)
    }
}

/// What an agent session changed, and what the agent did through Covo.
struct AgentActivitySheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    let session: AgentSession
    @State private var diff: SessionDiff?
    @State private var actions: [MCPAction] = []
    @State private var loading = true

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("\(session.agentName) on \(site.name)").font(.title3.weight(.semibold))
            Text("Since \(session.openedAt.formatted(date: .abbreviated, time: .shortened)). Starting snapshot \(session.baseSnapshot).")
                .font(.caption).foregroundStyle(.secondary)
            if loading {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    if let d = diff {
                        Section("Files: \(d.files.added) added, \(d.files.modified) modified, \(d.files.deleted) deleted") {
                            if d.files.changes.isEmpty { Text("No file changed.").foregroundStyle(.secondary) }
                            ForEach(d.files.changes) { f in
                                HStack {
                                    Image(systemName: f.change == "added" ? "plus.circle" : f.change == "deleted" ? "minus.circle" : "pencil.circle")
                                        .foregroundStyle(f.change == "added" ? .green : f.change == "deleted" ? .red : .orange)
                                    Text(f.path).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                                }
                            }
                            if d.files.truncated == true { Text("… more in `covo agent diff \(site.name)`").font(.caption).foregroundStyle(.secondary) }
                        }
                        if let db = d.database {
                            Section("Database \(db.database)\(db.approximate == true ? " (approximate)" : "")") {
                                if db.tables.isEmpty { Text("No table changed.").foregroundStyle(.secondary) }
                                ForEach(db.tables) { t in
                                    HStack {
                                        Text(t.table).font(.callout.monospaced())
                                        Spacer()
                                        Text(tableSummary(t)).font(.caption).foregroundStyle(.secondary)
                                    }
                                }
                                if let note = db.note { Text(note).font(.caption).foregroundStyle(.secondary) }
                            }
                        }
                    }
                    Section("What the agent did through Covo") {
                        if actions.isEmpty { Text("Nothing through Covo's tools (the agent may have used its own).").foregroundStyle(.secondary) }
                        ForEach(actions) { a in
                            HStack(alignment: .firstTextBaseline) {
                                Image(systemName: a.error != nil ? "xmark.circle" : a.snapshot != nil ? "camera" : "checkmark.circle")
                                    .foregroundStyle(a.error != nil ? .red : .secondary)
                                VStack(alignment: .leading, spacing: 1) {
                                    Text(a.tool).font(.callout)
                                    if let e = a.error {
                                        Text(e).font(.caption).foregroundStyle(.red).lineLimit(2)
                                    } else if let s = a.snapshot {
                                        Text("checkpoint \(s) before").font(.caption).foregroundStyle(.secondary)
                                    }
                                }
                                Spacer()
                                Text(a.at.formatted(date: .omitted, time: .shortened)).font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            HStack {
                Button("Refresh") { Task { await load() } }
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding()
        .frame(width: 620, height: 560)
        .task { await load() }
    }

    private func tableSummary(_ t: SessionDiff.TableChange) -> String {
        switch t.change {
        case "added": return "new table"
        case "dropped": return "dropped"
        default:
            var p: [String] = []
            if let i = t.inserted, i > 0 { p.append("+\(i)") }
            if let u = t.updated, u > 0 { p.append("~\(u)") }
            if let d = t.deleted, d > 0 { p.append("−\(d)") }
            if t.change == "altered" { p.append("columns changed") }
            return p.joined(separator: " ")
        }
    }

    private func load() async {
        loading = true
        diff = await model.sessionDiff(site)
        actions = await model.mcpActions().filter { $0.site == site.name && $0.at >= session.openedAt }
        loading = false
    }
}

/// "Work with an Agent": choose one of the agents on this Mac; Covo opens a
/// session, lets agents change this site until it closes, and starts the
/// agent in the site's folder.
struct WorkWithAgentSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    @State private var tools: AgentTools?
    @State private var choice = ""
    @State private var terminal = "Terminal"
    @State private var needsConnect: String?
    @State private var started: AgentStartResult?

    private var installed: [AgentTool] { (tools?.agents ?? []).filter { $0.installed && $0.kind != "desktop" } }
    private var chosen: AgentTool? { installed.first { $0.id == choice } }
    private var terminals: [String] {
        let names = ToolApp.terminals.map(\.name).filter { ["Terminal", "iTerm", "Ghostty"].contains($0) }
        return names.isEmpty ? ["Terminal"] : names
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Work with an Agent on \(site.name)").font(.title3.weight(.semibold))
            if let started {
                Label("\(chosenName) started\(started.started.terminal.map { " in a new \($0) window" } ?? ""). The session is open: keep or undo the changes from this site's detail.",
                      systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.green)
                HStack { Spacer(); Button("Done") { dismiss() }.keyboardShortcut(.defaultAction) }
            } else if tools == nil {
                ProgressView("Looking for agents…").frame(maxWidth: .infinity)
            } else if installed.isEmpty {
                Text("No AI agent found on this Mac. Covo works with Claude Code, Codex, Gemini CLI, Cursor and GitHub Copilot CLI; install one, then come back (Covo never installs agents).")
                    .foregroundStyle(.secondary)
                HStack { Spacer(); Button("Close") { dismiss() }.keyboardShortcut(.cancelAction) }
            } else {
                Picker("Agent", selection: $choice) {
                    ForEach(installed) { a in Text(a.name).tag(a.id) }
                }
                if chosen?.kind == "terminal", terminals.count > 1 {
                    Picker("Terminal", selection: $terminal) {
                        ForEach(terminals, id: \.self) { Text($0).tag($0) }
                    }
                }
                ExplainBox(
                    title: "What happens",
                    steps: steps,
                    caution: caution
                )
                if let needsConnect {
                    Text(needsConnect).font(.caption).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
                }
                HStack {
                    Spacer()
                    Button("Cancel", role: .cancel) { dismiss() }.keyboardShortcut(.cancelAction)
                    Button(needsConnect == nil ? "Start \(chosenName)" : "Add Covo and Start") { Task { await start(connect: needsConnect != nil) } }
                        .keyboardShortcut(.defaultAction)
                        .disabled(choice.isEmpty || model.busy != nil)
                }
            }
        }
        .padding()
        .frame(width: 560)
        .task {
            tools = await model.agentTools()
            choice = installed.first { $0.id == tools?.last }?.id ?? installed.first?.id ?? ""
            if let t = tools?.terminal, terminals.contains(t) { terminal = t }
        }
        .onChange(of: choice) { needsConnect = nil }
    }

    private var chosenName: String { chosen?.name ?? "the agent" }

    private var steps: [String] {
        var s = [
            "Covo saves \(site.name)'s files and database as they are now (an agent session): you can keep the changes or undo them all at the end.",
            "Agents may change \(site.name) through Covo until you close the session; nothing else changes in your settings.",
        ]
        if chosen?.kind == "app" {
            s.append("\(chosenName) opens the site's folder; it reaches Covo through its own settings.")
        } else {
            s.append("\(chosenName) starts in a new \(terminal) window in the site's folder, with Covo connected and told about the site.")
        }
        return s
    }

    private var caution: String? {
        guard let a = chosen else { return nil }
        if !a.perRun && !a.connected {
            return "\(a.name) only sees Covo through its own settings (\(a.configPath ?? "")): Covo adds itself there, after a backup. Nothing is written into the project folder."
        }
        return "Nothing is written into the project folder: Covo's files for this start stay in Covo's own folder."
    }

    private func start(connect: Bool) async {
        let needsGlobal = chosen.map { !$0.perRun && !$0.connected } ?? false
        switch await model.startAgent(site, tool: choice, terminal: chosen?.kind == "terminal" ? terminal : nil, connect: connect || needsGlobal) {
        case .started(let r): started = r
        case .needsConnect(let m): needsConnect = m
        case .failed: break
        }
    }
}
