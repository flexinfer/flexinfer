import Foundation

/// Load a JSON fixture file from the test bundle.
func loadFixture(_ name: String) throws -> Data {
    guard let url = Bundle.module.url(forResource: name, withExtension: "json", subdirectory: "Fixtures") else {
        throw FixtureError.notFound(name)
    }
    return try Data(contentsOf: url)
}

enum FixtureError: Error {
    case notFound(String)
}
