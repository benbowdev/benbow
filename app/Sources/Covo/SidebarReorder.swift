// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI
import UniformTypeIdentifiers

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

/// Where a dragged row lands relative to the row under the pointer.
enum DropZone { case before, after, into }

/// The row being dragged in the site list (drag and drop gives the item
/// itself only on drop, the zones need it while hovering).
@MainActor @Observable final class SidebarDrag {
    var item: SidebarItem?
}

/// Lets a site or group row be dragged and dropped on: on its upper or
/// lower edge to order (a line shows where), in the middle of a group to
/// move into it.
struct SidebarReorder: ViewModifier {
    @Environment(SidebarDrag.self) private var drag
    let target: SidebarItem
    let place: (SidebarItem, DropZone) -> Void
    @State private var height: CGFloat = 1
    @State private var zone: DropZone?

    func body(content: Content) -> some View {
        content
            .background(GeometryReader { geo in
                Color.clear
                    .onAppear { height = geo.size.height }
                    .onChange(of: geo.size.height) { _, h in height = h }
            })
            .background(zone == .into ? Color.accentColor.opacity(0.2) : .clear, in: RoundedRectangle(cornerRadius: 4))
            .overlay(alignment: zone == .after ? .bottom : .top) {
                if zone == .before || zone == .after {
                    Capsule().fill(Color.accentColor).frame(height: 2)
                        .offset(y: zone == .before ? -3 : 3)
                        .allowsHitTesting(false)
                }
            }
            .onDrag {
                drag.item = target
                return NSItemProvider(object: target.token as NSString)
            }
            .onDrop(of: [.plainText], delegate: RowDrop(target: target, height: height, drag: drag, zone: $zone, place: place))
    }
}

private struct RowDrop: DropDelegate {
    let target: SidebarItem
    let height: CGFloat
    let drag: SidebarDrag
    @Binding var zone: DropZone?
    let place: (SidebarItem, DropZone) -> Void

    private func zone(for info: DropInfo) -> DropZone? {
        guard let item = drag.item, item != target else { return nil }
        let y = info.location.y, h = max(height, 1)
        switch (target, item) {
        case (.group, .site):
            return .into
        case (.group, .group):
            return y < h * 0.25 ? .before : y > h * 0.75 ? .after : .into
        case (.site, .site):
            return y < h / 2 ? .before : .after
        case (.site, .group):
            // Groups come before sites: dropped on a site, a group goes
            // into that site's level, after its groups.
            return .into
        }
    }

    func validateDrop(info: DropInfo) -> Bool { zone(for: info) != nil }

    func dropEntered(info: DropInfo) { zone = zone(for: info) }

    func dropUpdated(info: DropInfo) -> DropProposal? {
        zone = zone(for: info)
        return DropProposal(operation: .move)
    }

    func dropExited(info: DropInfo) { zone = nil }

    func performDrop(info: DropInfo) -> Bool {
        let z = zone(for: info)
        zone = nil
        guard let z, let item = drag.item else { return false }
        drag.item = nil
        place(item, z)
        return true
    }
}

extension View {
    func sidebarReorder(_ target: SidebarItem, place: @escaping (SidebarItem, DropZone) -> Void) -> some View {
        modifier(SidebarReorder(target: target, place: place))
    }
}

extension AppModel {
    /// Puts item before, after or into target in the site list.
    func place(_ item: SidebarItem, _ zone: DropZone, relativeTo target: SidebarItem) async {
        func insert<T: Equatable>(_ x: T, _ list: [T], at anchor: T) -> [T] {
            var l = list.filter { $0 != x }
            let i = l.firstIndex(of: anchor).map { zone == .after ? $0 + 1 : $0 } ?? l.count
            l.insert(x, at: i)
            return l
        }
        switch (item, target, zone) {
        case (.site(let name), .group(let gid), _):
            if let site = sites.first(where: { $0.name == name }) { await moveSite(site, into: gid) }
        case (.group(let id), .group(let gid), .into):
            if let g = groups.first(where: { $0.id == id }) { await moveGroup(g, into: gid) }
        case (.group(let id), .group(let gid), _):
            guard let anchor = groups.first(where: { $0.id == gid }) else { return }
            let parent = anchor.parentId
            let siblings = groups.filter { $0.parentId == parent }.map(\.id)
            await setListOrder(parent: parent, groups: insert(id, siblings, at: gid), sites: [])
        case (.group(let id), .site(let name), _):
            guard let site = sites.first(where: { $0.name == name }) else { return }
            let parent = site.groupId
            let siblings = groups.filter { $0.parentId == parent && $0.id != id }.map(\.id)
            await setListOrder(parent: parent, groups: siblings + [id], sites: [])
        case (.site(let name), .site(let anchor), _):
            guard let a = sites.first(where: { $0.name == anchor }) else { return }
            let parent = a.groupId
            let siblings = sites.filter { $0.groupId == parent }.map(\.name)
            await setListOrder(parent: parent, groups: [], sites: insert(name, siblings, at: anchor))
        }
    }
}
