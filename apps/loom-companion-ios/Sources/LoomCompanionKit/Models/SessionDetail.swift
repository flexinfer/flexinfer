import Foundation

/// Breakdown of context entries by type.
public struct EntryTypeBucket: Decodable, Sendable, Identifiable {
    public let entryType: String
    public let count: Int
    public let chars: Int
    public let estimatedTokens: Int

    public var id: String { entryType }

    enum CodingKeys: String, CodingKey {
        case entryType = "entry_type"
        case count
        case chars
        case estimatedTokens = "estimated_tokens"
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.entryType = try container.decodeIfPresent(String.self, forKey: .entryType) ?? ""
        self.count = try container.decodeIfPresent(Int.self, forKey: .count) ?? 0
        self.chars = try container.decodeIfPresent(Int.self, forKey: .chars) ?? 0
        self.estimatedTokens = try container.decodeIfPresent(Int.self, forKey: .estimatedTokens) ?? 0
    }
}

/// A notable entry in a session (decision, error, or top-weight entry).
public struct SessionTopEntry: Decodable, Sendable, Identifiable {
    public let entryId: String
    public let entryType: String
    public let title: String
    public let timestamp: String
    public let chars: Int
    public let estimatedTokens: Int

    public var id: String { entryId }

    enum CodingKeys: String, CodingKey {
        case entryId = "id"
        case entryType = "entry_type"
        case title
        case timestamp
        case chars
        case estimatedTokens = "estimated_tokens"
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.entryId = try container.decodeIfPresent(String.self, forKey: .entryId) ?? UUID().uuidString
        self.entryType = try container.decodeIfPresent(String.self, forKey: .entryType) ?? ""
        self.title = try container.decodeIfPresent(String.self, forKey: .title) ?? ""
        self.timestamp = try container.decodeIfPresent(String.self, forKey: .timestamp) ?? ""
        self.chars = try container.decodeIfPresent(Int.self, forKey: .chars) ?? 0
        self.estimatedTokens = try container.decodeIfPresent(Int.self, forKey: .estimatedTokens) ?? 0
    }
}

/// A file frequently touched during a session.
public struct TouchedFile: Decodable, Sendable, Identifiable {
    public let filePath: String
    public let touchCount: Int

    public var id: String { filePath }

    enum CodingKeys: String, CodingKey {
        case filePath = "file_path"
        case touchCount = "touch_count"
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.filePath = try container.decodeIfPresent(String.self, forKey: .filePath) ?? ""
        self.touchCount = try container.decodeIfPresent(Int.self, forKey: .touchCount) ?? 0
    }
}

/// Task status summary for a session.
public struct SessionTaskSummary: Decodable, Sendable {
    public let total: Int
    public let pending: Int
    public let inProgress: Int
    public let completed: Int

    enum CodingKeys: String, CodingKey {
        case total
        case pending
        case inProgress = "in_progress"
        case completed
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.total = try container.decodeIfPresent(Int.self, forKey: .total) ?? 0
        self.pending = try container.decodeIfPresent(Int.self, forKey: .pending) ?? 0
        self.inProgress = try container.decodeIfPresent(Int.self, forKey: .inProgress) ?? 0
        self.completed = try container.decodeIfPresent(Int.self, forKey: .completed) ?? 0
    }
}
