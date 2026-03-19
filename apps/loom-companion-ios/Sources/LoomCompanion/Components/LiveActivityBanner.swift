import SwiftUI
import LoomCompanionKit

/// Small pulsing banner shown at the top of views when Live Activities are active.
/// Tap to navigate to the source (session/workflow/pipeline).
struct LiveActivityBanner: View {
    let activeCount: Int
    var onTap: (() -> Void)?

    var body: some View {
        if activeCount > 0 {
            Button(action: { onTap?() }) {
                HStack(spacing: 8) {
                    Circle()
                        .fill(.green)
                        .frame(width: 8, height: 8)
                        .pulse()

                    Text("\(activeCount) Live \(activeCount == 1 ? "Activity" : "Activities")")
                        .font(.caption)
                        .fontWeight(.medium)

                    Spacer()

                    Image(systemName: "chevron.right")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
                .background(.ultraThinMaterial)
                .clipShape(RoundedRectangle(cornerRadius: 10))
            }
            .buttonStyle(.plain)
            .transition(.slideInFromTop)
        }
    }
}
