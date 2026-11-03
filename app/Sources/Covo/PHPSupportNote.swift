// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CovoKit
import SwiftUI

/// Warns about a PHP version php.net no longer fixes (or soon won't).
struct PHPSupportNote: View {
    @Environment(AppModel.self) private var model
    let minor: String

    var body: some View {
        if let s = model.phpSupport(minor) {
            switch s.status {
            case "eol":
                Label {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("PHP \(minor) is too old").font(.callout.weight(.semibold))
                        Text("It has had no security fixes since \(Self.date(s.securityUntil)). Use it only to run old sites on this Mac, and plan an upgrade\(recommended.map { " to PHP \($0)" } ?? "").")
                            .font(.caption)
                    }
                } icon: {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                }
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.orange.opacity(0.12), in: RoundedRectangle(cornerRadius: 6))
            case "security":
                Label("PHP \(minor) gets security fixes only, until \(Self.date(s.securityUntil)).",
                      systemImage: "clock.badge.exclamationmark")
                    .font(.caption).foregroundStyle(.secondary)
            default:
                EmptyView()
            }
        }
    }

    /// The newest version still fully supported.
    private var recommended: String? {
        model.php?.minors.first { model.phpSupport($0)?.status == "active" }
    }

    static func date(_ s: String?) -> String {
        guard let s, let d = try? Date(s + "T00:00:00Z", strategy: .iso8601) else { return s ?? "?" }
        return d.formatted(.dateTime.day().month(.wide).year())
    }
}
