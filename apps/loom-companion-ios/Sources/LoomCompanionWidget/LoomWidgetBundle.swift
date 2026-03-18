import SwiftUI
import WidgetKit

@main
struct LoomWidgetBundle: WidgetBundle {
    var body: some Widget {
        FleetHealthWidget()
        TasksWidget()
        ActiveSessionsWidget()
        SessionSummaryWidget()
        LockScreenWidgets()
        WorkflowLiveActivityView()
        SessionLiveActivityView()
        PipelineLiveActivityView()
    }
}
