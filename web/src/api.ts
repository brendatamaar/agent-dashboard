import { startRegistration, startAuthentication } from '@simplewebauthn/browser';

export interface User {
  id: string;
  username: string;
  has_totp: boolean;
  passkeys_count: number;
}

export interface AgentUsageStats {
  id: string;
  memory_bytes: number;
  memory_percent: number;
  cpu_percent: number;
}

export interface SystemStats {
  cpu_percent: number;
  memory_used_bytes: number;
  memory_total_bytes: number;
  memory_percent: number;
  disk_used_bytes: number;
  disk_total_bytes: number;
  disk_percent: number;
  swap_used_bytes: number;
  swap_total_bytes: number;
  swap_percent: number;
  disk_warning: boolean;
  agents?: AgentUsageStats[];
}

export interface AuditEntry {
  id: number;
  actor: string;
  action: string;
  target: string;
  result: string;
  created_at: string;
}

export interface SecretMetadata {
  id: string;
  kind: string;
  name: string;
  created_at: string;
  updated_at: string;
}

export interface Repo {
  id: string;
  name: string;
  remote_url: string;
  path: string;
  default_branch: string;
  current_branch?: string;
  is_dirty?: boolean;
  ahead?: number;
  behind?: number;
  created_at: string;
  updated_at: string;
}

export type AgentState =
  | 'queued'
  | 'provisioning'
  | 'running'
  | 'awaiting_approval'
  | 'idle'
  | 'stopped'
  | 'done'
  | 'failed';

export interface Agent {
  id: string;
  repo_id: string;
  harness: string;
  model: string;
  runtime: string;
  mode: 'task' | 'interactive';
  prompt?: string;
  branch: string;
  base_branch?: string;
  worktree_path: string;
  tmux_session: string;
  harness_session_id?: string;
  state: AgentState;
  failure_reason?: string;
  auto_approve?: boolean;
  created_at: string;
  updated_at: string;
}

export interface NormalizedEvent {
  seq: number;
  type: 'message' | 'tool_call' | 'tool_result' | 'approval_needed' | 'usage' | 'done' | 'error';
  timestamp: string;
  payload: Record<string, any>;
}

export async function apiFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers || {});
  headers.set('X-Requested-With', 'XMLHttpRequest');
  if (options.body && typeof options.body === 'string' && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }

  const res = await fetch(path, {
    ...options,
    headers,
    credentials: 'same-origin',
  });

  if (!res.ok) {
    let errorMsg = `Request failed: ${res.status} ${res.statusText}`;
    try {
      const data = await res.json();
      if (data.error) errorMsg = data.error;
    } catch {
      // ignore
    }
    throw new Error(errorMsg);
  }

  return res.json() as Promise<T>;
}

export async function checkSetupStatus(): Promise<boolean> {
  const res = await apiFetch<{ setup_needed: boolean }>('/api/auth/setup/status');
  return res.setup_needed;
}

export async function getMe(): Promise<User | null> {
  try {
    return await apiFetch<User>('/api/auth/me');
  } catch {
    return null;
  }
}

export async function getSystemStats(): Promise<SystemStats> {
  return await apiFetch<SystemStats>('/api/system');
}

export async function getAuditLogs(): Promise<AuditEntry[]> {
  return await apiFetch<AuditEntry[]>('/api/audit-log?limit=25');
}

export async function getSecrets(): Promise<SecretMetadata[]> {
  return await apiFetch<SecretMetadata[]>('/api/secrets');
}

export async function createSecret(kind: string, name: string, value: string): Promise<void> {
  await apiFetch('/api/secrets', {
    method: 'POST',
    body: JSON.stringify({ kind, name, value }),
  });
}

export async function deleteSecret(name: string): Promise<void> {
  await apiFetch(`/api/secrets/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  });
}

export async function registerPasskey(): Promise<void> {
  const data = await apiFetch<any>('/api/auth/passkey/begin-registration', { method: 'POST' });
  const options = data.publicKey || data;
  const attResp = await startRegistration(options);
  await apiFetch('/api/auth/passkey/finish-registration', {
    method: 'POST',
    body: JSON.stringify(attResp),
  });
}

export async function loginWithPasskey(): Promise<void> {
  const data = await apiFetch<any>('/api/auth/passkey/begin-login', { method: 'POST' });
  const options = data.publicKey || data;
  const asseResp = await startAuthentication(options);
  await apiFetch('/api/auth/passkey/finish-login', {
    method: 'POST',
    body: JSON.stringify(asseResp),
  });
}

// Repositories API
export async function getRepos(): Promise<Repo[]> {
  return await apiFetch<Repo[]>('/api/repos');
}

export async function addRepo(url: string, name?: string): Promise<Repo> {
  return await apiFetch<Repo>('/api/repos', {
    method: 'POST',
    body: JSON.stringify({ url, name }),
  });
}

export async function fetchRepo(id: string): Promise<void> {
  await apiFetch(`/api/repos/${id}/fetch`, { method: 'POST' });
}

export async function pullRepo(id: string): Promise<void> {
  await apiFetch(`/api/repos/${id}/pull`, { method: 'POST' });
}

export async function deleteRepo(id: string): Promise<void> {
  await apiFetch(`/api/repos/${id}`, { method: 'DELETE' });
}

export interface RepoSettings {
  runtime: 'docker' | 'host';
  auto_approve: boolean;
  image?: string;
  setup_command?: string;
}

export async function getRepoSettings(repoId: string): Promise<RepoSettings> {
  return await apiFetch<RepoSettings>(`/api/repos/${repoId}/settings`);
}

export async function updateRepoSettings(repoId: string, settings: RepoSettings): Promise<RepoSettings> {
  return await apiFetch<RepoSettings>(`/api/repos/${repoId}/settings`, {
    method: 'PUT',
    body: JSON.stringify(settings),
  });
}

// Agents API
export async function getAgents(): Promise<Agent[]> {
  return await apiFetch<Agent[]>('/api/agents');
}

export async function getAgent(id: string): Promise<Agent> {
  return await apiFetch<Agent>(`/api/agents/${id}`);
}

export interface CreateAgentInput {
  repo_id: string;
  harness: string;
  model: string;
  mode: 'task' | 'interactive';
  prompt: string;
  base_branch?: string;
  branch?: string;
}

export async function createAgent(input: CreateAgentInput): Promise<Agent> {
  return await apiFetch<Agent>('/api/agents', {
    method: 'POST',
    body: JSON.stringify(input),
  });
}

export async function sendAgentMessage(id: string, message: string): Promise<void> {
  await apiFetch(`/api/agents/${id}/message`, {
    method: 'POST',
    body: JSON.stringify({ message }),
  });
}

export async function stopAgent(id: string): Promise<void> {
  await apiFetch(`/api/agents/${id}/stop`, { method: 'POST' });
}

export async function cancelAgent(id: string): Promise<void> {
  await apiFetch(`/api/agents/${id}/cancel`, { method: 'POST' });
}

export async function deleteAgent(id: string): Promise<void> {
  await apiFetch(`/api/agents/${id}`, { method: 'DELETE' });
}

export async function getAgentEvents(id: string, afterSeq = 0): Promise<{ seq: number; type: string; payload_json: string }[]> {
  return await apiFetch(`/api/agents/${id}/events?after_seq=${afterSeq}`);
}


// Approvals API
export interface ApprovalItem {
  id: string;
  agent_id: string;
  tool_name: string;
  command?: string;
  file_path?: string;
  diff?: string;
  request_json?: string;
  decision: 'pending' | 'approved' | 'denied' | 'always_allowed';
  created_at: string;
}

export async function getApprovals(agentId: string): Promise<ApprovalItem[]> {
  const res = await apiFetch<{ approvals: ApprovalItem[] }>(`/api/agents/${agentId}/approvals`);
  return res.approvals || [];
}

export async function decideApproval(
  agentId: string,
  approvalId: string,
  decision: 'approve' | 'deny' | 'always_allow'
): Promise<void> {
  await apiFetch(`/api/agents/${agentId}/approvals/${approvalId}`, {
    method: 'POST',
    body: JSON.stringify({ decision }),
  });
}

// Web Push API
export async function getVAPIDKey(): Promise<string> {
  const res = await apiFetch<{ public_key: string }>('/api/push/vapid-key');
  return res.public_key;
}

export async function subscribePush(subscription: PushSubscription): Promise<void> {
  const subJSON = subscription.toJSON();
  await apiFetch('/api/push/subscribe', {
    method: 'POST',
    body: JSON.stringify({
      endpoint: subJSON.endpoint,
      keys: {
        p256dh: subJSON.keys?.p256dh,
        auth: subJSON.keys?.auth,
      },
    }),
  });
}

export async function unsubscribePush(endpoint: string): Promise<void> {
  await apiFetch('/api/push/unsubscribe', {
    method: 'POST',
    body: JSON.stringify({ endpoint }),
  });
}

export async function testPushNotification(): Promise<void> {
  await apiFetch('/api/push/test', { method: 'POST' });
}

// Git Operations API
export interface GitStatus {
  current_branch: string;
  is_dirty: boolean;
  ahead: number;
  behind: number;
}

export interface FileChange {
  path: string;
  old_path?: string;
  status: 'modified' | 'added' | 'deleted' | 'renamed' | 'untracked';
  staged: boolean;
  unstaged: boolean;
}

export async function getGitStatus(agentId: string): Promise<GitStatus> {
  return await apiFetch<GitStatus>(`/api/agents/${agentId}/git/status`);
}

export async function getGitChanges(agentId: string): Promise<FileChange[]> {
  const res = await apiFetch<{ changes: FileChange[] }>(`/api/agents/${agentId}/git/changes`);
  return res.changes || [];
}

export async function getGitDiff(agentId: string, file?: string, staged?: boolean): Promise<string> {
  const params = new URLSearchParams();
  if (file) params.set('file', file);
  if (staged) params.set('staged', 'true');
  const res = await apiFetch<{ diff: string }>(`/api/agents/${agentId}/git/diff?${params.toString()}`);
  return res.diff || '';
}

export async function stageGitFiles(agentId: string, paths: string[]): Promise<void> {
  await apiFetch(`/api/agents/${agentId}/git/stage`, {
    method: 'POST',
    body: JSON.stringify({ paths }),
  });
}

export async function unstageGitFiles(agentId: string, paths: string[]): Promise<void> {
  await apiFetch(`/api/agents/${agentId}/git/unstage`, {
    method: 'POST',
    body: JSON.stringify({ paths }),
  });
}

export async function stageGitHunk(agentId: string, patch: string): Promise<void> {
  await apiFetch(`/api/agents/${agentId}/git/stage-hunk`, {
    method: 'POST',
    body: JSON.stringify({ patch }),
  });
}

export async function commitGit(agentId: string, message: string, amend = false): Promise<void> {
  await apiFetch(`/api/agents/${agentId}/git/commit`, {
    method: 'POST',
    body: JSON.stringify({ message, amend }),
  });
}

export async function branchGit(agentId: string, action: 'create' | 'switch', branch: string): Promise<void> {
  await apiFetch(`/api/agents/${agentId}/git/branch`, {
    method: 'POST',
    body: JSON.stringify({ action, branch }),
  });
}

export async function pushGit(agentId: string, force = false, confirmMain = false): Promise<void> {
  await apiFetch(`/api/agents/${agentId}/git/push`, {
    method: 'POST',
    body: JSON.stringify({ force, confirm_main: confirmMain }),
  });
}

export async function pullRebaseGit(agentId: string): Promise<void> {
  await apiFetch(`/api/agents/${agentId}/git/pull-rebase`, { method: 'POST' });
}

export async function createPR(
  agentId: string,
  title: string,
  body: string
): Promise<{ pr: { id: number; number: number; title: string; html_url: string; state: string } }> {
  return await apiFetch(`/api/agents/${agentId}/git/pr`, {
    method: 'POST',
    body: JSON.stringify({ title, body }),
  });
}
