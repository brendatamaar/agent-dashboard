import React, { useEffect, useRef, useState } from 'react';
import {
  Activity,
  Bot,
  Cpu,
  FolderGit2,
  GitBranch,
  HardDrive,
  Key,
  Lock,
  LogOut,
  Plus,
  RefreshCw,
  Send,
  Server,
  Shield,
  Square,
  Terminal as TerminalIcon,
  Trash2,
  X,
} from 'lucide-react';
import {
  Agent,
  AuditEntry,
  CreateAgentInput,
  NormalizedEvent,
  Repo,
  SecretMetadata,
  SystemStats,
  User,
  addRepo,
  apiFetch,
  checkSetupStatus,
  createAgent,
  createSecret,
  deleteAgent,
  deleteRepo,
  deleteSecret,
  fetchRepo,
  getAgent,
  getAgentEvents,
  getAgents,
  getAuditLogs,
  getMe,
  getRepos,
  getSecrets,
  getSystemStats,
  loginWithPasskey,
  pullRepo,
  registerPasskey,
  sendAgentMessage,
  stopAgent,
} from './api';
import { Terminal } from './components/Terminal';

export default function App() {
  const [loading, setLoading] = useState(true);
  const [setupNeeded, setSetupNeeded] = useState(false);
  const [user, setUser] = useState<User | null>(null);
  const [stats, setStats] = useState<SystemStats | null>(null);
  const [auditLogs, setAuditLogs] = useState<AuditEntry[]>([]);
  const [secrets, setSecrets] = useState<SecretMetadata[]>([]);
  const [repos, setRepos] = useState<Repo[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);

  // Navigation: 'agents' is the default phone-first screen
  const [activeTab, setActiveTab] = useState<'agents' | 'repos' | 'terminal' | 'system' | 'secrets' | 'audit'>('agents');
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null);
  const [showStartModal, setShowStartModal] = useState(false);
  const [showAddRepoModal, setShowAddRepoModal] = useState(false);
  const [showAgentTerminal, setShowAgentTerminal] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Setup form states
  const [setupUsername, setSetupUsername] = useState('admin');
  const [setupPassword, setSetupPassword] = useState('');
  const [totpSecretBanner, setTotpSecretBanner] = useState<string | null>(null);

  // Login form states
  const [loginUsername, setLoginUsername] = useState('admin');
  const [loginPassword, setLoginPassword] = useState('');
  const [loginTotp, setLoginTotp] = useState('');

  // Start Agent form states
  const [startRepoId, setStartRepoId] = useState('');
  const [startPrompt, setStartPrompt] = useState('');
  const [startMode, setStartMode] = useState<'task' | 'interactive'>('task');
  const [startModel, setStartModel] = useState('claude-sonnet-5-5');
  const [startBaseBranch, setStartBaseBranch] = useState('');
  const [startCustomBranch, setStartCustomBranch] = useState('');

  // Add Repo form states
  const [newRepoUrl, setNewRepoUrl] = useState('');
  const [newRepoName, setNewRepoName] = useState('');

  // Add Secret form states
  const [newSecKind, setNewSecKind] = useState('harness_auth');
  const [newSecName, setNewSecName] = useState('claude_token');
  const [newSecVal, setNewSecVal] = useState('');

  // Agent detail states
  const [agentEvents, setAgentEvents] = useState<NormalizedEvent[]>([]);
  const [followupMsg, setFollowupMsg] = useState('');
  const eventStreamRef = useRef<WebSocket | null>(null);
  const eventsEndRef = useRef<HTMLDivElement>(null);

  const init = async () => {
    try {
      setError(null);
      const needed = await checkSetupStatus();
      setSetupNeeded(needed);
      if (!needed) {
        const currentUser = await getMe();
        setUser(currentUser);
        if (currentUser) {
          await loadDashboardData();
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
      const [s, logs, sec, rList, aList] = await Promise.all([
        getSystemStats().catch(() => null),
        getAuditLogs().catch(() => []),
        getSecrets().catch(() => []),
        getRepos().catch(() => []),
        getAgents().catch(() => []),
      ]);
      if (s) setStats(s);
      setAuditLogs(logs);
      setSecrets(sec);
      setRepos(rList);
      setAgents(aList);
      if (rList.length > 0 && !startRepoId) {
        setStartRepoId(rList[0].id);
      }
    } catch (err: any) {
      console.warn('Dashboard data fetch error:', err);
    }
  };

  useEffect(() => {
    init();
  }, []);

  // Poll for agent updates every 5 seconds when logged in
  useEffect(() => {
    if (!user) return;
    const interval = setInterval(() => {
      getAgents().then(setAgents).catch(() => {});
      if (activeTab === 'repos') {
        getRepos().then(setRepos).catch(() => {});
      }
    }, 5000);
    return () => clearInterval(interval);
  }, [user, activeTab]);

  // Connect to WebSocket events when viewing agent detail
  useEffect(() => {
    if (!selectedAgentId) return;

    // Load initial historical events
    getAgentEvents(selectedAgentId, 0).then((rawList) => {
      const parsed = rawList.map((r) => {
        let payload = {};
        try {
          payload = JSON.parse(r.payload_json);
        } catch {}
        return {
          seq: r.seq,
          type: r.type as any,
          timestamp: new Date().toISOString(),
          payload,
        };
      });
      setAgentEvents(parsed);
    });

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/events/ws?agent_id=${encodeURIComponent(selectedAgentId)}`;
    const ws = new WebSocket(wsUrl);
    eventStreamRef.current = ws;

    ws.onmessage = (ev) => {
      try {
        const eventData: NormalizedEvent = JSON.parse(ev.data);
        setAgentEvents((prev) => [...prev, eventData]);
        getAgent(selectedAgentId).then((updated) => {
          setAgents((all) => all.map((a) => (a.id === updated.id ? updated : a)));
        });
      } catch {}
    };

    return () => {
      ws.close();
    };
  }, [selectedAgentId]);

  // Scroll live events to bottom
  useEffect(() => {
    eventsEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [agentEvents]);

  const handleSetup = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      const res = await apiFetch<{ totp_secret: string; totp_url: string }>('/api/auth/setup', {
        method: 'POST',
        body: JSON.stringify({ username: setupUsername, password: setupPassword }),
      });
      setTotpSecretBanner(res.totp_secret);
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

  const handleRegisterPasskey = async () => {
    setError(null);
    try {
      await registerPasskey();
      alert('Passkey successfully registered!');
      const me = await getMe();
      setUser(me);
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleLogout = async () => {
    try {
      await apiFetch('/api/auth/logout', { method: 'POST' });
    } finally {
      setUser(null);
      setTotpSecretBanner(null);
    }
  };

  const handleStartAgentSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      const input: CreateAgentInput = {
        repo_id: startRepoId,
        harness: 'claude',
        model: startModel,
        mode: startMode,
        prompt: startPrompt,
        base_branch: startBaseBranch,
        branch: startCustomBranch,
      };
      const newAgent = await createAgent(input);
      setAgents((prev) => [newAgent, ...prev]);
      setShowStartModal(false);
      setStartPrompt('');
      setStartCustomBranch('');
      setSelectedAgentId(newAgent.id);
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleAddRepoSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      const r = await addRepo(newRepoUrl, newRepoName);
      setRepos((prev) => [...prev, r]);
      setNewRepoUrl('');
      setNewRepoName('');
      setShowAddRepoModal(false);
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleCreateSecretSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      await createSecret(newSecKind, newSecName, newSecVal);
      setNewSecVal('');
      const sec = await getSecrets();
      setSecrets(sec);
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleSendMessage = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedAgentId || !followupMsg.trim()) return;
    try {
      await sendAgentMessage(selectedAgentId, followupMsg.trim());
      setFollowupMsg('');
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleStop = async (id: string) => {
    try {
      await stopAgent(id);
      const updated = await getAgent(id);
      setAgents((prev) => prev.map((a) => (a.id === id ? updated : a)));
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm('Are you sure you want to delete this agent and its worktree?')) return;
    try {
      await deleteAgent(id);
      setAgents((prev) => prev.filter((a) => a.id !== id));
      if (selectedAgentId === id) setSelectedAgentId(null);
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

  const renderStateBadge = (state: string) => {
    switch (state) {
      case 'running':
      case 'provisioning':
        return <span className="badge badge-success animate-pulse">Running</span>;
      case 'awaiting_approval':
        return <span className="badge badge-warning">Needs Action</span>;
      case 'idle':
        return <span className="badge badge-info">Interactive Idle</span>;
      case 'queued':
        return <span className="badge badge-warning">Queued</span>;
      case 'done':
        return <span className="badge badge-muted">Completed</span>;
      default:
        return <span className="badge badge-danger">{state}</span>;
    }
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
            <span className="badge badge-success">Phase 1</span>
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

  const runningCount = agents.filter((a) => a.state === 'running' || a.state === 'provisioning' || a.state === 'awaiting_approval' || a.state === 'idle').length;
  const selectedAgent = agents.find((a) => a.id === selectedAgentId);

  // 3. Authenticated Dashboard Screen
  return (
    <div className="container" style={{ paddingBottom: '5rem' }}>
      {/* Header */}
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1rem' }}>
        <div>
          <h1 style={{ fontSize: '1.25rem', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <Server style={{ color: 'var(--primary)' }} /> agent-dashboard
          </h1>
          <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
            Single-User VPS · Cap: {runningCount}/1 running (Max 2)
          </p>
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

      {totpSecretBanner && (
        <div className="card" style={{ border: '1px solid var(--primary)', backgroundColor: 'rgba(59, 130, 246, 0.08)' }}>
          <div className="card-header">
            <strong>Save your Authenticator (TOTP) Secret Key:</strong>
            <button className="btn btn-secondary" onClick={() => setTotpSecretBanner(null)}>Dismiss</button>
          </div>
          <code style={{ fontSize: '0.875rem', wordBreak: 'break-all' }}>{totpSecretBanner}</code>
        </div>
      )}

      {error && (
        <div className="badge badge-danger" style={{ marginBottom: '1rem', width: '100%', padding: '0.5rem' }}>
          {error}
        </div>
      )}

      {/* DETAIL VIEW: AGENT LIVE CARD OR TERMINAL */}
      {selectedAgent ? (
        <div>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '0.75rem' }}>
            <button
              className="btn btn-secondary"
              onClick={() => {
                setSelectedAgentId(null);
                setShowAgentTerminal(false);
              }}
            >
              ← Back to Agents
            </button>
            <div style={{ display: 'flex', gap: '0.5rem' }}>
              <button
                className={`btn ${showAgentTerminal ? 'btn-primary' : 'btn-secondary'}`}
                onClick={() => setShowAgentTerminal(!showAgentTerminal)}
              >
                <TerminalIcon style={{ width: 16, height: 16 }} /> {showAgentTerminal ? 'Live View' : 'Terminal'}
              </button>
              {selectedAgent.state === 'running' || selectedAgent.state === 'idle' || selectedAgent.state === 'awaiting_approval' ? (
                <button className="btn btn-secondary" onClick={() => handleStop(selectedAgent.id)}>
                  <Square style={{ width: 16, height: 16, color: 'var(--danger)' }} /> Stop
                </button>
              ) : null}
              <button className="btn btn-secondary" onClick={() => handleDelete(selectedAgent.id)}>
                <Trash2 style={{ width: 16, height: 16 }} />
              </button>
            </div>
          </div>

          <div className="card">
            <div className="card-header">
              <div>
                <h2 style={{ fontSize: '1.125rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                  <Bot style={{ color: 'var(--primary)' }} /> {selectedAgent.id}
                </h2>
                <div style={{ display: 'flex', gap: '0.75rem', fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '0.25rem' }}>
                  <span>Branch: <code>{selectedAgent.branch}</code></span>
                  <span>Mode: {selectedAgent.mode}</span>
                  <span>Harness: {selectedAgent.harness}</span>
                </div>
              </div>
              <div>{renderStateBadge(selectedAgent.state)}</div>
            </div>

            {selectedAgent.prompt && (
              <div style={{ padding: '0.625rem', backgroundColor: '#0f172a', borderRadius: '0.375rem', fontSize: '0.875rem', marginBottom: '0.75rem' }}>
                <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)', display: 'block', textTransform: 'uppercase' }}>Task Prompt</span>
                {selectedAgent.prompt}
              </div>
            )}

            {showAgentTerminal ? (
              <div style={{ height: '420px', marginTop: '0.5rem' }}>
                <Terminal agentId={selectedAgent.id} />
              </div>
            ) : (
              <div>
                <h3 style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase', marginBottom: '0.5rem' }}>
                  Live Event Stream
                </h3>
                <div style={{ maxHeight: '380px', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: '0.5rem', paddingRight: '0.25rem' }}>
                  {agentEvents.length === 0 ? (
                    <p style={{ fontSize: '0.875rem', color: 'var(--text-muted)', fontStyle: 'italic', padding: '1rem 0' }}>
                      Waiting for stream events...
                    </p>
                  ) : (
                    agentEvents.map((ev, i) => {
                      if (ev.type === 'message') {
                        return (
                          <div key={i} className={`event-bubble ${ev.payload.role === 'assistant' ? 'assistant' : 'user'}`}>
                            {ev.payload.text || JSON.stringify(ev.payload)}
                          </div>
                        );
                      } else if (ev.type === 'tool_call') {
                        return (
                          <div key={i} className="event-bubble tool">
                            <span style={{ color: '#38bdf8' }}>&gt; Tool: {ev.payload.name}</span>
                            <div style={{ color: 'var(--text-muted)', marginTop: '0.25rem' }}>
                              {JSON.stringify(ev.payload.input)}
                            </div>
                          </div>
                        );
                      } else if (ev.type === 'tool_result') {
                        return (
                          <div key={i} className="event-bubble tool" style={{ borderLeft: '3px solid #10b981' }}>
                            <span style={{ color: '#34d399' }}>✓ Result</span>
                            <div style={{ color: 'var(--text-muted)', marginTop: '0.25rem', maxHeight: '100px', overflowY: 'auto' }}>
                              {typeof ev.payload.content === 'string' ? ev.payload.content : JSON.stringify(ev.payload.content)}
                            </div>
                          </div>
                        );
                      } else if (ev.type === 'usage') {
                        return (
                          <div key={i} className="badge badge-muted" style={{ alignSelf: 'flex-start', margin: '0.25rem 0' }}>
                            Tokens: {ev.payload.input_tokens} in / {ev.payload.output_tokens} out · ${Number(ev.payload.cost_usd || 0).toFixed(4)}
                          </div>
                        );
                      } else if (ev.type === 'done') {
                        return (
                          <div key={i} className="event-bubble assistant" style={{ borderLeft: '3px solid #10b981' }}>
                            <strong style={{ color: '#34d399' }}>Completed:</strong> {ev.payload.result}
                          </div>
                        );
                      }
                      return null;
                    })
                  )}
                  <div ref={eventsEndRef} />
                </div>

                {/* Send Follow-up Input */}
                <form onSubmit={handleSendMessage} style={{ display: 'flex', gap: '0.5rem', marginTop: '0.75rem' }}>
                  <input
                    className="input-control"
                    type="text"
                    value={followupMsg}
                    onChange={(e) => setFollowupMsg(e.target.value)}
                    placeholder="Send a follow-up instruction to Claude..."
                  />
                  <button className="btn btn-primary" type="submit">
                    <Send style={{ width: 16, height: 16 }} />
                  </button>
                </form>
              </div>
            )}
          </div>
        </div>
      ) : null}

      {/* AGENTS LIST TAB (PHONE HOME VIEW) */}
      {!selectedAgent && activeTab === 'agents' && (
        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' }}>
            <h2 style={{ fontSize: '1rem', fontWeight: 600 }}>Active Agents</h2>
            <button className="btn btn-primary" onClick={() => setShowStartModal(true)}>
              <Plus style={{ width: 16, height: 16 }} /> Start Agent
            </button>
          </div>

          {agents.length === 0 ? (
            <div className="card" style={{ textAlign: 'center', padding: '2rem 1rem' }}>
              <Bot style={{ width: 40, height: 40, margin: '0 auto 0.75rem', color: 'var(--text-muted)' }} />
              <h3 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: '0.25rem' }}>No agents running</h3>
              <p style={{ fontSize: '0.8125rem', color: 'var(--text-muted)', marginBottom: '1rem' }}>
                Launch Claude Code on any of your cloned repositories.
              </p>
              <button className="btn btn-primary" onClick={() => setShowStartModal(true)}>
                Start Agent
              </button>
            </div>
          ) : (
            agents.map((ag) => (
              <div
                key={ag.id}
                className={`agent-card ${ag.state === 'awaiting_approval' ? 'needs-action' : ''}`}
                onClick={() => setSelectedAgentId(ag.id)}
              >
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '0.375rem' }}>
                  <div>
                    <span style={{ fontWeight: 600, fontSize: '0.9375rem' }}>{ag.id}</span>
                    <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '0.125rem' }}>
                      <code>{ag.branch}</code>
                    </div>
                  </div>
                  <div>{renderStateBadge(ag.state)}</div>
                </div>
                {ag.prompt && (
                  <p style={{ fontSize: '0.8125rem', color: 'var(--text-main)', marginTop: '0.375rem', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {ag.prompt}
                  </p>
                )}
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '0.5rem', fontSize: '0.6875rem', color: 'var(--text-muted)' }}>
                  <span>Harness: {ag.harness} ({ag.mode})</span>
                  <span>{new Date(ag.updated_at).toLocaleTimeString()}</span>
                </div>
              </div>
            ))
          )}
        </div>
      )}

      {/* REPOSITORIES TAB */}
      {!selectedAgent && activeTab === 'repos' && (
        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' }}>
            <h2 style={{ fontSize: '1rem', fontWeight: 600 }}>Repositories</h2>
            <button className="btn btn-primary" onClick={() => setShowAddRepoModal(true)}>
              <Plus style={{ width: 16, height: 16 }} /> Add Repo
            </button>
          </div>

          {repos.length === 0 ? (
            <div className="card" style={{ textAlign: 'center', padding: '2rem 1rem' }}>
              <FolderGit2 style={{ width: 40, height: 40, margin: '0 auto 0.75rem', color: 'var(--text-muted)' }} />
              <h3 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: '0.25rem' }}>No repositories added</h3>
              <p style={{ fontSize: '0.8125rem', color: 'var(--text-muted)', marginBottom: '1rem' }}>
                Clone a GitHub repository to run agents in isolated worktrees.
              </p>
              <button className="btn btn-primary" onClick={() => setShowAddRepoModal(true)}>
                Add Repository
              </button>
            </div>
          ) : (
            repos.map((r) => (
              <div key={r.id} className="card">
                <div className="card-header">
                  <div>
                    <h3 style={{ fontSize: '1rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                      <FolderGit2 style={{ color: 'var(--primary)', width: 18, height: 18 }} /> {r.name}
                    </h3>
                    <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>{r.remote_url}</p>
                  </div>
                  {r.is_dirty ? (
                    <span className="badge badge-warning">Dirty (Changes)</span>
                  ) : (
                    <span className="badge badge-muted">Clean</span>
                  )}
                </div>

                <div style={{ display: 'flex', alignItems: 'center', gap: '1rem', fontSize: '0.8125rem', marginBottom: '0.75rem' }}>
                  <span style={{ display: 'flex', alignItems: 'center', gap: '0.25rem' }}>
                    <GitBranch style={{ width: 14, height: 14 }} /> {r.current_branch || r.default_branch}
                  </span>
                  {(r.ahead !== undefined || r.behind !== undefined) && (
                    <span style={{ color: 'var(--text-muted)' }}>
                      ↑ {r.ahead || 0} / ↓ {r.behind || 0}
                    </span>
                  )}
                </div>

                <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
                  <button
                    className="btn btn-primary"
                    style={{ fontSize: '0.75rem', padding: '0.375rem 0.625rem' }}
                    onClick={() => {
                      setStartRepoId(r.id);
                      setStartBaseBranch(r.default_branch);
                      setShowStartModal(true);
                    }}
                  >
                    Start Agent
                  </button>
                  <button
                    className="btn btn-secondary"
                    style={{ fontSize: '0.75rem', padding: '0.375rem 0.625rem' }}
                    onClick={() => fetchRepo(r.id)}
                  >
                    Fetch
                  </button>
                  <button
                    className="btn btn-secondary"
                    style={{ fontSize: '0.75rem', padding: '0.375rem 0.625rem' }}
                    onClick={() => pullRepo(r.id)}
                  >
                    Pull
                  </button>
                  <button
                    className="btn btn-secondary"
                    style={{ fontSize: '0.75rem', padding: '0.375rem 0.625rem' }}
                    onClick={() => deleteRepo(r.id).then(() => setRepos((prev) => prev.filter((p) => p.id !== r.id)))}
                  >
                    <Trash2 style={{ width: 14, height: 14 }} />
                  </button>
                </div>
              </div>
            ))
          )}
        </div>
      )}

      {/* WEB TERMINAL TAB (HOST SHELL) */}
      {!selectedAgent && activeTab === 'terminal' && (
        <div>
          <h2 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: '0.75rem' }}>Host Web Terminal</h2>
          <div style={{ height: '520px' }}>
            <Terminal />
          </div>
        </div>
      )}

      {/* SYSTEM TELEMETRY TAB */}
      {!selectedAgent && activeTab === 'system' && stats && (
        <div>
          <h2 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: '0.75rem' }}>System Performance</h2>
          <div className="grid-2">
            <div className="card">
              <div className="card-header">
                <span className="card-title"><Cpu /> CPU Load</span>
                <span className="badge badge-muted">{stats.cpu_percent.toFixed(1)}%</span>
              </div>
              <div className="progress-track">
                <div className="progress-bar" style={{ width: `${stats.cpu_percent}%` }} />
              </div>
            </div>

            <div className="card">
              <div className="card-header">
                <span className="card-title"><Activity /> RAM Usage</span>
                <span className="badge badge-muted">{stats.memory_percent.toFixed(1)}%</span>
              </div>
              <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                {formatBytes(stats.memory_used_bytes)} / {formatBytes(stats.memory_total_bytes)}
              </p>
              <div className="progress-track">
                <div className="progress-bar" style={{ width: `${stats.memory_percent}%` }} />
              </div>
            </div>

            <div className="card">
              <div className="card-header">
                <span className="card-title"><HardDrive /> Disk Space</span>
                <span className="badge badge-muted">{stats.disk_percent.toFixed(1)}%</span>
              </div>
              <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                {formatBytes(stats.disk_used_bytes)} / {formatBytes(stats.disk_total_bytes)}
              </p>
              <div className="progress-track">
                <div className={`progress-bar ${stats.disk_warning ? 'warning' : ''}`} style={{ width: `${stats.disk_percent}%` }} />
              </div>
            </div>

            <div className="card">
              <div className="card-header">
                <span className="card-title"><Server /> Swap Memory</span>
                <span className="badge badge-muted">{stats.swap_percent.toFixed(1)}%</span>
              </div>
              <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                {formatBytes(stats.swap_used_bytes)} / {formatBytes(stats.swap_total_bytes)}
              </p>
              <div className="progress-track">
                <div className="progress-bar" style={{ width: `${stats.swap_percent}%` }} />
              </div>
            </div>
          </div>
        </div>
      )}

      {/* SECRETS TAB */}
      {!selectedAgent && activeTab === 'secrets' && (
        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' }}>
            <h2 style={{ fontSize: '1rem', fontWeight: 600 }}>Encrypted Credentials</h2>
            <button className="btn btn-secondary" onClick={handleRegisterPasskey}>
              <Key style={{ width: 14, height: 14 }} /> Add Passkey
            </button>
          </div>

          <div className="card">
            <h3 style={{ fontSize: '0.875rem', fontWeight: 600, marginBottom: '0.5rem' }}>Add Encrypted Secret</h3>
            <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginBottom: '0.75rem' }}>
              Add Anthropic API keys (<code>anthropic_api_key</code>), Claude tokens (<code>claude_token</code>), or GitHub tokens (<code>github_token</code>).
            </p>
            <form onSubmit={handleCreateSecretSubmit}>
              <div className="input-group">
                <label className="input-label">Kind</label>
                <input
                  className="input-control"
                  type="text"
                  value={newSecKind}
                  onChange={(e) => setNewSecKind(e.target.value)}
                  placeholder="harness_auth"
                  required
                />
              </div>
              <div className="input-group">
                <label className="input-label">Secret Name</label>
                <input
                  className="input-control"
                  type="text"
                  value={newSecName}
                  onChange={(e) => setNewSecName(e.target.value)}
                  placeholder="claude_token"
                  required
                />
              </div>
              <div className="input-group">
                <label className="input-label">Secret Value</label>
                <input
                  className="input-control"
                  type="password"
                  value={newSecVal}
                  onChange={(e) => setNewSecVal(e.target.value)}
                  placeholder="Paste token or key"
                  required
                />
              </div>
              <button className="btn btn-primary" type="submit">Encrypt & Save</button>
            </form>
          </div>

          {secrets.map((sec) => (
            <div key={sec.id} className="card" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <div>
                <strong>{sec.name}</strong>
                <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)', display: 'block' }}>Kind: {sec.kind}</span>
              </div>
              <button className="btn btn-secondary" onClick={() => deleteSecret(sec.name).then(() => setSecrets((prev) => prev.filter((p) => p.name !== sec.name)))}>
                <Trash2 style={{ width: 14, height: 14 }} />
              </button>
            </div>
          ))}
        </div>
      )}

      {/* AUDIT LOG TAB */}
      {!selectedAgent && activeTab === 'audit' && (
        <div>
          <h2 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: '0.75rem' }}>Audit Log</h2>
          {auditLogs.length === 0 ? (
            <p style={{ color: 'var(--text-muted)', fontSize: '0.875rem' }}>No audit events logged yet.</p>
          ) : (
            auditLogs.map((entry) => (
              <div key={entry.id} className="card" style={{ padding: '0.75rem' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.8125rem' }}>
                  <span><strong>{entry.action}</strong> on <code>{entry.target}</code></span>
                  <span className="badge badge-muted">{entry.result}</span>
                </div>
                <div style={{ fontSize: '0.6875rem', color: 'var(--text-muted)', marginTop: '0.25rem' }}>
                  By {entry.actor} at {new Date(entry.created_at).toLocaleString()}
                </div>
              </div>
            ))
          )}
        </div>
      )}

      {/* START AGENT MODAL */}
      {showStartModal && (
        <div style={{ position: 'fixed', inset: 0, backgroundColor: 'rgba(0,0,0,0.7)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '1rem' }}>
          <div className="card" style={{ width: '100%', maxWidth: 480, maxHeight: '90vh', overflowY: 'auto' }}>
            <div className="card-header">
              <h2 className="card-title"><Bot style={{ color: 'var(--primary)' }} /> Launch Coding Agent</h2>
              <button className="btn btn-secondary" onClick={() => setShowStartModal(false)}><X style={{ width: 16, height: 16 }} /></button>
            </div>

            <form onSubmit={handleStartAgentSubmit}>
              <div className="input-group">
                <label className="input-label">Repository</label>
                <select
                  className="input-control"
                  value={startRepoId}
                  onChange={(e) => setStartRepoId(e.target.value)}
                  required
                >
                  {repos.map((r) => (
                    <option key={r.id} value={r.id}>{r.name} ({r.default_branch})</option>
                  ))}
                </select>
              </div>

              <div className="input-group">
                <label className="input-label">Mode</label>
                <div style={{ display: 'flex', gap: '0.5rem' }}>
                  <button
                    type="button"
                    className={`btn btn-full ${startMode === 'task' ? 'btn-primary' : 'btn-secondary'}`}
                    onClick={() => setStartMode('task')}
                  >
                    Task (Unattended)
                  </button>
                  <button
                    type="button"
                    className={`btn btn-full ${startMode === 'interactive' ? 'btn-primary' : 'btn-secondary'}`}
                    onClick={() => setStartMode('interactive')}
                  >
                    Interactive (Chat)
                  </button>
                </div>
              </div>

              <div className="input-group">
                <label className="input-label">Model</label>
                <select
                  className="input-control"
                  value={startModel}
                  onChange={(e) => setStartModel(e.target.value)}
                >
                  <option value="claude-sonnet-5-5">Claude 3.5 Sonnet (Default)</option>
                  <option value="claude-opus-4">Claude 3 Opus</option>
                  <option value="claude-haiku-3-5">Claude 3.5 Haiku</option>
                </select>
              </div>

              <div className="input-group">
                <label className="input-label">Task Instruction / Prompt</label>
                <textarea
                  className="input-control"
                  rows={4}
                  value={startPrompt}
                  onChange={(e) => setStartPrompt(e.target.value)}
                  placeholder="Describe what you want Claude Code to do..."
                  required
                />
              </div>

              <button className="btn btn-primary btn-full" type="submit">
                Launch Agent in Isolated Worktree
              </button>
            </form>
          </div>
        </div>
      )}

      {/* ADD REPO MODAL */}
      {showAddRepoModal && (
        <div style={{ position: 'fixed', inset: 0, backgroundColor: 'rgba(0,0,0,0.7)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '1rem' }}>
          <div className="card" style={{ width: '100%', maxWidth: 440 }}>
            <div className="card-header">
              <h2 className="card-title"><FolderGit2 style={{ color: 'var(--primary)' }} /> Clone GitHub Repo</h2>
              <button className="btn btn-secondary" onClick={() => setShowAddRepoModal(false)}><X style={{ width: 16, height: 16 }} /></button>
            </div>

            <form onSubmit={handleAddRepoSubmit}>
              <div className="input-group">
                <label className="input-label">GitHub Repository URL</label>
                <input
                  className="input-control"
                  type="text"
                  value={newRepoUrl}
                  onChange={(e) => setNewRepoUrl(e.target.value)}
                  placeholder="https://github.com/owner/repository"
                  required
                />
              </div>
              <div className="input-group">
                <label className="input-label">Local Folder Name (optional)</label>
                <input
                  className="input-control"
                  type="text"
                  value={newRepoName}
                  onChange={(e) => setNewRepoName(e.target.value)}
                  placeholder="Defaults to repo name"
                />
              </div>
              <button className="btn btn-primary btn-full" type="submit">
                Clone to Workspace
              </button>
            </form>
          </div>
        </div>
      )}

      {/* PHONE-FIRST BOTTOM TAB NAVIGATION */}
      <nav className="bottom-nav">
        <button
          className={`nav-item ${activeTab === 'agents' ? 'active' : ''}`}
          onClick={() => {
            setSelectedAgentId(null);
            setActiveTab('agents');
          }}
        >
          <Bot style={{ width: 18, height: 18 }} />
          <span>Agents</span>
        </button>
        <button
          className={`nav-item ${activeTab === 'repos' ? 'active' : ''}`}
          onClick={() => {
            setSelectedAgentId(null);
            setActiveTab('repos');
          }}
        >
          <FolderGit2 style={{ width: 18, height: 18 }} />
          <span>Repos</span>
        </button>
        <button
          className={`nav-item ${activeTab === 'terminal' ? 'active' : ''}`}
          onClick={() => {
            setSelectedAgentId(null);
            setActiveTab('terminal');
          }}
        >
          <TerminalIcon style={{ width: 18, height: 18 }} />
          <span>Terminal</span>
        </button>
        <button
          className={`nav-item ${activeTab === 'system' ? 'active' : ''}`}
          onClick={() => {
            setSelectedAgentId(null);
            setActiveTab('system');
          }}
        >
          <Activity style={{ width: 18, height: 18 }} />
          <span>System</span>
        </button>
        <button
          className={`nav-item ${activeTab === 'secrets' ? 'active' : ''}`}
          onClick={() => {
            setSelectedAgentId(null);
            setActiveTab('secrets');
          }}
        >
          <Key style={{ width: 18, height: 18 }} />
          <span>Secrets</span>
        </button>
      </nav>
    </div>
  );
}
