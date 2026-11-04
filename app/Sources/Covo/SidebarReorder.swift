// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI
import UniformTypeIdentifiers

// Ordering the site list by drag and drop uses the list's own mechanisms:
// rows are `.draggable` (a "covo-site:<name>" / "covo-group:<id>" string),
// each level's ForEach takes insertions between rows (`.onInsert`, macOS
// draws the line), and a group row takes drops into the group.

/// A row of the site list that can be dragged.
enum SidebarItem: Equatable {
    case site(String)
    case group(Int64)

    init?(token: String) {
        if let name = token.removingPrefix("covo-site:") {
            self = .site(name)
        } else if let raw = token.removingPrefix("covo-group:"), let id = Int64(raw) {
            self = .group(id)
        } else {
            return nil
        }
    }

    var token: String {
        switch self {
        case .site(let name): "covo-site:\(name)"
        case .group(let id): "covo-group:\(id)"
        }
    }
}

/// Reads the dragged rows from an insertion's item providers.
@MainActor
func sidebarItems(_ providers: [NSItemProvider], _ done: @escaping @MainActor ([SidebarItem]) -> Void) {
    Task {
        var items: [SidebarItem] = []
        for p in providers {
            let token: String? = await withCheckedContinuation { cont in
                _ = p.loadObject(ofClass: NSString.self) { obj, _ in
                    cont.resume(returning: (obj as? NSString).map { $0 as String })
                }
            }
            if let token, let item = SidebarItem(token: token) { items.append(item) }
        }
        done(items)
    }
}

/// One row of a level of the site list: a group or a site.
enum SidebarNode: Identifiable {
    case group(SiteGroup)
    case site(Site)

    var id: String { order }

    /// The core's name for it in a list order.
    var order: String {
        switch self {
        case .group(let g): "group:\(g.id)"
        case .site(let s): "site:\(s.name)"
        }
    }

    fileprivate var sortKey: (Int, Int, String) {
        switch self {
        case .group(let g): (g.position ?? 0, 0, g.name.lowercased())
        case .site(let s): (s.position ?? 0, 1, s.name.lowercased())
        }
    }
}

extension SidebarItem {
    /// The core's name for it in a list order.
    var order: String {
        switch self {
        case .site(let name): "site:\(name)"
        case .group(let id): "group:\(id)"
        }
    }
}

extension AppModel {
    /// A level of the list in the user's order (groups and sites mixed; a
    /// level never ordered shows groups, then sites, by name), from sites.
    func level(_ parent: Int64?, sites: [Site]) -> [SidebarNode] {
        let nodes = groups.filter { $0.parentId == parent }.map(SidebarNode.group)
            + sites.filter { $0.groupId == parent }.map(SidebarNode.site)
        return nodes.sorted { $0.sortKey < $1.sortKey }
    }

    /// Puts dragged rows at index of a level as shown (shown: that level's
    /// rows), moving them there if needed.
    func insert(_ items: [SidebarItem], into parent: Int64?, at index: Int, shown: [SidebarNode]) async {
        let moving = items.map(\.order)
        let current = shown.map(\.order)
        var order = current.filter { !moving.contains($0) }
        let before = current.prefix(index).filter { !moving.contains($0) }.count
        order.insert(contentsOf: moving, at: min(before, order.count))
        await setListOrder(parent: parent, items: order)
    }
}
