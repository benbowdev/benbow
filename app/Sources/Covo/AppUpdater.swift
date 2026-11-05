// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import Observation
import Sparkle
import SwiftUI

/// Updates of the app itself, through Sparkle (docs/updates.md). Sparkle
/// checks once a day; a found update is shown as a discreet toolbar badge
/// (gentle reminders), never as a window on its own. Sparkle's window opens
/// only when the user clicks the badge or Check for Updates….
/// Dev builds have no SUFeedURL and never check.
@MainActor @Observable
final class AppUpdater: NSObject {
    /// Version of the update found by a scheduled check, until it is handled.
    private(set) var available: String?
    private(set) var enabled = false

    @ObservationIgnored private var controller: SPUStandardUpdaterController?

    override init() {
        super.init()
        guard Bundle.main.object(forInfoDictionaryKey: "SUFeedURL") != nil else { return }
        controller = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: nil, userDriverDelegate: self)
        enabled = true
        automaticChecks = controller?.updater.automaticallyChecksForUpdates ?? false
    }

    var automaticChecks = false {
        didSet { controller?.updater.automaticallyChecksForUpdates = automaticChecks }
    }

    func check() {
        controller?.checkForUpdates(nil)
    }
}

extension AppUpdater: @preconcurrency SPUStandardUserDriverDelegate {
    var supportsGentleScheduledUpdateReminders: Bool { true }

    func standardUserDriverShouldHandleShowingScheduledUpdate(_ update: SUAppcastItem, andInImmediateFocus immediateFocus: Bool) -> Bool {
        false
    }

    func standardUserDriverWillHandleShowingUpdate(_ handleShowingUpdate: Bool, forUpdate update: SUAppcastItem, state: SPUUserUpdateState) {
        if !handleShowingUpdate {
            available = update.displayVersionString
        }
    }

    func standardUserDriverDidReceiveUserAttention(forUpdate update: SUAppcastItem) {
        available = nil
    }

    func standardUserDriverWillFinishUpdateSession() {
        available = nil
    }
}

/// Toolbar badge, there only when a newer Covo is available.
struct AppUpdateBadge: View {
    @Environment(AppUpdater.self) private var updater

    var body: some View {
        if let version = updater.available {
            Button {
                updater.check()
            } label: {
                Label("Covo \(version)", systemImage: "sparkles")
                    .labelStyle(.titleAndIcon)
            }
            .help("A new version of Covo is available")
        }
    }
}

/// Settings → General → Updates: the app's own updates, above the components.
struct AppUpdateRow: View {
    @Environment(AppUpdater.self) private var updater

    var body: some View {
        if updater.enabled {
            @Bindable var updater = updater
            HStack {
                Toggle(isOn: $updater.automaticChecks) {
                    Text("Check for new versions of Covo daily")
                    Text("Reads getcovo.dev/appcast.xml; only Covo's version is sent. Nothing installs without your click.")
                }
                Spacer()
                Button("Check Now") { updater.check() }
            }
        } else {
            Text("This build of Covo does not check for app updates.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }
}
