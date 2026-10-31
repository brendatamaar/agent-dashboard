export const EVENT_LIMIT = 400;
export const PAGE_SIZE = 200;

export const eventTypes = [
  'message', 'tool_call', 'tool_result', 'approval_needed', 'usage',
  'done', 'error', 'subagent_started', 'subagent_finished',
] as const;

export interface NormalizedEvent {
  seq: number;
  agent_id?: string;
  type: typeof eventTypes[number];
  timestamp: string;
  payload: Record<string, unknown>;
}

export interface EventRecord {
  seq: number;
  type: string;
  payload_json: string;
  created_at: string;
}

export interface StreamState {
  agentID: string;
  events: NormalizedEvent[];
  cursor: number;
  status: 'connecting' | 'live' | 'reconnecting';
  truncated: boolean;
}

export type StreamInput =
  | { type: 'select'; agentID: string }
  | { type: 'status'; agentID: string; status: StreamState['status'] }
  | { type: 'receive'; agentID: string; events: NormalizedEvent[] };

export function initialStream(agentID: string): StreamState {
  return { agentID, events: [], cursor: 0, status: 'connecting', truncated: false };
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function parseEvent(value: unknown): NormalizedEvent | null {
  if (!isRecord(value) || !Number.isSafeInteger(value.seq) || Number(value.seq) < 1 ||
      !eventTypes.includes(value.type as NormalizedEvent['type']) ||
      typeof value.timestamp !== 'string' || !Number.isFinite(Date.parse(value.timestamp)) ||
      !isRecord(value.payload) || (value.agent_id !== undefined && typeof value.agent_id !== 'string')) return null;
  return value as unknown as NormalizedEvent;
}

export function eventFromRecord(record: EventRecord): NormalizedEvent | null {
  try {
    return parseEvent({ seq: record.seq, type: record.type, timestamp: record.created_at,
      payload: JSON.parse(record.payload_json) as unknown });
  } catch { return null; }
}

export function streamStep(state: StreamState, input: StreamInput): StreamState {
  if (input.type === 'select') return initialStream(input.agentID);
  if (input.agentID !== state.agentID) return state;
  if (input.type === 'status') return { ...state, status: input.status };
  const bySequence = new Map(state.events.map((event) => [event.seq, event]));
  for (const event of input.events) bySequence.set(event.seq, event);
  const merged = [...bySequence.values()].sort((a, b) => a.seq - b.seq);
  return { ...state, events: merged.slice(-EVENT_LIMIT),
    cursor: Math.max(state.cursor, ...input.events.map((event) => event.seq)),
    truncated: state.truncated || merged.length > EVENT_LIMIT };
}

export function agentFromURL(url: string): string | null {
  const parsed = new URL(url, 'http://localhost');
  const id = parsed.searchParams.get('agent_id') || /^\/agents\/([^/]+)\/?$/.exec(parsed.pathname)?.[1];
  return id && /^[A-Za-z0-9_-]{1,128}$/.test(id) ? id : null;
}

export function renderValue(value: unknown): string {
  if (value === undefined || value === null) return '';
  return typeof value === 'string' ? value : JSON.stringify(value);
}
