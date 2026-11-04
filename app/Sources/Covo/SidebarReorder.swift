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

extension AppModel {
    /// Puts dragged groups at index among the groups of parent (nil: top
    /// level), moving them there if needed. Dragged sites go into parent.
    func insertGroups(_ items: [SidebarItem], into parent: Int64?, at index: Int) async {
        let moving = items.compactMap { if case .group(let id) = $0 { id } else { nil } }
        let sitesMoving = items.compactMap { if case .site(let n) = $0 { n } else { nil } }
        var order = groups.filter { $0.parentId == parent }.map(\.id)
        let before = order.prefix(index).filter { !moving.contains($0) }.count
        order.removeAll { moving.contains($0) }
        order.insert(contentsOf: moving, at: min(before, order.count))
        await setListOrder(parent: parent, groups: moving.isEmpty ? [] : order, sites: sitesMoving)
    }

    /// Puts dragged sites at index among the listed (not archived) sites of
    /// parent, moving them there if needed. Dragged groups go into parent.
    func insertSites(_ items: [SidebarItem], into parent: Int64?, at index: Int) async {
        let moving = items.compactMap { if case .site(let n) = $0 { n } else { nil } }
        let groupsMoving = items.compactMap { if case .group(let id) = $0 { id } else { nil } }
        var order = sites.filter { $0.groupId == parent && !$0.archived }.map(\.name)
        let before = order.prefix(index).filter { !moving.contains($0) }.count
        order.removeAll { moving.contains($0) }
        order.insert(contentsOf: moving, at: min(before, order.count))
        let siblingGroups = groups.filter { $0.parentId == parent && !groupsMoving.contains($0.id) }.map(\.id)
        await setListOrder(parent: parent, groups: groupsMoving.isEmpty ? [] : siblingGroups + groupsMoving,
                           sites: moving.isEmpty ? [] : order)
    }
}
