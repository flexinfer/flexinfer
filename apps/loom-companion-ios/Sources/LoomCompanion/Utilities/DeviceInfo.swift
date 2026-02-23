import Foundation

/// Platform-specific device identifier for X-Device-ID header.
enum DeviceInfo {
    static var deviceId: String {
        #if os(iOS)
        return UIDevice.current.identifierForVendor?.uuidString ?? "unknown-ios"
        #elseif os(macOS)
        return Host.current().localizedName ?? "unknown-mac"
        #else
        return "unknown"
        #endif
    }
}
