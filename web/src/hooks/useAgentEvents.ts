import { useEffect, useReducer } from 'react';
import { getAgentEvents } from '../api';
import { eventFromRecord, initialStream, PAGE_SIZE, parseEvent, streamStep, type NormalizedEvent } from '../core/eventStream';

// Subscribe before replay, then merge by durable sequence. This closes the gap
// between an HTTP snapshot and the first live event, including after reconnect.
export function useAgentEvents(agentID: string | null, enabled: boolean) {
  const [state, dispatch] = useReducer(streamStep, initialStream(agentID || ''));
  useEffect(() => {
    const id = agentID || '';
    dispatch({ type: 'select', agentID: id });
    if (!id || !enabled) return;
    const abort = new AbortController();
    let socket: WebSocket | undefined;
    let reconnect: ReturnType<typeof setTimeout> | undefined;
    let cursor = 0;
    let retry = 0;
    const receive = (events: NormalizedEvent[]) => {
      if (abort.signal.aborted) return;
      for (const event of events) cursor = Math.max(cursor, event.seq);
      dispatch({ type: 'receive', agentID: id, events });
    };
    const connect = () => {
      if (abort.signal.aborted) return;
      const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const ws = new WebSocket(`${protocol}//${location.host}/api/events/ws?agent_id=${encodeURIComponent(id)}`);
      socket = ws;
      let replaying = true;
      let pending: NormalizedEvent[] = [];
      ws.onmessage = (message) => {
        if (socket !== ws || abort.signal.aborted) return;
        try {
          const event = parseEvent(JSON.parse(String(message.data)) as unknown);
          if (!event || (event.agent_id && event.agent_id !== id)) return;
          if (replaying) {
            // A fast producer must not grow the reconnect buffer indefinitely.
            // Reconnect/replay from disk rather than discard an unseen event.
            if (pending.length >= 1000) { ws.close(); return; }
            pending.push(event);
          } else if (cursor > 0 && event.seq > cursor + 1) {
            pending.push(event);
            void replay();
          } else receive([event]);
        } catch { /* Malformed normalized frames do not replace raw terminal access. */ }
      };
      const replay = async () => {
        replaying = true;
        try {
          let after = cursor;
          for (;;) {
            const page = await getAgentEvents(id, after, PAGE_SIZE, abort.signal);
            if (abort.signal.aborted || socket !== ws || ws.readyState !== WebSocket.OPEN) return;
            receive(page.map(eventFromRecord).filter((event): event is NormalizedEvent => event !== null));
            const next = page.reduce((max, event) => Math.max(max, event.seq), after);
            if (page.length < PAGE_SIZE || next <= after) break;
            after = next;
          }
          receive(pending);
          pending = [];
          replaying = false;
          dispatch({ type: 'status', agentID: id, status: 'live' });
        } catch {
          if (!abort.signal.aborted && socket === ws) ws.close();
        }
      };
      ws.onopen = () => { retry = 0; void replay(); };
      ws.onerror = () => ws.close();
      ws.onclose = () => {
        if (abort.signal.aborted || socket !== ws) return;
        dispatch({ type: 'status', agentID: id, status: 'reconnecting' });
        reconnect = setTimeout(connect, Math.min(30_000, 1000 * 2 ** retry++));
      };
    };
    const reconnectOnReturn = () => {
      if (document.visibilityState === 'visible' && socket?.readyState === WebSocket.CLOSED) {
        clearTimeout(reconnect); connect();
      }
    };
    document.addEventListener('visibilitychange', reconnectOnReturn);
    connect();
    return () => {
      abort.abort(); clearTimeout(reconnect); socket?.close();
      document.removeEventListener('visibilitychange', reconnectOnReturn);
    };
  }, [agentID, enabled]);
  return state.agentID === (agentID || '') ? state : initialStream(agentID || '');
}
