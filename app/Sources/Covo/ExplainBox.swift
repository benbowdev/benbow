// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import SwiftUI

/// A short "what happens" box for operations that change more than the
/// user may expect: numbered steps and, optionally, one thing to keep in
/// mind, shown before the user acts.
struct ExplainBox: View {
    let title: String
    let steps: [String]
    var caution: String?
    /// Collapsible boxes show the steps only when opened; the caution is
    /// always visible.
    var collapsible = false
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if collapsible {
                Button {
                    withAnimation(.snappy(duration: 0.2)) { expanded.toggle() }
                } label: {
                    HStack(spacing: 4) {
                        Label(title, systemImage: "info.circle").font(.callout.weight(.semibold))
                        Image(systemName: "chevron.right")
                            .font(.caption.weight(.semibold))
                            .rotationEffect(.degrees(expanded ? 90 : 0))
                            .foregroundStyle(.secondary)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(expanded ? "Hide the steps" : "Show the steps")
            } else {
                Label(title, systemImage: "info.circle").font(.callout.weight(.semibold))
            }
            if !collapsible || expanded {
                ForEach(Array(steps.enumerated()), id: \.offset) { i, step in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text("\(i + 1).").monospacedDigit().foregroundStyle(.secondary)
                        Text(step).fixedSize(horizontal: false, vertical: true)
                    }
                    .font(.caption)
                }
            }
            if let caution {
                Label(caution, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.blue.opacity(0.07), in: RoundedRectangle(cornerRadius: 8))
    }
}
