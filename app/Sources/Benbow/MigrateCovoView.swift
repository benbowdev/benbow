// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
import BenbowKit
import SwiftUI

/// Benbow was called Covo until November 2026 (docs/migrate-from-covo.md).
/// On its first start Benbow looks for a Covo installation and offers to
/// move it over with `benbow migrate covo`, before its own core starts.
struct MigrationOffer: Equatable {
    let from: String
    let sites: [String]
    let steps: [MigrationReport.Step]

    /// A Covo installation Benbow can take over (nil: none, or Benbow
    /// already has sites, or the user said no before).
    static func check(binary: URL) async -> MigrationOffer? {
        if UserDefaults.standard.bool(forKey: declinedKey) { return nil }
        let env = ProcessInfo.processInfo.environment
        let home = env["BENBOW_MIGRATE_FROM"] ?? FileManager.default.homeDirectoryForCurrentUser
            .appending(path: "Library/Application Support/covo").path
        guard FileManager.default.fileExists(atPath: home + "/covo.db") else { return nil }
        guard let data = try? await runBenbow(binary, ["migrate", "covo", "--dry-run", "--json"]),
              let plan = try? EnvelopeDecoder.decode(MigrationReport.self, from: data) else { return nil }
        return MigrationOffer(from: plan.from, sites: plan.sites, steps: plan.steps)
    }

    static let declinedKey = "formerInstallDeclined"

    /// Covo.app, if it is still installed.
    static var oldApp: URL? { NSWorkspace.shared.urlForApplication(withBundleIdentifier: "dev.getcovo.app") }

    /// The app's own settings that carry over from Covo.
    @MainActor static func movePreferences() {
        guard let old = UserDefaults(suiteName: "dev.getcovo.app") else { return }
        for key in [AppModel.startOnLaunchKey] where old.object(forKey: key) != nil {
            UserDefaults.standard.set(old.object(forKey: key), forKey: key)
        }
    }
}

/// What `benbow migrate covo --json` reports.
struct MigrationReport: Decodable, Equatable {
    struct Step: Decodable, Equatable, Hashable {
        let step: String
        let status: String
        let detail: String?
    }
    let from: String
    let to: String
    let sites: [String]
    let steps: [Step]
    let warnings: [String]?
    let finished: Bool
}

/// Runs the benbow binary and returns its standard output.
func runBenbow(_ binary: URL, _ args: [String]) async throws -> Data {
    try await Task.detached {
        let p = Process()
        p.executableURL = binary
        p.arguments = args
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        try p.run()
        let data = out.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        return data
    }.value
}

/// The first-start screen that moves Covo into Benbow.
struct MigrationOfferView: View {
    @Environment(AppModel.self) private var model
    let offer: MigrationOffer
    @State private var working = false
    @State private var report: MigrationReport?
    @State private var error: String?
    @State private var trashed = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(report == nil ? "Covo is now Benbow" : report!.finished ? "Your sites moved to Benbow" : "Almost done")
                .font(.title.weight(.semibold))
            if let report {
                done(report)
            } else {
                proposal
            }
        }
        .padding(32)
        .frame(maxWidth: 620)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    @ViewBuilder private var proposal: some View {
        Text("Benbow found Covo on this Mac with \(offer.sites.count) \(offer.sites.count == 1 ? "site" : "sites"). It can move everything over: sites, databases, snapshots and passwords. The sites keep their addresses.")
            .fixedSize(horizontal: false, vertical: true)
        ExplainBox(
            title: "What happens",
            steps: [
                "Covo stops, and its folder becomes Benbow's: nothing is copied, so it takes a moment.",
                "Database and server passwords are copied to Benbow's keychain items.",
                "Benbow gets its own local certificate: macOS asks for your password to trust it.",
                "If .test was on, macOS asks for an administrator password once more.",
                "Benbow starts and checks that every site answers. Then Covo's keychain items and certificate are removed, and AI agents connected to Covo point at Benbow.",
            ],
            caution: "If moving the folder or the passwords fails, Covo is left exactly as it was."
        )
        if let error {
            Text(error).foregroundStyle(.red).font(.callout).fixedSize(horizontal: false, vertical: true)
        }
        HStack {
            Button("Not Now") {
                UserDefaults.standard.set(true, forKey: MigrationOffer.declinedKey)
                Task { await model.connect() }
            }
            .disabled(working)
            .help("Start Benbow empty. You can still run `benbow migrate covo` later, before adding sites.")
            Spacer()
            if working {
                ProgressView().controlSize(.small)
                Text("Moving…").foregroundStyle(.secondary)
            }
            Button("Move Covo into Benbow") { Task { await migrate() } }
                .keyboardShortcut(.defaultAction)
                .disabled(working)
        }
    }

    @ViewBuilder private func done(_ r: MigrationReport) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            ForEach(r.steps, id: \.self) { s in
                Label {
                    Text(s.step + (s.detail.map { $0.isEmpty ? "" : ": " + $0 } ?? ""))
                } icon: {
                    Image(systemName: s.status == "done" ? "checkmark.circle.fill" : s.status == "failed" ? "xmark.circle.fill" : "minus.circle")
                        .foregroundStyle(s.status == "done" ? .green : s.status == "failed" ? .red : .secondary)
                }
                .font(.callout)
            }
        }
        ForEach(r.warnings ?? [], id: \.self) { w in
            Text(w).font(.callout).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
        }
        if !r.finished {
            Text("Covo's keychain items and certificate are kept until your sites work: then run `benbow migrate covo --finish` in Terminal.")
                .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
        }
        HStack {
            if r.finished, let app = MigrationOffer.oldApp, !trashed {
                Button("Move Covo to the Trash") {
                    NSWorkspace.shared.recycle([app]) { _, err in
                        Task { @MainActor in trashed = err == nil }
                    }
                }
            }
            Spacer()
            Button("Continue") { Task { await model.connect() } }
                .keyboardShortcut(.defaultAction)
        }
    }

    private func migrate() async {
        guard let bin = Daemon.binary() else { return }
        working = true
        defer { working = false }
        error = nil
        do {
            let data = try await runBenbow(bin, ["migrate", "covo", "--json"])
            report = try EnvelopeDecoder.decode(MigrationReport.self, from: data)
            MigrationOffer.movePreferences()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
