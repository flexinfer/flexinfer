// Runnable fixture / smoke test for the spawn-terminal-status filter in
// buildUnifiedAgents. The HUD frontend doesn't ship a test runner, so
// this is a self-check: pnpm dlx tsx src/lib/utils/agents.terminalSpawn.fixture.ts
//
// Demonstrates four cases that the original buildUnifiedAgents got
// wrong by hardcoding `status: 'active'` on every spawn row:
//
//   1. spawn-only + completed status            → row is offline
//   2. spawn-only + running status              → row is active
//   3. presence-backed (active) + completed spawn → row downgraded to offline
//   4. presence-backed (active) + running spawn → row stays active

import { buildUnifiedAgents } from './agents';

function expect(label: string, actual: unknown, want: unknown): boolean {
  const ok = actual === want;
  if (ok) console.log(`PASS ${label}: got=${String(actual)}`);
  else console.error(`FAIL ${label}: got=${String(actual)} want=${String(want)}`);
  return ok;
}

let allOk = true;

// 1. spawn-only + completed → offline (the original bug — was always 'active')
const c1 = buildUnifiedAgents({
  sessions: [],
  agents: [],
  spawns: [{
    spawn_id: 'spawn-A',
    agent_id: 'spawn-claude-code-aaaaaaaaaaaa',
    status: 'completed',
    request: { project: 'loom-core', task_description: 'CI pipeline 9839 failed on fix/x' },
  }],
});
allOk = expect('spawn-only completed → offline', c1[0]?.status, 'offline') && allOk;

// 2. spawn-only + running → active
const c2 = buildUnifiedAgents({
  sessions: [],
  agents: [],
  spawns: [{
    spawn_id: 'spawn-B',
    agent_id: 'spawn-claude-code-bbbbbbbbbbbb',
    status: 'running',
    request: { project: 'loom-core' },
  }],
});
allOk = expect('spawn-only running → active', c2[0]?.status, 'active') && allOk;

// 3. presence-backed (active) + completed spawn → downgraded to offline
const c3 = buildUnifiedAgents({
  sessions: [],
  agents: [{
    agent_id: 'spawn-claude-code-cccccccccccc',
    status: 'active',
    has_presence: true,
    heartbeat_age_seconds: 30, // recent
  }],
  spawns: [{
    spawn_id: 'spawn-C',
    agent_id: 'spawn-claude-code-cccccccccccc',
    status: 'failed',
  }],
});
allOk = expect('presence+terminal-spawn → offline (downgrade)', c3[0]?.status, 'offline') && allOk;

// 4. presence-backed (active) + running spawn → stays active
const c4 = buildUnifiedAgents({
  sessions: [],
  agents: [{
    agent_id: 'spawn-claude-code-dddddddddddd',
    status: 'active',
    has_presence: true,
    heartbeat_age_seconds: 30,
  }],
  spawns: [{
    spawn_id: 'spawn-D',
    agent_id: 'spawn-claude-code-dddddddddddd',
    status: 'running',
  }],
});
allOk = expect('presence+running-spawn → active', c4[0]?.status, 'active') && allOk;

if (!allOk) {
  console.error('agents.terminalSpawn fixture: FAILURES detected');
  throw new Error('fixture failed');
}
console.log('agents.terminalSpawn fixture: all cases pass');
