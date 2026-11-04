// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import SwiftUI

/// Covo's on/off switches are green, not the accent color.
struct CovoSwitchStyle: ToggleStyle {
    func makeBody(configuration: Configuration) -> some View {
        Toggle(configuration).toggleStyle(.switch).tint(.green)
    }
}

extension ToggleStyle where Self == CovoSwitchStyle {
    static var covoSwitch: CovoSwitchStyle { CovoSwitchStyle() }
}
