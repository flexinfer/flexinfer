import SwiftUI

/// A lightweight inline sparkline drawn as a filled area path.
/// Works without the Charts framework — uses `Shape` for rendering.
struct CompactSparkline: View {
    let data: [Double]
    var lineColor: Color = LoomColors.info
    var fillOpacity: Double = 0.15

    var body: some View {
        GeometryReader { geo in
            if data.count > 1, let lo = data.min(), let hi = data.max() {
                let range = hi - lo
                let yNorm: (Double) -> CGFloat = { v in
                    range > 0
                        ? CGFloat(1 - (v - lo) / range) * geo.size.height
                        : geo.size.height * 0.5
                }
                let step = geo.size.width / CGFloat(data.count - 1)

                let points: [CGPoint] = data.enumerated().map { i, v in
                    CGPoint(x: step * CGFloat(i), y: yNorm(v))
                }

                // Filled area
                Path { p in
                    p.move(to: CGPoint(x: points[0].x, y: geo.size.height))
                    for pt in points { p.addLine(to: pt) }
                    p.addLine(to: CGPoint(x: points.last!.x, y: geo.size.height))
                    p.closeSubpath()
                }
                .fill(lineColor.opacity(fillOpacity))

                // Line
                Path { p in
                    p.move(to: points[0])
                    for pt in points.dropFirst() { p.addLine(to: pt) }
                }
                .stroke(lineColor, lineWidth: 1.5)
            }
        }
    }
}
