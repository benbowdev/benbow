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

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label(title, systemImage: "info.circle").font(.callout.weight(.semibold))
            ForEach(Array(steps.enumerated()), id: \.offset) { i, step in
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text("\(i + 1).").monospacedDigit().foregroundStyle(.secondary)
                    Text(step).fixedSize(horizontal: false, vertical: true)
                }
                .font(.caption)
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
