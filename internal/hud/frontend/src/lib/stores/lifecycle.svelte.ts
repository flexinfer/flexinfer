// Lifecycle store - computes swimlane data from fleet + timeline stores
import { fleetStore } from './fleet.svelte.ts';
import { timelineStore, type TimelineEntry } from './timeline.svelte.ts';

export interface SessionBar {
  session_id: string;
  start: number;
  end: number;
  status: string;
  namespace: string;
}

export interface EventMarker {
  timestamp: number;
  event_type: string;
  label: string;
}

export interface SwimLane {
  agent_id: string;
  agent_type: string;
  current_status: string;
  sessions: SessionBar[];
  events: EventMarker[];
}

export type ZoomPreset = '6h' | '12h' | '24h' | '48h';

const PRESET_MS: Record<ZoomPreset, number> = {
  '6h': 6 * 3600_000,
  '12h': 12 * 3600_000,
  '24h': 24 * 3600_000,
  '48h': 48 * 3600_000,
};

class LifecycleStore {
  zoomPreset = $state<ZoomPreset>('24h');

  get timeRange(): { start: number; end: number } {
    const end = Date.now();
    const start = end - PRESET_MS[this.zoomPreset];
    return { start, end };
  }

  get lanes(): SwimLane[] {
    const { start, end } = this.timeRange;
    const agents = fleetStore.agents;
    const sessions = fleetStore.sessions;
    const entries = timelineStore.entries;

    // Build agent lookup.
    const agentMap = new Map(agents.map((a) => [a.agent_id, a]));

    // Group sessions by agent_id.
    const sessionsByAgent = new Map<string, SessionBar[]>();
    for (const s of sessions) {
      const sStart = new Date(s.started_at).getTime();
      const sEnd = s.ended_at ? new Date(s.ended_at).getTime() : end;

      // Skip sessions entirely outside the time range.
      if (sEnd < start || sStart > end) continue;

      const bars = sessionsByAgent.get(s.agent_id) ?? [];
      bars.push({
        session_id: s.id,
        start: Math.max(sStart, start),
        end: Math.min(sEnd, end),
        status: s.status,
        namespace: s.namespace,
      });
      sessionsByAgent.set(s.agent_id, bars);
    }

    // Group timeline events by agent_id.
    const eventsByAgent = new Map<string, EventMarker[]>();
    for (const e of entries) {
      const ts = new Date(e.timestamp).getTime();
      if (ts < start || ts > end) continue;
      if (!e.agent_id) continue;

      const markers = eventsByAgent.get(e.agent_id) ?? [];
      markers.push({
        timestamp: ts,
        event_type: e.event_type,
        label: eventLabel(e),
      });
      eventsByAgent.set(e.agent_id, markers);
    }

    // Build lanes for all known agents (from sessions + presence).
    const agentIds = new Set([...sessionsByAgent.keys(), ...agents.map((a) => a.agent_id)]);
    const lanes: SwimLane[] = [];

    for (const agentId of agentIds) {
      const agentInfo = agentMap.get(agentId);
      const agentSessions = sessionsByAgent.get(agentId) ?? [];

      // Skip agents with no sessions and no events in the time window.
      if (agentSessions.length === 0 && !eventsByAgent.has(agentId)) continue;

      lanes.push({
        agent_id: agentId,
        agent_type: agentInfo?.agent_type ?? '',
        current_status: agentInfo?.status ?? 'offline',
        sessions: agentSessions.sort((a, b) => a.start - b.start),
        events: (eventsByAgent.get(agentId) ?? []).sort((a, b) => a.timestamp - b.timestamp),
      });
    }

    // Sort: active agents first, then alphabetical.
    lanes.sort((a, b) => {
      const aActive = a.current_status === 'active' ? 0 : a.current_status === 'idle' ? 1 : 2;
      const bActive = b.current_status === 'active' ? 0 : b.current_status === 'idle' ? 1 : 2;
      if (aActive !== bActive) return aActive - bActive;
      return a.agent_id.localeCompare(b.agent_id);
    });

    return lanes;
  }

  setZoom(preset: ZoomPreset): void {
    this.zoomPreset = preset;
  }
}

function eventLabel(e: TimelineEntry): string {
  switch (e.event_type) {
    case 'agent.session.start': return 'Session started';
    case 'agent.session.end': return 'Session ended';
    case 'agent.session.reaped': return 'Session reaped';
    case 'agent.task.update': return 'Task updated';
    case 'agent.task.dispatched': return 'Task dispatched';
    case 'hud.conflict': return 'File conflict';
    case 'agent.heartbeat': return 'Heartbeat';
    default: return e.event_type;
  }
}

export const lifecycleStore = new LifecycleStore();
