import SwiftUI
import LoomCompanionKit

struct AlertsListView: View {
    @Bindable var viewModel: AlertsViewModel
    var onNavigate: ((AlertAction, AlertItem) -> Void)?

    var body: some View {
        Group {
            if viewModel.alerts.isEmpty {
                ContentUnavailableView {
                    Label("No Alerts", systemImage: "bell.slash")
                } description: {
                    Text("Alerts from SSE events will appear here.")
                }
            } else {
                List {
                    ForEach(viewModel.alerts) { alert in
                        AlertRowView(alert: alert)
                            .contentShape(Rectangle())
                            .onTapGesture {
                                viewModel.markRead(alert.id)
                                let action = alert.primaryAction
                                if action != .acknowledge {
                                    onNavigate?(action, alert)
                                }
                            }
                    }
                }
                .listStyle(.plain)
            }
        }
        .navigationTitle("Alerts")
        .toolbar {
            ToolbarItemGroup(placement: .primaryAction) {
                if !viewModel.alerts.isEmpty {
                    Button {
                        viewModel.markAllRead()
                    } label: {
                        Label("Mark All Read", systemImage: "envelope.open")
                    }
                    .disabled(viewModel.unreadCount == 0)

                    Button(role: .destructive) {
                        viewModel.clearAll()
                    } label: {
                        Label("Clear All", systemImage: "trash")
                    }
                }
            }
        }
    }
}
