// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import SwiftUI

/// Benbow's on/off switches are green, not the accent color.
struct BenbowSwitchStyle: ToggleStyle {
    func makeBody(configuration: Configuration) -> some View {
        Toggle(configuration).toggleStyle(.switch).tint(.green)
    }
}

extension ToggleStyle where Self == BenbowSwitchStyle {
    static var benbowSwitch: BenbowSwitchStyle { BenbowSwitchStyle() }
}
