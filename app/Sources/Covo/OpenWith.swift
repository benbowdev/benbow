// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
import CovoKit
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

    /// The app's own icon, menu-sized.
    var icon: NSImage {
        let image = NSWorkspace.shared.icon(forFile: url.path)
        image.size = NSSize(width: 16, height: 16)
        return image
    }

    static var finder: ToolApp? {
        installed([("Finder", "com.apple.finder")]).first
    }

    /// The default web browser (Safari, Chrome, …).
    static var browser: ToolApp? {
        guard let probe = URL(string: "https://example.localhost"),
              let url = NSWorkspace.shared.urlForApplication(toOpen: probe) else { return nil }
        let name = FileManager.default.displayName(atPath: url.path).replacingOccurrences(of: ".app", with: "")
        return ToolApp(name: name, url: url)
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
        ForEach(ToolApp.terminals + ToolApp.editors) { app in
            Button {
                app.open(folder)
            } label: {
                Label { Text("Open in \(app.name)") } icon: { Image(nsImage: app.icon) }
            }
        }
    }
}

/// The site actions shared by the list's context menu and the detail.
struct SiteActions: View {
    @Environment(\.openWindow) private var openWindow
    let site: Site

    var body: some View {
        Button {
            open(site.url)
        } label: {
            if let b = ToolApp.browser {
                Label { Text("Open in \(b.name)") } icon: { Image(nsImage: b.icon) }
            } else {
                Label("Open in Browser", systemImage: "safari")
            }
        }
        Button {
            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: site.path)
        } label: {
            if let f = ToolApp.finder {
                Label { Text("Show in Finder") } icon: { Image(nsImage: f.icon) }
            } else {
                Label("Show in Finder", systemImage: "folder")
            }
        }
        Button {
            openWindow(id: "logs", value: site.name)
        } label: {
            Label("Logs", systemImage: "doc.text.magnifyingglass")
        }
    }
}
