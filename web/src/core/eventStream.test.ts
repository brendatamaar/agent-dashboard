// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { agentFromURL, EVENT_LIMIT, eventFromRecord, initialStream, parseEvent, streamStep, type NormalizedEvent } from './eventStream';

const event = (seq: number): NormalizedEvent => ({ seq, type: 'message', timestamp: '2026-10-30T10:00:00Z', payload: { text: `message ${seq}` } });

describe('event stream invariants', () => {
  it('merges overlapping history and live pages in sequence without duplicates', () => {
    let state = initialStream('a');
    for (const page of [[3, 2], [1, 2, 3], [5, 4], [3]]) {
      state = streamStep(state, { type: 'receive', agentID: 'a', events: page.map(event) });
    }
    expect(state.events.map((item) => item.seq)).toEqual([1, 2, 3, 4, 5]);
    expect(state.cursor).toBe(5);
  });

  it('bounds retained events while preserving a monotonic replay cursor', () => {
    let state = initialStream('a');
    for (let seq = 1; seq <= 1000; seq++) {
      state = streamStep(state, { type: 'receive', agentID: 'a', events: [event(seq), event(Math.max(1, seq - 10))] });
      expect(state.events.length).toBeLessThanOrEqual(EVENT_LIMIT);
      expect(state.cursor).toBe(seq);
    }
    expect(state.events[0].seq).toBe(601);
    expect(state.truncated).toBe(true);
  });

  it('rejects late pages from a previously selected agent without changing state', () => {
    const state = initialStream('b');
    expect(streamStep(state, { type: 'receive', agentID: 'a', events: [event(1)] })).toBe(state);
    expect(streamStep(state, { type: 'status', agentID: 'a', status: 'live' })).toBe(state);
  });

  it('does not mutate its input state or events', () => {
    const input = Object.freeze(event(1));
    const state = initialStream('a');
    Object.freeze(state.events); Object.freeze(state);
    expect(streamStep(state, { type: 'receive', agentID: 'a', events: [input] }).events).toEqual([input]);
    expect(state.cursor).toBe(0);
  });
});

describe('event boundary', () => {
  it('round-trips valid wire events and preserves stored timestamps', () => {
    const value = event(12);
    expect(parseEvent(JSON.parse(JSON.stringify(value)))).toEqual(value);
    expect(eventFromRecord({ seq: value.seq, type: value.type, payload_json: JSON.stringify(value.payload), created_at: value.timestamp })).toEqual(value);
  });

  it.each([
    null, [], { ...event(1), seq: 0 }, { ...event(1), seq: -1 }, { ...event(1), seq: 1.2 },
    { ...event(1), seq: Number.MAX_SAFE_INTEGER + 1 }, { ...event(1), type: 'unknown' },
    { ...event(1), timestamp: 'not a date' }, { ...event(1), payload: [] },
    { ...event(1), payload: null }, { ...event(1), agent_id: 123 },
  ])('rejects malformed event %#', (value) => expect(parseEvent(value)).toBeNull());

  it('rejects corrupt stored JSON', () => {
    expect(eventFromRecord({ seq: 1, type: 'message', payload_json: '{', created_at: event(1).timestamp })).toBeNull();
  });

  it('accepts a notification deep link and rejects ambiguous/path-like agent IDs', () => {
    expect(agentFromURL('https://dashboard.test/?agent_id=agent-123')).toBe('agent-123');
    expect(agentFromURL('https://dashboard.test/agents/agent-123')).toBe('agent-123');
    expect(agentFromURL('https://dashboard.test/?agent_id=../private')).toBeNull();
    expect(agentFromURL('https://dashboard.test/')).toBeNull();
  });
});
