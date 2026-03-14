import SwiftUI

/// A thin horizontal bar showing proportional segments by color.
struct ProportionBar: View {
    let segments: [(Double, Color)]

    private var total: Double {
        segments.reduce(0) { $0 + $1.0 }
    }

    var body: some View {
        GeometryReader { geo in
            if total > 0 {
                HStack(spacing: 1) {
                    ForEach(Array(segments.enumerated()), id: \.offset) { _, seg in
                        if seg.0 > 0 {
                            RoundedRectangle(cornerRadius: 2)
                                .fill(seg.1)
                                .frame(width: max(2, geo.size.width * seg.0 / total))
                        }
                    }
                }
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: 2))
    }
}
