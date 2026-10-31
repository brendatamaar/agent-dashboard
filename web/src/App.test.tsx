import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import App from './App';
import * as api from './api';

vi.mock('./api', async (original) => ({
  ...await original<typeof api>(),
  checkSetupStatus: vi.fn(), getMe: vi.fn(), getAgents: vi.fn(), getAgent: vi.fn(), getRepos: vi.fn(),
  getSecrets: vi.fn(), getAuditLogs: vi.fn(), getSystemStats: vi.fn(), getApprovals: vi.fn(),
  getRepoSettings: vi.fn(), createAgent: vi.fn(), resumeAgent: vi.fn(), interruptAgent: vi.fn(),
  apiFetch: vi.fn(),
}));

class Socket {
  static instances: Socket[] = [];
  static OPEN = 1;
  static CLOSED = 3;
  readyState = 0;
  onopen?: () => void;
  onclose?: () => void;
  constructor() { Socket.instances.push(this); }
  close() { this.readyState = Socket.CLOSED; this.onclose?.(); }
}
const agent: api.Agent = {
  id: 'agent-123', repo_id: 'repo-1', harness: 'claude', model: '', runtime: 'docker', mode: 'task',
  prompt: 'Write tests', branch: 'agent/tests', worktree_path: '/workspace/a', tmux_session: 'ad-a',
  state: 'done', created_at: '2026-10-30T10:00:00Z', updated_at: '2026-10-30T10:00:00Z',
};
beforeEach(() => {
  vi.clearAllMocks(); Socket.instances = [];
  vi.stubGlobal('WebSocket', Socket);
  HTMLElement.prototype.scrollIntoView = vi.fn();
  window.history.replaceState(null, '', '/');
  vi.mocked(api.checkSetupStatus).mockResolvedValue(false);
  vi.mocked(api.getMe).mockResolvedValue({ id: 'owner', username: 'alice', has_totp: true, passkeys_count: 1 });
  vi.mocked(api.getAgents).mockResolvedValue([agent]);
  vi.mocked(api.getAgent).mockResolvedValue(agent);
  vi.mocked(api.getRepos).mockResolvedValue([{ id: 'repo-1', name: 'project', remote_url: 'https://github.com/owner/project', path: '/workspace/project', default_branch: 'main', created_at: agent.created_at, updated_at: agent.updated_at }]);
  vi.mocked(api.getSecrets).mockResolvedValue([]);
  vi.mocked(api.getAuditLogs).mockResolvedValue([]);
  vi.mocked(api.getSystemStats).mockRejectedValue(new Error('No telemetry fixture'));
  vi.mocked(api.getApprovals).mockResolvedValue([]);
  vi.mocked(api.getRepoSettings).mockResolvedValue({ runtime: 'docker', auto_approve: false });
  vi.mocked(api.resumeAgent).mockResolvedValue(undefined);
  vi.mocked(api.interruptAgent).mockResolvedValue(undefined);
  vi.mocked(api.apiFetch).mockResolvedValue({ status: 'ok' });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('opens an agent notification link and invokes explicit resume', async () => {
  window.history.replaceState(null, '', '/agents/agent-123');
  render(<App />);
  const resume = await screen.findByRole('button', { name: 'Resume' });
  fireEvent.click(resume);
  await waitFor(() => expect(api.resumeAgent).toHaveBeenCalledWith('agent-123'));
  expect(new URL(location.href).searchParams.get('agent_id')).toBe('agent-123');
});

it('uses Docker and the installed harness default model for a new task', async () => {
  vi.mocked(api.createAgent).mockResolvedValue({ ...agent, id: 'agent-new', state: 'queued' });
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: /Start Agent/ }));
  await waitFor(() => expect(api.getRepoSettings).toHaveBeenCalledWith('repo-1'));
  fireEvent.change(screen.getByPlaceholderText('Describe what you want the agent to do...'), { target: { value: 'Fix the parser' } });
  fireEvent.click(screen.getByRole('button', { name: 'Launch Agent in Isolated Worktree' }));
  await waitFor(() => expect(api.createAgent).toHaveBeenCalledWith(expect.objectContaining({ repo_id: 'repo-1', runtime: 'docker', model: '', prompt: 'Fix the parser' })));
});

it('closes the event connection and removes the deep link on logout', async () => {
  window.history.replaceState(null, '', '/?agent_id=agent-123');
  render(<App />);
  fireEvent.click(await screen.findByTitle('Sign Out'));
  await waitFor(() => expect(Socket.instances.every((socket) => socket.readyState === Socket.CLOSED)).toBe(true));
  expect(new URL(location.href).searchParams.has('agent_id')).toBe(false);
});

it('ignores an in-flight approval refresh after switching agents', async () => {
  let finishOldRefresh!: (approvals: api.ApprovalItem[]) => void;
  const other = { ...agent, id: 'agent-other' };
  vi.mocked(api.getAgents).mockResolvedValue([agent, other]);
  vi.mocked(api.getApprovals).mockResolvedValueOnce([])
    .mockImplementationOnce(() => new Promise((resolve) => { finishOldRefresh = resolve; }));
  window.history.replaceState(null, '', '/?agent_id=agent-123');
  render(<App />);
  await screen.findByRole('button', { name: 'Resume' });
  await waitFor(() => expect(finishOldRefresh).toBeDefined(), { timeout: 2500 });
  act(() => {
    window.history.pushState(null, '', '/?agent_id=agent-other');
    window.dispatchEvent(new PopStateEvent('popstate'));
  });
  await waitFor(() => expect(api.getApprovals).toHaveBeenCalledWith('agent-other'));
  await act(async () => finishOldRefresh([{
    id: 'old-request', agent_id: agent.id, tool_name: 'Bash', command: 'old-agent-command',
    decision: 'pending', created_at: agent.created_at,
  }]));
  expect(screen.queryByText(/Pending Approvals/)).toBeNull();
});
