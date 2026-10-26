import { startRegistration, startAuthentication } from '@simplewebauthn/browser';

export interface User {
  id: string;
  username: string;
  has_totp: boolean;
  passkeys_count: number;
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

export async function registerPasskey(): Promise<void> {
  const options = await apiFetch<any>('/api/auth/passkey/begin-registration', { method: 'POST' });
  const attResp = await startRegistration(options);
  await apiFetch('/api/auth/passkey/finish-registration', {
    method: 'POST',
    body: JSON.stringify(attResp),
  });
}

export async function loginWithPasskey(): Promise<void> {
  const options = await apiFetch<any>('/api/auth/passkey/begin-login', { method: 'POST' });
  const asseResp = await startAuthentication(options);
  await apiFetch('/api/auth/passkey/finish-login', {
    method: 'POST',
    body: JSON.stringify(asseResp),
  });
}
