import SwiftUI
import LoomCompanionKit

struct SessionFilterView: View {
    @Binding var statusFilter: SessionStatus?
    @Binding var agentFilter: String?
    let availableAgents: [String]

    var body: some View {
        Section {
            Picker("Status", selection: $statusFilter) {
                Text("All").tag(Optional<SessionStatus>.none)
                Text("Active").tag(Optional<SessionStatus>.some(.active))
                Text("Ended").tag(Optional<SessionStatus>.some(.ended))
            }
            .pickerStyle(.segmented)

            if !availableAgents.isEmpty {
                Picker("Agent", selection: $agentFilter) {
                    Text("All Agents").tag(Optional<String>.none)
                    ForEach(availableAgents, id: \.self) { agent in
                        Text(agent).tag(Optional<String>.some(agent))
                    }
                }
            }
        }
    }
}
