import React, { useEffect, useState } from 'react';
import { Bot, Plus, Trash2, Edit3, X, Save } from 'lucide-react';
import { SubagentDef, listRepoSubagents, saveRepoSubagent, deleteRepoSubagent } from '../api';

interface RepoSubagentsProps {
  repoId: string;
  onNotify?: (msg: string) => void;
}

export const RepoSubagents: React.FC<RepoSubagentsProps> = ({ repoId, onNotify }) => {
  const [subagents, setSubagents] = useState<SubagentDef[]>([]);
  const [showModal, setShowModal] = useState(false);
  const [currentName, setCurrentName] = useState('');
  const [currentContent, setCurrentContent] = useState('');
  const [saving, setSaving] = useState(false);

  const fetchSubagents = async () => {
    try {
      const list = await listRepoSubagents(repoId);
      setSubagents(list);
    } catch (err: any) {
      if (onNotify) onNotify(`Failed to list subagents: ${err.message}`);
    }
  };

  useEffect(() => {
    fetchSubagents();
  }, [repoId]);

  const handleOpenCreate = () => {
    setCurrentName('');
    setCurrentContent('# Subagent Role\nDescribe specialized responsibilities...');
    setShowModal(true);
  };

  const handleOpenEdit = (sub: SubagentDef) => {
    setCurrentName(sub.name);
    setCurrentContent(sub.content);
    setShowModal(true);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const saved = await saveRepoSubagent(repoId, { name: currentName, content: currentContent });
      setSubagents((prev) => {
        const filtered = prev.filter((s) => s.name !== saved.name);
        return [...filtered, saved];
      });
      setShowModal(false);
      if (onNotify) onNotify(`Saved subagent .claude/agents/${saved.name}.md`);
    } catch (err: any) {
      if (onNotify) onNotify(`Failed to save subagent: ${err.message}`);
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (name: string) => {
    if (!confirm(`Delete subagent definition .claude/agents/${name}.md?`)) return;
    try {
      await deleteRepoSubagent(repoId, name);
      setSubagents((prev) => prev.filter((s) => s.name !== name));
      if (onNotify) onNotify('Subagent deleted');
    } catch (err: any) {
      if (onNotify) onNotify(`Failed to delete subagent: ${err.message}`);
    }
  };

  return (
    <div style={{ marginTop: '1rem', borderTop: '1px solid #1e293b', paddingTop: '1rem' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' }}>
        <div>
          <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <Bot style={{ width: 16, height: 16, color: '#818cf8' }} /> Claude Subagents (<code>.claude/agents/*.md</code>)
          </h3>
          <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)', margin: 0 }}>
            Specialized persona agents available in this repository
          </p>
        </div>
        <button className="btn btn-secondary" style={{ fontSize: '0.75rem', padding: '0.25rem 0.5rem' }} onClick={handleOpenCreate}>
          <Plus style={{ width: 14, height: 14 }} /> Add Subagent
        </button>
      </div>

      {subagents.length === 0 ? (
        <p style={{ fontSize: '0.8125rem', color: 'var(--text-muted)', fontStyle: 'italic', padding: '0.5rem 0' }}>
          No custom subagents configured in .claude/agents/
        </p>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
          {subagents.map((sub) => (
            <div
              key={sub.name}
              style={{
                padding: '0.625rem 0.75rem',
                backgroundColor: '#0f172a',
                borderRadius: '0.375rem',
                border: '1px solid #1e293b',
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
              }}
            >
              <div>
                <strong style={{ fontSize: '0.875rem' }}>{sub.name}.md</strong>
                <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', maxWidth: '280px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {sub.content.split('\n')[0]}
                </div>
              </div>
              <div style={{ display: 'flex', gap: '0.375rem' }}>
                <button
                  className="btn btn-secondary"
                  style={{ padding: '0.25rem 0.375rem' }}
                  onClick={() => handleOpenEdit(sub)}
                >
                  <Edit3 style={{ width: 12, height: 12 }} />
                </button>
                <button
                  className="btn btn-secondary"
                  style={{ padding: '0.25rem 0.375rem', color: '#f87171' }}
                  onClick={() => handleDelete(sub.name)}
                >
                  <Trash2 style={{ width: 12, height: 12 }} />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* CREATE / EDIT SUBAGENT MODAL */}
      {showModal && (
        <div style={{ position: 'fixed', inset: 0, backgroundColor: 'rgba(0,0,0,0.7)', zIndex: 110, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '1rem' }}>
          <div className="card" style={{ width: '100%', maxWidth: 460 }}>
            <div className="card-header">
              <h2 className="card-title">Subagent Definition</h2>
              <button className="btn btn-secondary" onClick={() => setShowModal(false)}>
                <X style={{ width: 16, height: 16 }} />
              </button>
            </div>
            <form onSubmit={handleSave}>
              <div className="input-group">
                <label className="input-label">Subagent Name (filename: [name].md)</label>
                <input
                  className="input-control"
                  placeholder="e.g. security-auditor"
                  value={currentName}
                  onChange={(e) => setCurrentName(e.target.value)}
                  required
                />
              </div>

              <div className="input-group">
                <label className="input-label">Markdown Prompt & Instructions</label>
                <textarea
                  className="input-control"
                  rows={8}
                  style={{ fontFamily: 'monospace', fontSize: '0.8125rem' }}
                  value={currentContent}
                  onChange={(e) => setCurrentContent(e.target.value)}
                  required
                />
              </div>

              <button className="btn btn-primary btn-full" type="submit" disabled={saving}>
                <Save style={{ width: 14, height: 14 }} /> {saving ? 'Saving...' : 'Save Subagent'}
              </button>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};
