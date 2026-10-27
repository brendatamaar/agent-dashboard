import React, { useEffect, useState } from 'react';
import { Bell, Send, Moon, Save } from 'lucide-react';
import { NotificationSettings as SettingsType, getNotificationSettings, updateNotificationSettings } from '../api';

export const NotificationSettings: React.FC<{ onNotify?: (msg: string) => void }> = ({ onNotify }) => {
  const [settings, setSettings] = useState<SettingsType>({
    telegram: { enabled: false, bot_token: '', chat_id: '' },
    ntfy: { enabled: false, server_url: 'https://ntfy.sh', topic: '' },
    quiet_hours: { enabled: false, start_hour: 22, start_min: 0, end_hour: 7, end_min: 0, timezone: 'UTC' },
  });
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        const s = await getNotificationSettings();
        if (s && s.telegram) setSettings(s);
      } catch (err: any) {
        if (onNotify) onNotify(`Failed to load notification settings: ${err.message}`);
      }
    })();
  }, []);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      await updateNotificationSettings(settings);
      if (onNotify) onNotify('Notification channels and quiet hours updated');
    } catch (err: any) {
      if (onNotify) onNotify(`Failed to save settings: ${err.message}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="card" style={{ marginTop: '1rem' }}>
      <div className="card-header">
        <h2 className="card-title">
          <Bell style={{ color: 'var(--primary)' }} /> Notification Channels & Quiet Hours
        </h2>
      </div>

      <form onSubmit={handleSave} style={{ display: 'flex', flexDirection: 'column', gap: '1rem', marginTop: '0.5rem' }}>
        {/* TELEGRAM */}
        <div style={{ padding: '0.875rem', backgroundColor: '#0f172a', borderRadius: '0.5rem', border: '1px solid #1e293b' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
            <span style={{ fontWeight: 600, fontSize: '0.9375rem', display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
              <Send style={{ width: 16, height: 16, color: '#38bdf8' }} /> Telegram Bot
            </span>
            <label style={{ display: 'flex', alignItems: 'center', gap: '0.375rem', fontSize: '0.8125rem' }}>
              <input
                type="checkbox"
                checked={settings.telegram?.enabled || false}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    telegram: { ...settings.telegram, enabled: e.target.checked },
                  })
                }
              />
              Enable
            </label>
          </div>

          {settings.telegram?.enabled && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem', marginTop: '0.5rem' }}>
              <div className="input-group" style={{ marginBottom: 0 }}>
                <label className="input-label" style={{ fontSize: '0.75rem' }}>Bot Token</label>
                <input
                  type="password"
                  className="input-control"
                  placeholder="123456789:ABCdefGHIjklMNOpqrSTUvwxYZ"
                  value={settings.telegram?.bot_token || ''}
                  onChange={(e) =>
                    setSettings({
                      ...settings,
                      telegram: { ...settings.telegram, bot_token: e.target.value },
                    })
                  }
                />
              </div>
              <div className="input-group" style={{ marginBottom: 0 }}>
                <label className="input-label" style={{ fontSize: '0.75rem' }}>Chat ID</label>
                <input
                  className="input-control"
                  placeholder="e.g. 987654321"
                  value={settings.telegram?.chat_id || ''}
                  onChange={(e) =>
                    setSettings({
                      ...settings,
                      telegram: { ...settings.telegram, chat_id: e.target.value },
                    })
                  }
                />
              </div>
            </div>
          )}
        </div>

        {/* NTFY */}
        <div style={{ padding: '0.875rem', backgroundColor: '#0f172a', borderRadius: '0.5rem', border: '1px solid #1e293b' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
            <span style={{ fontWeight: 600, fontSize: '0.9375rem', display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
              <Bell style={{ width: 16, height: 16, color: '#f59e0b' }} /> ntfy.sh Topic
            </span>
            <label style={{ display: 'flex', alignItems: 'center', gap: '0.375rem', fontSize: '0.8125rem' }}>
              <input
                type="checkbox"
                checked={settings.ntfy?.enabled || false}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    ntfy: { ...settings.ntfy, enabled: e.target.checked },
                  })
                }
              />
              Enable
            </label>
          </div>

          {settings.ntfy?.enabled && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem', marginTop: '0.5rem' }}>
              <div className="input-group" style={{ marginBottom: 0 }}>
                <label className="input-label" style={{ fontSize: '0.75rem' }}>Topic Name</label>
                <input
                  className="input-control"
                  placeholder="e.g. my-private-dashboard-alerts"
                  value={settings.ntfy?.topic || ''}
                  onChange={(e) =>
                    setSettings({
                      ...settings,
                      ntfy: { ...settings.ntfy, topic: e.target.value },
                    })
                  }
                />
              </div>
            </div>
          )}
        </div>

        {/* QUIET HOURS */}
        <div style={{ padding: '0.875rem', backgroundColor: '#0f172a', borderRadius: '0.5rem', border: '1px solid #1e293b' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
            <span style={{ fontWeight: 600, fontSize: '0.9375rem', display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
              <Moon style={{ width: 16, height: 16, color: '#a78bfa' }} /> Quiet Hours
            </span>
            <label style={{ display: 'flex', alignItems: 'center', gap: '0.375rem', fontSize: '0.8125rem' }}>
              <input
                type="checkbox"
                checked={settings.quiet_hours?.enabled || false}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    quiet_hours: { ...settings.quiet_hours, enabled: e.target.checked },
                  })
                }
              />
              Enable
            </label>
          </div>

          {settings.quiet_hours?.enabled && (
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.5rem', marginTop: '0.5rem' }}>
              <div className="input-group" style={{ marginBottom: 0 }}>
                <label className="input-label" style={{ fontSize: '0.75rem' }}>Start Time (Hour 0-23)</label>
                <input
                  type="number"
                  min={0}
                  max={23}
                  className="input-control"
                  value={settings.quiet_hours?.start_hour ?? 22}
                  onChange={(e) =>
                    setSettings({
                      ...settings,
                      quiet_hours: { ...settings.quiet_hours, start_hour: parseInt(e.target.value) || 0 },
                    })
                  }
                />
              </div>
              <div className="input-group" style={{ marginBottom: 0 }}>
                <label className="input-label" style={{ fontSize: '0.75rem' }}>End Time (Hour 0-23)</label>
                <input
                  type="number"
                  min={0}
                  max={23}
                  className="input-control"
                  value={settings.quiet_hours?.end_hour ?? 7}
                  onChange={(e) =>
                    setSettings({
                      ...settings,
                      quiet_hours: { ...settings.quiet_hours, end_hour: parseInt(e.target.value) || 0 },
                    })
                  }
                />
              </div>
              <div style={{ gridColumn: 'span 2', fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                * Normal notifications are silenced; urgent approvals and errors bypass quiet hours.
              </div>
            </div>
          )}
        </div>

        <button className="btn btn-primary btn-full" type="submit" disabled={saving}>
          <Save style={{ width: 16, height: 16 }} /> {saving ? 'Saving...' : 'Save Notification Preferences'}
        </button>
      </form>
    </div>
  );
};
