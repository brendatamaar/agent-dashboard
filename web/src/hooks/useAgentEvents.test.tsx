import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { getAgentEvents } from '../api';
import { useAgentEvents } from './useAgentEvents';
import type { EventRecord } from '../core/eventStream';

vi.mock('../api', () => ({ getAgentEvents: vi.fn() }));

class Socket {
  static OPEN = 1;
  static CLOSED = 3;
  static instances: Socket[] = [];
  readyState = 0;
  onopen?: () => void;
  onclose?: () => void;
  onerror?: () => void;
  onmessage?: (event: { data: string }) => void;
  constructor(public url: string) { Socket.instances.push(this); }
  open() { this.readyState = Socket.OPEN; this.onopen?.(); }
  close() { this.readyState = Socket.CLOSED; this.onclose?.(); }
  message(seq: number) {
    this.onmessage?.({ data: JSON.stringify({ seq, type: 'message', timestamp: '2026-10-30T10:00:00Z', payload: { text: String(seq) } }) });
  }
}
const record = (seq: number): EventRecord => ({ seq, type: 'message', payload_json: JSON.stringify({ text: String(seq) }), created_at: '2026-10-30T10:00:00Z' });

beforeEach(() => {
  Socket.instances = [];
  vi.stubGlobal('WebSocket', Socket);
  vi.mocked(getAgentEvents).mockReset();
  vi.mocked(getAgentEvents).mockResolvedValue([]);
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

describe('replay transport', () => {
  it('buffers frames while history loads and merges overlap without losing live output', async () => {
    let resolve!: (value: EventRecord[]) => void;
    vi.mocked(getAgentEvents).mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    const { result } = renderHook(() => useAgentEvents('a', true));
    const ws = Socket.instances[0];
    act(() => { ws.open(); ws.message(2); ws.message(3); });
    await act(async () => { resolve([record(1), record(2)]); });
    expect(result.current.events.map((event) => event.seq)).toEqual([1, 2, 3]);
    expect(result.current.status).toBe('live');
  });

  it('reconnects and replays only after the last durable sequence', async () => {
    vi.useFakeTimers();
    vi.mocked(getAgentEvents).mockResolvedValueOnce([record(1)]).mockResolvedValueOnce([record(2)]);
    const { result } = renderHook(() => useAgentEvents('a', true));
    await act(async () => Socket.instances[0].open());
    act(() => Socket.instances[0].close());
    expect(result.current.status).toBe('reconnecting');
    await act(async () => vi.advanceTimersByTime(1000));
    await act(async () => Socket.instances[1].open());
    expect(getAgentEvents).toHaveBeenLastCalledWith('a', 1, 200, expect.any(AbortSignal));
    expect(result.current.events.map((event) => event.seq)).toEqual([1, 2]);
  });

  it('fetches a missing sequence when the live transport drops a frame', async () => {
    vi.mocked(getAgentEvents).mockResolvedValueOnce([record(1)]).mockResolvedValueOnce([record(2), record(3)]);
    const { result } = renderHook(() => useAgentEvents('a', true));
    await act(async () => Socket.instances[0].open());
    await act(async () => Socket.instances[0].message(3));
    expect(result.current.events.map((event) => event.seq)).toEqual([1, 2, 3]);
  });

  it('cancels old history and sockets when switching agents', async () => {
    let oldPage!: (value: EventRecord[]) => void;
    vi.mocked(getAgentEvents).mockReturnValueOnce(new Promise((done) => { oldPage = done; })).mockResolvedValueOnce([record(8)]);
    const { result, rerender } = renderHook(({ id }) => useAgentEvents(id, true), { initialProps: { id: 'a' } });
    act(() => Socket.instances[0].open());
    rerender({ id: 'b' });
    await act(async () => { Socket.instances[1].open(); oldPage([record(1)]); });
    expect(Socket.instances[0].readyState).toBe(Socket.CLOSED);
    expect(result.current.agentID).toBe('b');
    expect(result.current.events.map((event) => event.seq)).toEqual([8]);
  });

  it('does not create an authenticated stream while logged out', () => {
    renderHook(() => useAgentEvents('a', false));
    expect(Socket.instances).toHaveLength(0);
    expect(getAgentEvents).not.toHaveBeenCalled();
  });
});
