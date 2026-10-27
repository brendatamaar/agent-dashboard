import React, { useEffect, useState } from 'react';
import { Clock, BookTemplate, Play, Trash2, Plus, X, RefreshCw } from 'lucide-react';
import {
  Template,
  Schedule,
  Repo,
  listTemplates,
  createTemplate,
  deleteTemplate,
  listSchedules,
  createSchedule,
  triggerSchedule,
  deleteSchedule,
} from '../api';

interface AutomationPanelProps {
  repos: Repo[];
  onLaunchTemplate?: (template: Template) => void;
  onNotify?: (msg: string) => void;
}

export const AutomationPanel: React.FC<AutomationPanelProps> = ({ repos, onLaunchTemplate, onNotify }) => {
  const [subTab, setSubTab] = useState<'templates' | 'schedules'>('templates');
  const [templates, setTemplates] = useState<Template[]>([]);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [loading, setLoading] = useState(false);

  // New Template Modal state
  const [showTmplModal, setShowTmplModal] = useState(false);
  const [newTmplName, setNewTmplName] = useState('');
  const [newTmplHarness, setNewTmplHarness] = useState('claude');
  const [newTmplPrompt, setNewTmplPrompt] = useState('');

  // New Schedule Modal state
  const [showSchModal, setShowSchModal] = useState(false);
  const [newSchTmplId, setNewSchTmplId] = useState('');
  const [newSchRepoId, setNewSchRepoId] = useState('');
  const [newSchCron, setNewSchCron] = useState('0 2 * * *');

  const fetchData = async () => {
    setLoading(true);
    try {
      const [tList, sList] = await Promise.all([listTemplates(), listSchedules()]);
      setTemplates(tList);
      setSchedules(sList);
      if (tList.length > 0 && !newSchTmplId) setNewSchTmplId(tList[0].id);
      if (repos.length > 0 && !newSchRepoId) setNewSchRepoId(repos[0].id);
    } catch (err: any) {
      if (onNotify) onNotify(`Failed to load automation data: ${err.message}`);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  const handleCreateTemplate = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const t = await createTemplate({
        name: newTmplName,
        harness: newTmplHarness,
        prompt: newTmplPrompt,
      });
      setTemplates((prev) => [...prev, t]);
      setShowTmplModal(false);
      setNewTmplName('');
      setNewTmplPrompt('');
      if (onNotify) onNotify(`Created template: ${t.name}`);
    } catch (err: any) {
      if (onNotify) onNotify(`Error creating template: ${err.message}`);
    }
  };

  const handleDeleteTemplate = async (id: string) => {
    if (!confirm('Are you sure you want to delete this template?')) return;
    try {
      await deleteTemplate(id);
      setTemplates((prev) => prev.filter((t) => t.id !== id));
      if (onNotify) onNotify('Template deleted');
    } catch (err: any) {
      if (onNotify) onNotify(`Failed to delete template: ${err.message}`);
    }
  };

  const handleCreateSchedule = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const s = await createSchedule({
        template_id: newSchTmplId,
        repo_id: newSchRepoId,
        cron: newSchCron,
        enabled: true,
      });
      setSchedules((prev) => [...prev, s]);
      setShowSchModal(false);
      if (onNotify) onNotify('Schedule created successfully');
    } catch (err: any) {
      if (onNotify) onNotify(`Error creating schedule: ${err.message}`);
    }
  };

  const handleTriggerSchedule = async (id: string) => {
    try {
      await triggerSchedule(id);
      if (onNotify) onNotify('Triggered scheduled run');
    } catch (err: any) {
      if (onNotify) onNotify(`Failed to trigger: ${err.message}`);
    }
  };

  const handleDeleteSchedule = async (id: string) => {
    if (!confirm('Are you sure you want to delete this schedule?')) return;
    try {
      await deleteSchedule(id);
      setSchedules((prev) => prev.filter((s) => s.id !== id));
      if (onNotify) onNotify('Schedule deleted');
    } catch (err: any) {
      if (onNotify) onNotify(`Failed to delete schedule: ${err.message}`);
    }
  };

  const getTemplateName = (id: string) => {
    const t = templates.find((x) => x.id === id);
    return t ? t.name : id;
  };

  const getRepoName = (id: string) => {
    const r = repos.find((x) => x.id === id);
    return r ? r.name : id;
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      {/* Sub-tab navigation */}
      <div style={{ display: 'flex', gap: '0.5rem', borderBottom: '1px solid #1e293b', paddingBottom: '0.5rem' }}>
        <button
          className={`btn ${subTab === 'templates' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setSubTab('templates')}
        >
          <BookTemplate style={{ width: 16, height: 16 }} /> Saved Templates ({templates.length})
        </button>
        <button
          className={`btn ${subTab === 'schedules' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setSubTab('schedules')}
        >
          <Clock style={{ width: 16, height: 16 }} /> Scheduled Tasks ({schedules.length})
        </button>
        <button className="btn btn-secondary" onClick={fetchData} style={{ marginLeft: 'auto' }}>
          <RefreshCw style={{ width: 14, height: 14 }} className={loading ? 'spin' : ''} />
        </button>
      </div>

      {/* TEMPLATES TAB */}
      {subTab === 'templates' && (
        <div className="card">
          <div className="card-header">
            <h2 className="card-title">Prompt Templates</h2>
            <button className="btn btn-primary" onClick={() => setShowTmplModal(true)}>
              <Plus style={{ width: 16, height: 16 }} /> New Template
            </button>
          </div>

          {templates.length === 0 ? (
            <p style={{ color: 'var(--text-muted)', fontStyle: 'italic', padding: '1rem 0' }}>
              No saved templates yet. Create one to easily launch recurring tasks.
            </p>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem', marginTop: '0.5rem' }}>
              {templates.map((t) => (
                <div
                  key={t.id}
                  style={{
                    padding: '0.875rem',
                    backgroundColor: '#0f172a',
                    borderRadius: '0.5rem',
                    border: '1px solid #1e293b',
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '0.5rem',
                  }}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <div style={{ fontWeight: 600, fontSize: '0.9375rem' }}>{t.name}</div>
                    <span className="badge badge-muted" style={{ textTransform: 'uppercase' }}>
                      {t.harness}
                    </span>
                  </div>
                  <div style={{ fontSize: '0.8125rem', color: '#94a3b8', maxHeight: '60px', overflowY: 'auto' }}>
                    {t.prompt}
                  </div>
                  <div style={{ display: 'flex', gap: '0.5rem', marginTop: '0.25rem' }}>
                    {onLaunchTemplate && (
                      <button
                        className="btn btn-primary"
                        style={{ fontSize: '0.75rem', padding: '0.375rem 0.625rem' }}
                        onClick={() => onLaunchTemplate(t)}
                      >
                        <Play style={{ width: 12, height: 12 }} /> Launch Agent
                      </button>
                    )}
                    <button
                      className="btn btn-secondary"
                      style={{ fontSize: '0.75rem', padding: '0.375rem 0.625rem', color: '#f87171' }}
                      onClick={() => handleDeleteTemplate(t.id)}
                    >
                      <Trash2 style={{ width: 12, height: 12 }} />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* SCHEDULES TAB */}
      {subTab === 'schedules' && (
        <div className="card">
          <div className="card-header">
            <h2 className="card-title">Scheduled Automations</h2>
            <button
              className="btn btn-primary"
              onClick={() => setShowSchModal(true)}
              disabled={templates.length === 0 || repos.length === 0}
            >
              <Plus style={{ width: 16, height: 16 }} /> New Schedule
            </button>
          </div>

          {schedules.length === 0 ? (
            <p style={{ color: 'var(--text-muted)', fontStyle: 'italic', padding: '1rem 0' }}>
              No scheduled tasks yet. Create a schedule to automatically run agents (e.g. nightly PR reviews).
            </p>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem', marginTop: '0.5rem' }}>
              {schedules.map((sc) => (
                <div
                  key={sc.id}
                  style={{
                    padding: '0.875rem',
                    backgroundColor: '#0f172a',
                    borderRadius: '0.5rem',
                    border: '1px solid #1e293b',
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '0.5rem',
                  }}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <div style={{ fontWeight: 600, fontSize: '0.9375rem' }}>
                      {getTemplateName(sc.template_id)}
                    </div>
                    <span className="badge badge-success">
                      <code>{sc.cron}</code>
                    </span>
                  </div>

                  <div style={{ fontSize: '0.8125rem', color: 'var(--text-muted)' }}>
                    Repo: <strong style={{ color: '#cbd5e1' }}>{getRepoName(sc.repo_id)}</strong>
                  </div>

                  <div style={{ display: 'flex', gap: '1rem', fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                    <span>
                      Next run: {sc.next_run_at ? new Date(sc.next_run_at).toLocaleString() : 'N/A'}
                    </span>
                    <span>
                      Last run: {sc.last_run_at ? new Date(sc.last_run_at).toLocaleString() : 'Never'}
                    </span>
                  </div>

                  <div style={{ display: 'flex', gap: '0.5rem', marginTop: '0.25rem' }}>
                    <button
                      className="btn btn-secondary"
                      style={{ fontSize: '0.75rem', padding: '0.375rem 0.625rem' }}
                      onClick={() => handleTriggerSchedule(sc.id)}
                    >
                      <Play style={{ width: 12, height: 12 }} /> Run Now
                    </button>
                    <button
                      className="btn btn-secondary"
                      style={{ fontSize: '0.75rem', padding: '0.375rem 0.625rem', color: '#f87171' }}
                      onClick={() => handleDeleteSchedule(sc.id)}
                    >
                      <Trash2 style={{ width: 12, height: 12 }} />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* CREATE TEMPLATE MODAL */}
      {showTmplModal && (
        <div style={{ position: 'fixed', inset: 0, backgroundColor: 'rgba(0,0,0,0.7)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '1rem' }}>
          <div className="card" style={{ width: '100%', maxWidth: 440 }}>
            <div className="card-header">
              <h2 className="card-title">New Prompt Template</h2>
              <button className="btn btn-secondary" onClick={() => setShowTmplModal(false)}>
                <X style={{ width: 16, height: 16 }} />
              </button>
            </div>
            <form onSubmit={handleCreateTemplate}>
              <div className="input-group">
                <label className="input-label">Template Name</label>
                <input
                  className="input-control"
                  placeholder="e.g. Nightly Dependency Check"
                  value={newTmplName}
                  onChange={(e) => setNewTmplName(e.target.value)}
                  required
                />
              </div>

              <div className="input-group">
                <label className="input-label">Harness</label>
                <select
                  className="input-control"
                  value={newTmplHarness}
                  onChange={(e) => setNewTmplHarness(e.target.value)}
                >
                  <option value="claude">Claude Code</option>
                  <option value="codex">Codex CLI</option>
                  <option value="gemini">Gemini CLI</option>
                  <option value="muse">Muse Code</option>
                </select>
              </div>

              <div className="input-group">
                <label className="input-label">Task Prompt</label>
                <textarea
                  className="input-control"
                  rows={4}
                  placeholder="Task instructions to send to the agent..."
                  value={newTmplPrompt}
                  onChange={(e) => setNewTmplPrompt(e.target.value)}
                  required
                />
              </div>

              <button className="btn btn-primary btn-full" type="submit">
                Save Template
              </button>
            </form>
          </div>
        </div>
      )}

      {/* CREATE SCHEDULE MODAL */}
      {showSchModal && (
        <div style={{ position: 'fixed', inset: 0, backgroundColor: 'rgba(0,0,0,0.7)', zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '1rem' }}>
          <div className="card" style={{ width: '100%', maxWidth: 440 }}>
            <div className="card-header">
              <h2 className="card-title">Schedule Recurring Agent</h2>
              <button className="btn btn-secondary" onClick={() => setShowSchModal(false)}>
                <X style={{ width: 16, height: 16 }} />
              </button>
            </div>
            <form onSubmit={handleCreateSchedule}>
              <div className="input-group">
                <label className="input-label">Repository</label>
                <select
                  className="input-control"
                  value={newSchRepoId}
                  onChange={(e) => setNewSchRepoId(e.target.value)}
                  required
                >
                  {repos.map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.name}
                    </option>
                  ))}
                </select>
              </div>

              <div className="input-group">
                <label className="input-label">Prompt Template</label>
                <select
                  className="input-control"
                  value={newSchTmplId}
                  onChange={(e) => setNewSchTmplId(e.target.value)}
                  required
                >
                  {templates.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name} ({t.harness})
                    </option>
                  ))}
                </select>
              </div>

              <div className="input-group">
                <label className="input-label">Cron Expression</label>
                <input
                  className="input-control"
                  placeholder="e.g. 0 2 * * * (daily at 2am)"
                  value={newSchCron}
                  onChange={(e) => setNewSchCron(e.target.value)}
                  required
                />
                <div style={{ display: 'flex', gap: '0.375rem', marginTop: '0.375rem' }}>
                  <button
                    type="button"
                    className="btn btn-secondary"
                    style={{ fontSize: '0.75rem', padding: '0.25rem 0.5rem' }}
                    onClick={() => setNewSchCron('0 2 * * *')}
                  >
                    Nightly (2 AM)
                  </button>
                  <button
                    type="button"
                    className="btn btn-secondary"
                    style={{ fontSize: '0.75rem', padding: '0.25rem 0.5rem' }}
                    onClick={() => setNewSchCron('@daily')}
                  >
                    Midnight
                  </button>
                  <button
                    type="button"
                    className="btn btn-secondary"
                    style={{ fontSize: '0.75rem', padding: '0.25rem 0.5rem' }}
                    onClick={() => setNewSchCron('@hourly')}
                  >
                    Hourly
                  </button>
                </div>
              </div>

              <button className="btn btn-primary btn-full" type="submit">
                Activate Schedule
              </button>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};
