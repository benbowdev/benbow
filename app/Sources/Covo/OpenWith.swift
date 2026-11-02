// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
import SwiftUI

/// Terminals and code editors installed on this Mac, to open a site's
/// folder with. Only apps that are actually installed are offered.
struct ToolApp: Identifiable, Hashable {
    let name: String
    let url: URL
    var id: URL { url }

    private static func installed(_ list: [(String, String)]) -> [ToolApp] {
        list.compactMap { name, bundleID in
            NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID).map { ToolApp(name: name, url: $0) }
        }
    }

    static var terminals: [ToolApp] {
        installed([
            ("Terminal", "com.apple.Terminal"),
            ("iTerm", "com.googlecode.iterm2"),
            ("Ghostty", "com.mitchellh.ghostty"),
            ("Warp", "dev.warp.Warp-Stable"),
        ])
    }

    static var editors: [ToolApp] {
        installed([
            ("Visual Studio Code", "com.microsoft.VSCode"),
            ("Cursor", "com.todesktop.230313mzl4w4u92"),
            ("PhpStorm", "com.jetbrains.PhpStorm"),
            ("Zed", "dev.zed.Zed"),
            ("Sublime Text", "com.sublimetext.4"),
            ("Nova", "com.panic.Nova"),
            ("BBEdit", "com.barebones.bbedit"),
        ])
    }

    func open(_ folder: String) {
        NSWorkspace.shared.open([URL(fileURLWithPath: folder, isDirectory: true)], withApplicationAt: url,
                                configuration: NSWorkspace.OpenConfiguration())
    }
}

/// "Open in Terminal" and "Open in <editor>" entries for a menu.
struct OpenWithItems: View {
    let folder: String

    var body: some View {
        ForEach(ToolApp.terminals) { app in
            Button("Open in \(app.name)") { app.open(folder) }
        }
        ForEach(ToolApp.editors) { app in
            Button("Open in \(app.name)") { app.open(folder) }
        }
    }
}
