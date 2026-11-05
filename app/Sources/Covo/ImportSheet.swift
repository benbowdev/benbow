// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// File → Import…: sites from MAMP PRO, Local, Herd, Valet or XAMPP, which
/// are only read. Optionally each site is migrated: its database copied to
/// Covo and its wp-config.php or .env pointed at Covo (with a backup, and
/// undo in the site detail).
struct ImportSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss

    static let sources: [(id: String, title: String)] = [
        ("mamp", "MAMP PRO"), ("local", "Local"), ("herd", "Laravel Herd"), ("valet", "Laravel Valet"), ("xampp", "XAMPP"),
    ]

    @State private var source = "mamp"
    @State private var keepNames = true
    @State private var migrate = false
    @State private var existing = "use"
    @State private var sourcePassword = ""
    @State private var selected: Set<String> = []
    @State private var preview: ImportPlan?
    @State private var result: ImportPlan?
    @State private var error: String?
    @State private var loading = false
    @State private var working = false
    @State private var confirming = false

    private var title: String { Self.sources.first { $0.id == source }?.title ?? source }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let result {
                done(result)
            } else {
                header
                Divider()
                list
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled)
            }
            footer
        }
        .padding()
        .frame(minWidth: 720, idealWidth: 780, maxWidth: .infinity,
               minHeight: result == nil ? 600 : nil, idealHeight: result == nil ? 780 : nil,
               maxHeight: result == nil ? .infinity : nil)
        .fixedSize(horizontal: false, vertical: result != nil)
        .task(id: "\(source)-\(keepNames)-\(migrate)-\(existing)") { await load() }
        .alert("Migrate \(selected.count) site\(selected.count == 1 ? "" : "s")?", isPresented: $confirming) {
            Button("Migrate") { Task { await run() } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("After the migration these sites work from Covo. Opened from \(title) they will no longer connect to their database and will show old data: the project files are shared between the two. \(title)'s databases are only read. Each site can be undone from its detail (Advanced).")
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Import from").font(.title3.weight(.semibold))
                Picker("", selection: $source) {
                    ForEach(Self.sources, id: \.id) { Text($0.title).tag($0.id) }
                }
                .labelsHidden()
                .fixedSize()
            }
            Text("Covo reads \(title)'s site list and creates the same sites on the same folders. \(title) is not changed and can keep running.")
                .font(.callout).foregroundStyle(.secondary)
            Picker("Addresses", selection: $keepNames) {
                Text("Keep their names (\(source == "herd" || source == "valet" ? ".test" : ".local"))").tag(true)
                Text("Use name.localhost (no setup)").tag(false)
            }
            .pickerStyle(.radioGroup)
            if keepNames, preview?.needsLocal == true {
                Label("Keeping .local names turns on Covo's MAMP compatibility mode: macOS asks once for your administrator password.",
                      systemImage: "lock")
                    .font(.caption).foregroundStyle(.orange)
            }
            if keepNames, preview?.needsTest == true {
                Label("Keeping .test names turns on .test domains: macOS asks once for your administrator password.", systemImage: "lock")
                    .font(.caption).foregroundStyle(.orange)
            }
            Toggle(isOn: $migrate) {
                Text("Also migrate: copy each database to Covo and point wp-config.php or .env at it")
                Text("Without this, the sites use the folder as is and you set the database by hand.")
            }
            if migrate {
                ExplainBox(
                    title: "What migrating does, for each selected site",
                    steps: [
                        "Covo reads the database from \(title)'s MySQL (only reads: \(title) and its data are not changed) and copies it into Covo's MySQL, with its own user.",
                        "It saves a backup of wp-config.php (or .env) in Covo's folder, then changes only the database name, user, password and host in it.",
                        "It replaces the old address (e.g. http://site.local:8888) with the Covo one in the database copy.",
                    ],
                    caution: "The project folder is shared with \(title): from now on the site opened in \(title) no longer finds its database. Undo Migration (site → Advanced) puts wp-config.php back and deletes the database copy Covo made.",
                    collapsible: true
                )
                Picker("If Covo already has the database", selection: $existing) {
                    Text("Use it").tag("use")
                    Text("Copy again (after a backup)").tag("recopy")
                    Text("Skip the site").tag("skip")
                }
                .fixedSize()
                if !model.mysqlReady {
                    Label("Migrating needs Covo's MySQL running (Settings → MySQL, then start Covo).", systemImage: "exclamationmark.triangle")
                        .font(.caption).foregroundStyle(.orange)
                }
                Text("\(title)'s MySQL must be running (Covo never starts it); Covo connects with the user in wp-config.php or .env.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder private var list: some View {
        if loading && preview == nil {
            ProgressView("Reading \(title)…").frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let p = preview {
            List {
                Section {
                    ForEach(Array(p.items.enumerated()).filter { $0.element.creates }, id: \.offset) { _, it in
                        row(it)
                    }
                } header: {
                    let all = Set(p.items.filter(\.creates).map(\.mamp.serverName))
                    HStack {
                        Text("\(p.create) can be imported")
                        Spacer()
                        Button(selected.isEmpty ? "Select All" : "Select None") {
                            selected = selected.isEmpty ? all : []
                        }
                        .buttonStyle(.link)
                        .disabled(all.isEmpty)
                    }
                }
                if p.skip > 0 {
                    Section("\(p.skip) skipped") {
                        ForEach(Array(p.items.enumerated()).filter { !$0.element.creates }, id: \.offset) { _, it in
                            HStack {
                                Text(it.mamp.serverName)
                                Spacer()
                                Text(it.reason ?? "").font(.caption).foregroundStyle(.secondary)
                            }
                            .foregroundStyle(.secondary)
                        }
                    }
                }
            }
            .frame(minHeight: 260)
            .layoutPriority(1)
        } else {
            Spacer()
        }
    }

    private func row(_ it: ImportItem) -> some View {
        let key = it.mamp.serverName
        return HStack(alignment: .top) {
            Toggle("", isOn: Binding(
                get: { selected.contains(key) },
                set: { if $0 { selected.insert(key) } else { selected.remove(key) } }
            ))
            .labelsHidden()
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(it.domain ?? key).font(.body.weight(.medium))
                    Spacer()
                    Text("PHP \(it.php ?? "")").foregroundStyle(.secondary)
                }
                Text(it.mamp.documentRoot).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                ForEach(it.warnings, id: \.self) { w in
                    Label(w, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                }
                if let m = it.migration {
                    if let problem = m.problem {
                        Label("Not migrated: \(problem)", systemImage: "minus.circle").font(.caption).foregroundStyle(.secondary)
                    } else {
                        MigrationSteps(database: m.database, steps: m.steps)
                        ForEach(m.warnings ?? [], id: \.self) { w in
                            Label(w, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                        }
                    }
                }
            }
        }
    }

    private func done(_ r: ImportPlan) -> some View {
        let imported = r.items.filter { $0.creates && (selected.isEmpty || selected.contains($0.mamp.serverName)) }
        let migrated = r.items.filter { $0.migration?.done == true }
        let failed = r.items.filter { $0.migration?.error != nil }
        return VStack(alignment: .leading, spacing: 10) {
            Label("Imported \(r.create) site\(r.create == 1 ? "" : "s") from \(title)", systemImage: "checkmark.circle.fill")
                .font(.title3.weight(.semibold)).foregroundStyle(.green)
            if !migrated.isEmpty {
                Text("\(migrated.count == 1 ? "1 site was" : "\(migrated.count) sites were") migrated: \(migrated.count == 1 ? "it now works" : "they now work") from Covo with \(migrated.count == 1 ? "its" : "their") own database copy. Undo from the site's detail (Advanced).")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            VStack(spacing: 0) {
                ForEach(Array(imported.prefix(8).enumerated()), id: \.offset) { i, it in
                    if i > 0 { Divider() }
                    doneRow(it, served: r.applied)
                }
                if imported.count > 8 {
                    Divider()
                    Text("and \(imported.count - 8) more in the site list").font(.caption).foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading).padding(.vertical, 6)
                }
            }
            .padding(.horizontal, 10)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
            ForEach(Array(failed.enumerated()), id: \.offset) { _, it in
                VStack(alignment: .leading, spacing: 2) {
                    Text(it.domain ?? it.mamp.serverName).font(.body.weight(.medium))
                    Text("Migration failed and was undone: \(it.migration?.error ?? "")")
                        .font(.caption).foregroundStyle(.orange).textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            if !r.applied {
                Text("Start Covo to serve them.").foregroundStyle(.secondary)
            }
        }
    }

    private func doneRow(_ it: ImportItem, served: Bool) -> some View {
        let site = model.sites.first { $0.domain == it.domain }
        return HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(it.domain ?? it.mamp.serverName).font(.body.weight(.medium))
                Text(it.migration?.done == true ? "Migrated, database \(it.migration?.database ?? "")" : "Imported, database to set by hand")
                    .font(.caption).foregroundStyle(.secondary)
                ForEach(it.migration?.done == true ? it.migration?.warnings ?? [] : [], id: \.self) { w in
                    Label(w, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer()
            if let site, served {
                Button("Open") { open(site.url) }
            }
        }
        .padding(.vertical, 6)
    }

    private var footer: some View {
        HStack {
            if working { ProgressView().controlSize(.small); Text(model.busy ?? "Importing…").foregroundStyle(.secondary) }
            Spacer()
            if result != nil {
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            } else {
                Button("Cancel", role: .cancel) { dismiss() }.disabled(working)
                Button(migrate ? "Import and Migrate \(selected.count)" : "Import \(selected.count) Sites") {
                    if migrate { confirming = true } else { Task { await run() } }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(selected.isEmpty || working || loading || (migrate && !model.mysqlReady))
            }
        }
    }

    private func options(dryRun: Bool) -> CovoAPI.ImportOptions {
        var o = CovoAPI.ImportOptions()
        o.dryRun = dryRun
        o.localhost = !keepNames
        o.migrate = migrate
        o.existing = existing
        if !dryRun { o.sites = Array(selected) }
        return o
    }

    private func load() async {
        loading = true
        defer { loading = false }
        error = nil
        do {
            let p = try await model.importSites(from: source, options(dryRun: true))
            preview = p
            selected = Set((p?.items ?? []).filter(\.creates).map(\.mamp.serverName))
        } catch {
            preview = nil
            selected = []
            self.error = error.localizedDescription
        }
    }

    private func run() async {
        working = true
        defer { working = false }
        error = nil
        do {
            if keepNames, preview?.needsLocal == true {
                try await model.enableLocal()
            }
            if keepNames, preview?.needsTest == true {
                await model.setDomain("test", enabled: true)
            }
            result = try await model.importSites(from: source, options(dryRun: false))
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// A site's migration steps in the import list, behind a disclosure so
/// the list stays readable.
private struct MigrationSteps: View {
    let database: String?
    let steps: [String]
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Button {
                expanded.toggle()
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "chevron.right")
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                    Text("Migrate database \(database ?? "") (\(steps.count) steps)")
                }
                .font(.caption)
                .foregroundStyle(.secondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if expanded {
                ForEach(steps, id: \.self) { s in
                    Label(s, systemImage: "arrow.right.circle").font(.caption).foregroundStyle(.secondary)
                        .padding(.leading, 12)
                }
            }
        }
    }
}
