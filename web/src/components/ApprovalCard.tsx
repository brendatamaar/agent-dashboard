import React, { useState } from 'react';
import { Check, X, ShieldCheck, Terminal, FileCode } from 'lucide-react';
import { UnifiedDiff } from './UnifiedDiff';

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

interface ApprovalCardProps {
  approval: ApprovalItem;
  onDecide: (decision: 'approve' | 'deny' | 'always_allow') => Promise<void>;
}

export const ApprovalCard: React.FC<ApprovalCardProps> = ({ approval, onDecide }) => {
  const [loading, setLoading] = useState(false);
  const [showDetails, setShowDetails] = useState(false);

  // Parse request_json if fields are empty
  let command = approval.command;
  let filePath = approval.file_path;
  let diff = approval.diff;
  let toolName = approval.tool_name || 'Action';

  if (approval.request_json && (!command && !filePath && !diff)) {
    try {
      const parsed = JSON.parse(approval.request_json);
      toolName = parsed.tool_name || toolName;
      command = parsed.command;
      filePath = parsed.file_path;
      diff = parsed.diff;
    } catch {
      // ignore
    }
  }

  const handleAction = async (decision: 'approve' | 'deny' | 'always_allow') => {
    setLoading(true);
    try {
      await onDecide(decision);
    } finally {
      setLoading(false);
    }
  };

  const isPending = approval.decision === 'pending';

  return (
    <div className={`rounded-xl border p-3.5 shadow-sm transition-all ${
      isPending
        ? 'border-amber-500/40 bg-amber-950/20 ring-1 ring-amber-500/30'
        : approval.decision === 'approved' || approval.decision === 'always_allowed'
        ? 'border-emerald-800/40 bg-emerald-950/10'
        : 'border-rose-800/40 bg-rose-950/10'
    }`}>
      {/* Header */}
      <div className="flex items-center justify-between gap-2 mb-2">
        <div className="flex items-center gap-1.5">
          {toolName.toLowerCase().includes('bash') ? (
            <Terminal className="w-4 h-4 text-amber-400" />
          ) : (
            <FileCode className="w-4 h-4 text-sky-400" />
          )}
          <span className="font-semibold text-xs text-slate-200">
            Permission Request: <span className="font-mono text-amber-300">{toolName}</span>
          </span>
        </div>
        <span className={`text-[10px] px-2 py-0.5 rounded-full font-medium ${
          isPending
            ? 'bg-amber-500/20 text-amber-300 border border-amber-500/30 animate-pulse'
            : approval.decision === 'approved'
            ? 'bg-emerald-500/20 text-emerald-300'
            : approval.decision === 'always_allowed'
            ? 'bg-sky-500/20 text-sky-300'
            : 'bg-rose-500/20 text-rose-300'
        }`}>
          {approval.decision.toUpperCase().replace('_', ' ')}
        </span>
      </div>

      {/* Content / Command / File */}
      {command && (
        <div className="mb-2">
          <div className="text-[11px] text-slate-400 mb-1">Command to execute:</div>
          <div className="p-2 bg-slate-950 rounded border border-slate-800 font-mono text-xs text-amber-200 break-all select-all">
            {command}
          </div>
        </div>
      )}

      {filePath && (
        <div className="mb-2">
          <div className="text-[11px] text-slate-400 mb-1">Target file:</div>
          <div className="font-mono text-xs text-sky-300">{filePath}</div>
        </div>
      )}

      {diff && (
        <div className="mb-2">
          <button
            type="button"
            onClick={() => setShowDetails(!showDetails)}
            className="text-[11px] text-sky-400 hover:underline mb-1"
          >
            {showDetails ? 'Hide unified diff' : 'Show proposed diff'}
          </button>
          {showDetails && <UnifiedDiff diff={diff} className="max-h-60" />}
        </div>
      )}

      {/* Action buttons (Phone-first, large touch targets) */}
      {isPending && (
        <div className="grid grid-cols-3 gap-2 mt-3 pt-2 border-t border-slate-800/80">
          <button
            type="button"
            disabled={loading}
            onClick={() => handleAction('approve')}
            className="flex items-center justify-center gap-1.5 py-2.5 px-3 bg-emerald-600 hover:bg-emerald-500 active:bg-emerald-700 text-white rounded-lg text-xs font-semibold shadow disabled:opacity-50"
          >
            <Check className="w-4 h-4" />
            Approve
          </button>

          <button
            type="button"
            disabled={loading}
            onClick={() => handleAction('always_allow')}
            className="flex items-center justify-center gap-1.5 py-2.5 px-3 bg-sky-600 hover:bg-sky-500 active:bg-sky-700 text-white rounded-lg text-xs font-semibold shadow disabled:opacity-50"
            title="Always allow this tool for this agent"
          >
            <ShieldCheck className="w-4 h-4" />
            Always
          </button>

          <button
            type="button"
            disabled={loading}
            onClick={() => handleAction('deny')}
            className="flex items-center justify-center gap-1.5 py-2.5 px-3 bg-rose-600 hover:bg-rose-500 active:bg-rose-700 text-white rounded-lg text-xs font-semibold shadow disabled:opacity-50"
          >
            <X className="w-4 h-4" />
            Deny
          </button>
        </div>
      )}
    </div>
  );
};
