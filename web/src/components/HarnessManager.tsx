import React, { useEffect, useState } from 'react';
import { Cpu, RefreshCw, CheckCircle2, XCircle, Download } from 'lucide-react';
import { HarnessInfo, listHarnesses, updateHarness } from '../api';

export const HarnessManager: React.FC<{ onNotify?: (msg: string) => void }> = ({ onNotify }) => {
  const [harnesses, setHarnesses] = useState<HarnessInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [updating, setUpdating] = useState<string | null>(null);

  const fetchHarnesses = async () => {
    setLoading(true);
    try {
      const data = await listHarnesses();
      setHarnesses(data);
    } catch (err: any) {
      if (onNotify) onNotify(`Failed to fetch harnesses: ${err.message}`);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchHarnesses();
  }, []);

  const handleUpdate = async (name: string) => {
    setUpdating(name);
    try {
      const res = await updateHarness(name);
      if (onNotify) onNotify(`Updated ${name}: ${res.status}`);
      await fetchHarnesses();
    } catch (err: any) {
      if (onNotify) onNotify(`Update failed for ${name}: ${err.message}`);
    } finally {
      setUpdating(null);
    }
  };

  const getHarnessTitle = (name: string) => {
    switch (name) {
      case 'claude':
        return 'Claude Code';
      case 'codex':
        return 'Codex CLI';
      case 'gemini':
        return 'Gemini CLI';
      case 'muse':
        return 'Muse Code';
      default:
        return name;
    }
  };

  return (
    <div className="card" style={{ marginTop: '1rem' }}>
      <div className="card-header">
        <h2 className="card-title">
          <Cpu style={{ color: 'var(--primary)' }} /> Harness Manager
        </h2>
        <button className="btn btn-secondary" onClick={fetchHarnesses} disabled={loading}>
          <RefreshCw style={{ width: 14, height: 14 }} className={loading ? 'spin' : ''} />
        </button>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: '0.75rem', marginTop: '0.5rem' }}>
        {harnesses.map((h) => (
          <div
            key={h.name}
            style={{
              padding: '0.875rem',
              backgroundColor: '#0f172a',
              borderRadius: '0.5rem',
              border: '1px solid #1e293b',
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'space-between',
            }}
          >
            <div>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
                <span style={{ fontWeight: 600, fontSize: '0.9375rem' }}>{getHarnessTitle(h.name)}</span>
                {h.installed ? (
                  <span className="badge badge-success" style={{ display: 'flex', alignItems: 'center', gap: '0.25rem' }}>
                    <CheckCircle2 style={{ width: 12, height: 12 }} /> Installed
                  </span>
                ) : (
                  <span className="badge badge-danger" style={{ display: 'flex', alignItems: 'center', gap: '0.25rem' }}>
                    <XCircle style={{ width: 12, height: 12 }} /> Not Installed
                  </span>
                )}
              </div>

              <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginBottom: '0.375rem' }}>
                Version: <code style={{ color: '#cbd5e1' }}>{h.version?.version || 'N/A'}</code>
              </div>

              <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginBottom: '0.75rem' }}>
                Auth Status:{' '}
                {h.auth_status?.logged_in ? (
                  <span style={{ color: '#34d399', fontWeight: 500 }}>
                    Active ({h.auth_status.auth_method || 'authenticated'})
                  </span>
                ) : (
                  <span style={{ color: '#f59e0b', fontWeight: 500 }}>Unauthenticated / Key required</span>
                )}
              </div>
            </div>

            <button
              className="btn btn-secondary btn-full"
              style={{ fontSize: '0.75rem', padding: '0.375rem 0.5rem' }}
              onClick={() => handleUpdate(h.name)}
              disabled={updating === h.name}
            >
              <Download style={{ width: 12, height: 12 }} />
              {updating === h.name ? 'Updating...' : 'Update CLI'}
            </button>
          </div>
        ))}
      </div>
    </div>
  );
};
