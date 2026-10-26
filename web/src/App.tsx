import React, { useEffect, useState } from 'react';
import {
  Activity,
  Cpu,
  HardDrive,
  Key,
  Lock,
  LogOut,
  RefreshCw,
  Server,
  Shield,
  ShieldAlert,
  Smartphone,
  User as UserIcon,
} from 'lucide-react';
import {
  AuditEntry,
  SecretMetadata,
  SystemStats,
  User,
  apiFetch,
  checkSetupStatus,
  createSecret,
  getAuditLogs,
  getMe,
  getSecrets,
  getSystemStats,
  loginWithPasskey,
  registerPasskey,
} from './api';

export default function App() {
  const [loading, setLoading] = useState(true);
  const [setupNeeded, setSetupNeeded] = useState(false);
  const [user, setUser] = useState<User | null>(null);
  const [stats, setStats] = useState<SystemStats | null>(null);
  const [auditLogs, setAuditLogs] = useState<AuditEntry[]>([]);
  const [secrets, setSecrets] = useState<SecretMetadata[]>([]);
  const [activeTab, setActiveTab] = useState<'system' | 'audit' | 'secrets'>('system');
  const [error, setError] = useState<string | null>(null);

  // Setup form states
  const [setupUsername, setSetupUsername] = useState('admin');
  const [setupPassword, setSetupPassword] = useState('');
  const [totpInfo, setTotpInfo] = useState<{ secret: string; url: string } | null>(null);

  // Login form states
  const [loginUsername, setLoginUsername] = useState('admin');
  const [loginPassword, setLoginPassword] = useState('');
  const [loginTotp, setLoginTotp] = useState('');

  // New Secret form states
  const [newSecKind, setNewSecKind] = useState('github');
  const [newSecName, setNewSecName] = useState('');
  const [newSecVal, setNewSecVal] = useState('');

  const init = async () => {
    try {
      setError(null);
      const needed = await checkSetupStatus();
      setSetupNeeded(needed);
      if (!needed) {
        const currentUser = await getMe();
        setUser(currentUser);
        if (currentUser) {
          loadDashboardData();
        }
      }
    } catch (err: any) {
      setError(err.message || 'Initialization failed');
    } finally {
      setLoading(false);
    }
  };

  const loadDashboardData = async () => {
    try {
      const [s, logs, sec] = await Promise.all([
        getSystemStats().catch(() => null),
        getAuditLogs().catch(() => []),
        getSecrets().catch(() => []),
      ]);
      if (s) setStats(s);
      setAuditLogs(logs);
      setSecrets(sec);
    } catch (err: any) {
      console.warn('Dashboard data fetch error:', err);
    }
  };

  useEffect(() => {
    init();
  }, []);

  const handleSetup = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      const res = await apiFetch<{ totp_secret: string; totp_url: string }>('/api/auth/setup', {
        method: 'POST',
        body: JSON.stringify({ username: setupUsername, password: setupPassword }),
      });
      setTotpInfo({ secret: res.totp_secret, url: res.totp_url });
      setSetupNeeded(false);
      const me = await getMe();
      setUser(me);
      if (me) loadDashboardData();
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      await apiFetch('/api/auth/login', {
        method: 'POST',
        body: JSON.stringify({
          username: loginUsername,
          password: loginPassword,
          totp_code: loginTotp,
        }),
      });
      const me = await getMe();
      setUser(me);
      if (me) loadDashboardData();
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handlePasskeyLogin = async () => {
    setError(null);
    try {
      await loginWithPasskey();
      const me = await getMe();
      setUser(me);
      if (me) loadDashboardData();
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleAddPasskey = async () => {
    setError(null);
    try {
      await registerPasskey();
      const me = await getMe();
      setUser(me);
      alert('Passkey successfully registered!');
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleLogout = async () => {
    try {
      await apiFetch('/api/auth/logout', { method: 'POST' });
    } finally {
      setUser(null);
      setTotpInfo(null);
    }
  };

  const handleCreateSecret = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      await createSecret(newSecKind, newSecName, newSecVal);
      setNewSecName('');
      setNewSecVal('');
      const sec = await getSecrets();
      setSecrets(sec);
      const logs = await getAuditLogs();
      setAuditLogs(logs);
    } catch (err: any) {
      setError(err.message);
    }
  };

  const formatBytes = (bytes: number) => {
    if (!bytes || bytes === 0) return '0 B';
    const gb = bytes / (1024 * 1024 * 1024);
    if (gb >= 1) return `${gb.toFixed(1)} GB`;
    const mb = bytes / (1024 * 1024);
    return `${mb.toFixed(0)} MB`;
  };

  if (loading) {
    return (
      <div className="container" style={{ textAlign: 'center', marginTop: '4rem' }}>
        <RefreshCw className="animate-spin" style={{ margin: '0 auto 1rem', width: 32, height: 32 }} />
        <p style={{ color: 'var(--text-muted)' }}>Connecting to agent-dashboard...</p>
      </div>
    );
  }

  // 1. Initial Setup Screen
  if (setupNeeded && !user) {
    return (
      <div className="container" style={{ maxWidth: 440, marginTop: '2rem' }}>
        <div className="card">
          <div className="card-header">
            <h1 className="card-title">
              <Shield style={{ color: 'var(--primary)' }} /> First-Run Setup
            </h1>
            <span className="badge badge-success">Phase 0</span>
          </div>

          <p style={{ fontSize: '0.875rem', color: 'var(--text-muted)', marginBottom: '1.25rem' }}>
            Set up the administrator account for your private dashboard. The server is strictly bound to Tailscale or loopback.
          </p>

          {error && <div className="badge badge-danger" style={{ marginBottom: '1rem', width: '100%' }}>{error}</div>}

          <form onSubmit={handleSetup}>
            <div className="input-group">
              <label className="input-label">Username</label>
              <input
                className="input-control"
                type="text"
                value={setupUsername}
                onChange={(e) => setSetupUsername(e.target.value)}
                required
              />
            </div>
            <div className="input-group">
              <label className="input-label">Password (min 8 characters)</label>
              <input
                className="input-control"
                type="password"
                value={setupPassword}
                onChange={(e) => setSetupPassword(e.target.value)}
                placeholder="Choose a strong password"
                minLength={8}
                required
              />
            </div>
            <button className="btn btn-primary btn-full" type="submit">
              Initialize Dashboard
            </button>
          </form>
        </div>
      </div>
    );
  }

  // 2. Login Screen
  if (!user) {
    return (
      <div className="container" style={{ maxWidth: 440, marginTop: '2rem' }}>
        <div className="card">
          <div className="card-header">
            <h1 className="card-title">
              <Lock style={{ color: 'var(--primary)' }} /> Sign In
            </h1>
            <span className="badge badge-success">Tailscale Enforced</span>
          </div>

          {error && <div className="badge badge-danger" style={{ marginBottom: '1rem', width: '100%' }}>{error}</div>}

          <div style={{ marginBottom: '1.5rem' }}>
            <button className="btn btn-secondary btn-full" onClick={handlePasskeyLogin} type="button">
              <Key style={{ width: 18, height: 18 }} /> Sign In with Passkey
            </button>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', margin: '1rem 0', color: 'var(--text-muted)' }}>
            <div style={{ flex: 1, height: 1, backgroundColor: 'var(--border-color)' }}></div>
            <span style={{ padding: '0 0.75rem', fontSize: '0.75rem', textTransform: 'uppercase' }}>or password</span>
            <div style={{ flex: 1, height: 1, backgroundColor: 'var(--border-color)' }}></div>
          </div>

          <form onSubmit={handleLogin}>
            <div className="input-group">
              <label className="input-label">Username</label>
              <input
                className="input-control"
                type="text"
                value={loginUsername}
                onChange={(e) => setLoginUsername(e.target.value)}
                required
              />
            </div>
            <div className="input-group">
              <label className="input-label">Password</label>
              <input
                className="input-control"
                type="password"
                value={loginPassword}
                onChange={(e) => setLoginPassword(e.target.value)}
                required
              />
            </div>
            <div className="input-group">
              <label className="input-label">TOTP 6-digit Code (if enabled)</label>
              <input
                className="input-control"
                type="text"
                value={loginTotp}
                onChange={(e) => setLoginTotp(e.target.value.trim())}
                placeholder="123456"
                maxLength={6}
              />
            </div>
            <button className="btn btn-primary btn-full" type="submit">
              Sign In
            </button>
          </form>
        </div>
      </div>
    );
  }

  // 3. Authenticated Dashboard Screen
  return (
    <div className="container">
      {/* Header */}
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.25rem' }}>
        <div>
          <h1 style={{ fontSize: '1.25rem', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <Server style={{ color: 'var(--primary)' }} /> agent-dashboard
          </h1>
          <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>Single-User Linux VPS Shell · Phase 0</p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <button className="btn btn-secondary" onClick={loadDashboardData} title="Refresh">
            <RefreshCw style={{ width: 16, height: 16 }} />
          </button>
          <button className="btn btn-secondary" onClick={handleLogout} title="Sign Out">
            <LogOut style={{ width: 16, height: 16 }} />
          </button>
        </div>
      </header>

      {/* TOTP Onboarding Banner if just created */}
      {totpInfo && (
        <div className="card" style={{ borderColor: 'var(--primary)', backgroundColor: 'rgba(59, 130, 246, 0.08)' }}>
          <div className="card-header">
            <h2 className="card-title">
              <Smartphone style={{ color: 'var(--primary)' }} /> Setup Authenticator (TOTP)
            </h2>
            <button className="btn btn-secondary" onClick={() => setTotpInfo(null)}>Dismiss</button>
          </div>
          <p style={{ fontSize: '0.875rem', marginBottom: '0.75rem' }}>
            Save this TOTP secret key in your authenticator app (1Password, Google Authenticator, Aegis):
          </p>
          <div style={{ padding: '0.5rem', background: '#0b0f19', borderRadius: '0.375rem', fontFamily: 'monospace', fontSize: '0.875rem', wordBreak: 'break-all' }}>
            {totpInfo.secret}
          </div>
        </div>
      )}

      {/* User Status Bar */}
      <div className="card" style={{ padding: '0.75rem 1rem', display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: '0.5rem' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <UserIcon style={{ width: 18, height: 18, color: 'var(--text-muted)' }} />
          <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>{user.username}</span>
          <span className="badge badge-success">Tailscale Online</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <span className="badge badge-muted">Passkeys: {user.passkeys_count}</span>
          <button className="btn btn-secondary" style={{ padding: '0.25rem 0.5rem', fontSize: '0.75rem' }} onClick={handleAddPasskey}>
            <Key style={{ width: 14, height: 14 }} /> Add Passkey
          </button>
        </div>
      </div>

      {/* Navigation Tabs */}
      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem' }}>
        <button
          className={`btn ${activeTab === 'system' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setActiveTab('system')}
        >
          <Activity style={{ width: 16, height: 16 }} /> System Panel
        </button>
        <button
          className={`btn ${activeTab === 'audit' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setActiveTab('audit')}
        >
          <Shield style={{ width: 16, height: 16 }} /> Audit Log
        </button>
        <button
          className={`btn ${activeTab === 'secrets' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setActiveTab('secrets')}
        >
          <Lock style={{ width: 16, height: 16 }} /> Secrets
        </button>
      </div>

      {error && <div className="badge badge-danger" style={{ marginBottom: '1rem', width: '100%' }}>{error}</div>}

      {/* Tab: System Panel */}
      {activeTab === 'system' && (
        <>
          <div className="grid-2">
            {/* CPU Metric */}
            <div className="card">
              <div className="card-header">
                <span className="card-title">
                  <Cpu style={{ color: 'var(--primary)', width: 18, height: 18 }} /> CPU Usage
                </span>
                <span style={{ fontSize: '1.125rem', fontWeight: 700 }}>{stats ? `${stats.cpu_percent.toFixed(1)}%` : '--'}</span>
              </div>
              <div className="progress-track">
                <div
                  className="progress-bar"
                  style={{ width: `${Math.min(100, stats ? stats.cpu_percent : 0)}%` }}
                />
              </div>
              <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '0.5rem' }}>VPS 2 vCPU Allocated</p>
            </div>

            {/* RAM Metric */}
            <div className="card">
              <div className="card-header">
                <span className="card-title">
                  <Activity style={{ color: '#10b981', width: 18, height: 18 }} /> Memory (RAM)
                </span>
                <span style={{ fontSize: '1.125rem', fontWeight: 700 }}>{stats ? `${stats.memory_percent.toFixed(1)}%` : '--'}</span>
              </div>
              <div className="progress-track">
                <div
                  className="progress-bar"
                  style={{ width: `${Math.min(100, stats ? stats.memory_percent : 0)}%`, backgroundColor: '#10b981' }}
                />
              </div>
              <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '0.5rem' }}>
                {stats ? `${formatBytes(stats.memory_used_bytes)} / ${formatBytes(stats.memory_total_bytes)}` : '--'}
              </p>
            </div>

            {/* Disk Metric */}
            <div className="card">
              <div className="card-header">
                <span className="card-title">
                  <HardDrive style={{ color: stats?.disk_warning ? 'var(--danger)' : 'var(--primary)', width: 18, height: 18 }} /> Disk Usage
                </span>
                <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                  {stats?.disk_warning && <span className="badge badge-danger"><ShieldAlert style={{ width: 12, height: 12 }} /> ≥80% Alert</span>}
                  <span style={{ fontSize: '1.125rem', fontWeight: 700 }}>{stats ? `${stats.disk_percent.toFixed(1)}%` : '--'}</span>
                </div>
              </div>
              <div className="progress-track">
                <div
                  className={`progress-bar ${stats?.disk_warning ? 'warning' : ''}`}
                  style={{ width: `${Math.min(100, stats ? stats.disk_percent : 0)}%` }}
                />
              </div>
              <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '0.5rem' }}>
                {stats ? `${formatBytes(stats.disk_used_bytes)} / ${formatBytes(stats.disk_total_bytes)}` : '--'}
              </p>
            </div>

            {/* Swap Metric */}
            <div className="card">
              <div className="card-header">
                <span className="card-title">
                  <Server style={{ color: 'var(--text-muted)', width: 18, height: 18 }} /> Swap Memory
                </span>
                <span style={{ fontSize: '1.125rem', fontWeight: 700 }}>{stats ? `${stats.swap_percent.toFixed(1)}%` : '--'}</span>
              </div>
              <div className="progress-track">
                <div
                  className="progress-bar"
                  style={{ width: `${Math.min(100, stats ? stats.swap_percent : 0)}%`, backgroundColor: '#64748b' }}
                />
              </div>
              <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '0.5rem' }}>
                {stats ? `${formatBytes(stats.swap_used_bytes)} / ${formatBytes(stats.swap_total_bytes)}` : '2-4 GB Recommended'}
              </p>
            </div>
          </div>

          {/* Phase 1 Agent Cards Placeholder */}
          <div className="card" style={{ textAlign: 'center', padding: '2rem 1rem' }}>
            <HardDrive style={{ width: 40, height: 40, margin: '0 auto 0.75rem', color: 'var(--border-color)' }} />
            <h2 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: '0.25rem' }}>No Active Coding Agents</h2>
            <p style={{ fontSize: '0.875rem', color: 'var(--text-muted)', maxWidth: 400, margin: '0 auto' }}>
              Phase 0 Foundation is ready. Repos, git worktrees, and Claude Code (tmux + Docker) will be enabled in Phase 1.
            </p>
          </div>
        </>
      )}

      {/* Tab: Audit Log */}
      {activeTab === 'audit' && (
        <div className="card">
          <div className="card-header">
            <h2 className="card-title">
              <Shield style={{ width: 18, height: 18 }} /> Security & Audit Trail
            </h2>
            <span className="badge badge-muted">{auditLogs.length} events</span>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
            {auditLogs.length === 0 ? (
              <p style={{ fontSize: '0.875rem', color: 'var(--text-muted)' }}>No audit logs recorded yet.</p>
            ) : (
              auditLogs.map((log) => (
                <div
                  key={log.id}
                  style={{
                    padding: '0.625rem',
                    background: '#0b0f19',
                    borderRadius: '0.375rem',
                    border: '1px solid var(--border-color)',
                    fontSize: '0.8125rem',
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    flexWrap: 'wrap',
                    gap: '0.5rem',
                  }}
                >
                  <div>
                    <span style={{ fontWeight: 600, color: 'var(--text-main)' }}>{log.action}</span>
                    <span style={{ color: 'var(--text-muted)', margin: '0 0.5rem' }}>·</span>
                    <span style={{ color: 'var(--text-muted)' }}>{log.target}</span>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                    <span className={`badge ${log.result === 'success' ? 'badge-success' : 'badge-danger'}`}>
                      {log.result}
                    </span>
                    <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                      {new Date(log.created_at).toLocaleTimeString()}
                    </span>
                  </div>
                </div>
              ))
            )}
          </div>
        </div>
      )}

      {/* Tab: Secrets */}
      {activeTab === 'secrets' && (
        <>
          <div className="card">
            <div className="card-header">
              <h2 className="card-title">
                <Lock style={{ width: 18, height: 18 }} /> Encrypted Secrets at Rest
              </h2>
              <span className="badge badge-success">AES-256-GCM + Argon2id</span>
            </div>
            <p style={{ fontSize: '0.8125rem', color: 'var(--text-muted)', marginBottom: '1rem' }}>
              Secrets are encrypted using your master key. Decrypted tokens are injected into containers at runtime and scrubbed from all logs.
            </p>

            <form onSubmit={handleCreateSecret} style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
              <div className="grid-2">
                <div>
                  <label className="input-label">Kind</label>
                  <select
                    className="input-control"
                    value={newSecKind}
                    onChange={(e) => setNewSecKind(e.target.value)}
                  >
                    <option value="github">GitHub Fine-Grained Token</option>
                    <option value="anthropic">Anthropic API Key</option>
                    <option value="openai">OpenAI API Key</option>
                    <option value="gemini">Google Gemini Key</option>
                    <option value="custom">Custom Secret</option>
                  </select>
                </div>
                <div>
                  <label className="input-label">Secret Identifier</label>
                  <input
                    className="input-control"
                    type="text"
                    value={newSecName}
                    onChange={(e) => setNewSecName(e.target.value)}
                    placeholder="e.g. GITHUB_TOKEN"
                    required
                  />
                </div>
              </div>
              <div>
                <label className="input-label">Plaintext Value (Encrypted immediately)</label>
                <input
                  className="input-control"
                  type="password"
                  value={newSecVal}
                  onChange={(e) => setNewSecVal(e.target.value)}
                  placeholder="Paste token or key"
                  required
                />
              </div>
              <button className="btn btn-primary" type="submit" style={{ alignSelf: 'flex-start' }}>
                Encrypt and Store Secret
              </button>
            </form>
          </div>

          <div className="card">
            <div className="card-header">
              <h3 className="card-title" style={{ fontSize: '0.875rem' }}>Stored Secrets Metadata</h3>
              <span className="badge badge-muted">{secrets.length} secrets</span>
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
              {secrets.length === 0 ? (
                <p style={{ fontSize: '0.875rem', color: 'var(--text-muted)' }}>No secrets stored yet.</p>
              ) : (
                secrets.map((sec) => (
                  <div
                    key={sec.id}
                    style={{
                      padding: '0.625rem',
                      background: '#0b0f19',
                      borderRadius: '0.375rem',
                      border: '1px solid var(--border-color)',
                      fontSize: '0.8125rem',
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                    }}
                  >
                    <div>
                      <span style={{ fontWeight: 600 }}>{sec.name}</span>
                      <span className="badge badge-muted" style={{ marginLeft: '0.5rem' }}>{sec.kind}</span>
                    </div>
                    <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                      Updated {new Date(sec.updated_at).toLocaleDateString()}
                    </span>
                  </div>
                ))
              )}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
