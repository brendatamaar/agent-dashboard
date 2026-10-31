import { errorMessage } from '../errors';
import React, { useState, useEffect } from 'react';
import {
  X,
  GitCommit,
  GitPullRequest,
  Upload,
  Download,
  FileCheck,
  FileX,
  Sparkles,
  AlertTriangle,
  RefreshCw,
} from 'lucide-react';
import {
  GitStatus,
  FileChange,
  getGitStatus,
  getGitChanges,
  getGitDiff,
  stageGitFiles,
  unstageGitFiles,
  commitGit,
  pushGit,
  pullRebaseGit,
  createPR,
} from '../api';
import { UnifiedDiff } from './UnifiedDiff';

interface CommitSheetProps {
  agentId: string;
  agentBranch: string;
  agentPrompt?: string;
  onClose: () => void;
  onSuccess?: (msg: string) => void;
}

export const CommitSheet: React.FC<CommitSheetProps> = ({
  agentId,
  agentBranch,
  agentPrompt,
  onClose,
  onSuccess,
}) => {
  const [status, setStatus] = useState<GitStatus | null>(null);
  const [changes, setChanges] = useState<FileChange[]>([]);
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [diff, setDiff] = useState<string>('');
  const [viewStagedDiff, setViewStagedDiff] = useState(false);
  const [commitMessage, setCommitMessage] = useState('');
  const [amend, setAmend] = useState(false);
  const [showMainConfirmModal, setShowMainConfirmModal] = useState(false);
  const [showPRModal, setShowPRModal] = useState(false);
  const [prTitle, setPrTitle] = useState(`feat: ${agentBranch.replace('agent/', '')}`);
  const [prBody, setPrBody] = useState(agentPrompt ? `Summary of changes for: ${agentPrompt}` : '');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadGitState = async () => {
    try {
      const [st, ch] = await Promise.all([
        getGitStatus(agentId),
        getGitChanges(agentId),
      ]);
      setStatus(st);
      setChanges(ch);
    } catch (err: unknown) {
      setError(errorMessage(err) || 'Failed to load git status');
    }
  };

  useEffect(() => {
    loadGitState();
  }, [agentId]);

  const loadDiff = async (file?: string, staged = viewStagedDiff) => {
    try {
      const d = await getGitDiff(agentId, file, staged);
      setDiff(d);
    } catch (err: unknown) {
      setError(errorMessage(err) || 'Failed to load diff');
    }
  };

  useEffect(() => {
    loadDiff(selectedFile || undefined, viewStagedDiff);
  }, [selectedFile, viewStagedDiff]);

  const handleStageFile = async (path: string, e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      await stageGitFiles(agentId, [path]);
      await loadGitState();
      await loadDiff(selectedFile || undefined);
    } catch (err: unknown) {
      setError(errorMessage(err));
    }
  };

  const handleUnstageFile = async (path: string, e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      await unstageGitFiles(agentId, [path]);
      await loadGitState();
      await loadDiff(selectedFile || undefined);
    } catch (err: unknown) {
      setError(errorMessage(err));
    }
  };

  const handleStageAll = async () => {
    try {
      await stageGitFiles(agentId, []);
      await loadGitState();
      await loadDiff(selectedFile || undefined);
    } catch (err: unknown) {
      setError(errorMessage(err));
    }
  };

  const handleUnstageAll = async () => {
    try {
      await unstageGitFiles(agentId, []);
      await loadGitState();
      await loadDiff(selectedFile || undefined);
    } catch (err: unknown) {
      setError(errorMessage(err));
    }
  };

  const handleDraftMessage = () => {
    if (agentPrompt) {
      const firstLine = agentPrompt.split('\n')[0].trim();
      const slug = firstLine.slice(0, 60);
      setCommitMessage(`feat: ${slug}\n\nAutomated commit by agent for:\n${agentPrompt}`);
    } else {
      setCommitMessage(`feat: update ${agentBranch}`);
    }
  };

  const handleCommit = async () => {
    if (!commitMessage.trim()) {
      setError('Commit message cannot be empty');
      return;
    }
    setLoading(true);
    setError(null);
    try {
      await commitGit(agentId, commitMessage.trim(), amend);
      setCommitMessage('');
      setAmend(false);
      await loadGitState();
      await loadDiff();
      onSuccess?.('Commit successful');
    } catch (err: unknown) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  const executePush = async (overrideConfirm = false) => {
    setLoading(true);
    setError(null);
    try {
      await pushGit(agentId, false, overrideConfirm);
      setShowMainConfirmModal(false);
      await loadGitState();
      onSuccess?.('Pushed to upstream successfully');
    } catch (err: unknown) {
      if (errorMessage(err) && errorMessage(err).includes('explicit confirmation')) {
        setShowMainConfirmModal(true);
      } else {
        setError(errorMessage(err));
      }
    } finally {
      setLoading(false);
    }
  };

  const handlePush = () => {
    if (status?.current_branch === 'main' || status?.current_branch === 'master') {
      setShowMainConfirmModal(true);
      return;
    }
    executePush();
  };

  const handlePullRebase = async () => {
    setLoading(true);
    setError(null);
    try {
      await pullRebaseGit(agentId);
      await loadGitState();
      onSuccess?.('Rebased successfully onto base branch');
    } catch (err: unknown) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  const handleCreatePR = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await createPR(agentId, prTitle, prBody);
      setShowPRModal(false);
      onSuccess?.(`PR #${res.pr.number} created: ${res.pr.html_url}`);
    } catch (err: unknown) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  const stagedCount = changes.filter((c) => c.staged).length;

  return (
    <div className="fixed inset-0 z-50 flex flex-col justify-end bg-black/60 backdrop-blur-xs">
      <div className="flex flex-col h-[90vh] max-h-[850px] w-full bg-slate-900 border-t border-slate-700 rounded-t-2xl shadow-2xl overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-slate-800 bg-slate-900/90">
          <div className="flex items-center gap-2">
            <GitCommit className="w-5 h-5 text-sky-400" />
            <div>
              <h2 className="text-sm font-semibold text-slate-100">Review & Ship</h2>
              <div className="text-[11px] text-slate-400 font-mono">
                {status?.current_branch || agentBranch}
                {status?.ahead ? ` · ↑${status.ahead}` : ''}
                {status?.behind ? ` · ↓${status.behind}` : ''}
              </div>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => loadGitState()}
              className="p-1.5 text-slate-400 hover:text-slate-200"
              title="Refresh"
            >
              <RefreshCw className="w-4 h-4" />
            </button>
            <button onClick={onClose} className="p-1.5 text-slate-400 hover:text-slate-200">
              <X className="w-5 h-5" />
            </button>
          </div>
        </div>

        {/* Error banner */}
        {error && (
          <div className="flex items-center gap-2 px-4 py-2 bg-rose-950/60 border-b border-rose-800/60 text-xs text-rose-300">
            <AlertTriangle className="w-4 h-4 shrink-0 text-rose-400" />
            <span className="flex-1">{error}</span>
            <button onClick={() => setError(null)} className="text-rose-400 hover:text-rose-200">
              ✕
            </button>
          </div>
        )}

        {/* Content area: scrollable */}
        <div className="flex-1 overflow-y-auto p-4 space-y-4">
          {/* Changed files list */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-medium text-slate-300">
                Changed Files ({changes.length})
                {stagedCount > 0 && <span className="text-emerald-400 ml-1">· {stagedCount} staged</span>}
              </span>
              <div className="flex items-center gap-1.5 text-[11px]">
                <button
                  type="button"
                  onClick={handleStageAll}
                  className="px-2 py-0.5 bg-slate-800 hover:bg-slate-700 text-slate-300 rounded"
                >
                  Stage All
                </button>
                <button
                  type="button"
                  onClick={handleUnstageAll}
                  className="px-2 py-0.5 bg-slate-800 hover:bg-slate-700 text-slate-300 rounded"
                >
                  Unstage All
                </button>
              </div>
            </div>

            <div className="divide-y divide-slate-800/60 border border-slate-800 rounded-lg overflow-hidden bg-slate-950 max-h-48 overflow-y-auto">
              {changes.length === 0 ? (
                <div className="p-3 text-center text-xs text-slate-500">Working tree clean</div>
              ) : (
                changes.map((c) => {
                  const isSelected = selectedFile === c.path;
                  return (
                    <div
                      key={c.path}
                      onClick={() => setSelectedFile(isSelected ? null : c.path)}
                      className={`flex items-center justify-between px-3 py-2 text-xs cursor-pointer ${
                        isSelected ? 'bg-sky-950/40 text-sky-200' : 'hover:bg-slate-900/60 text-slate-300'
                      }`}
                    >
                      <div className="flex items-center gap-2 truncate">
                        <span
                          className={`text-[10px] font-mono px-1 py-0.5 rounded ${
                            c.status === 'modified'
                              ? 'text-sky-400 bg-sky-950/50'
                              : c.status === 'added' || c.status === 'untracked'
                              ? 'text-emerald-400 bg-emerald-950/50'
                              : 'text-rose-400 bg-rose-950/50'
                          }`}
                        >
                          {c.status.toUpperCase().slice(0, 3)}
                        </span>
                        <span className="truncate font-mono">{c.path}</span>
                      </div>

                      <div className="flex items-center gap-1">
                        {c.staged ? (
                          <button
                            type="button"
                            onClick={(e) => handleUnstageFile(c.path, e)}
                            className="p-1 hover:bg-slate-800 text-emerald-400 rounded"
                            title="Unstage file"
                          >
                            <FileCheck className="w-3.5 h-3.5" />
                          </button>
                        ) : (
                          <button
                            type="button"
                            onClick={(e) => handleStageFile(c.path, e)}
                            className="p-1 hover:bg-slate-800 text-slate-400 hover:text-emerald-400 rounded"
                            title="Stage file"
                          >
                            <FileX className="w-3.5 h-3.5" />
                          </button>
                        )}
                      </div>
                    </div>
                  );
                })
              )}
            </div>
          </div>

          {/* Unified Diff View */}
          <div>
            <div className="flex items-center justify-between mb-1.5">
              <span className="text-xs font-medium text-slate-300">
                {selectedFile ? `Diff: ${selectedFile}` : 'All Working Tree Diffs'}
              </span>
              <div className="flex items-center gap-2">
                <label className="text-[11px] text-slate-400 flex items-center gap-1 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={viewStagedDiff}
                    onChange={(e) => setViewStagedDiff(e.target.checked)}
                    className="rounded border-slate-700 text-sky-600 focus:ring-0"
                  />
                  Staged only
                </label>
              </div>
            </div>
            <UnifiedDiff diff={diff} className="max-h-64" />
          </div>

          {/* Commit Section */}
          <div className="bg-slate-950/60 p-3 rounded-xl border border-slate-800 space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs font-medium text-slate-300">Commit Message</label>
              <button
                type="button"
                onClick={handleDraftMessage}
                className="flex items-center gap-1 text-[11px] text-sky-400 hover:underline"
              >
                <Sparkles className="w-3 h-3" />
                Draft with Agent Summary
              </button>
            </div>

            <textarea
              rows={3}
              value={commitMessage}
              onChange={(e) => setCommitMessage(e.target.value)}
              placeholder="feat: concise commit description..."
              className="w-full p-2.5 bg-slate-900 border border-slate-700 rounded-lg text-xs font-mono text-slate-100 placeholder:text-slate-500 focus:outline-hidden focus:border-sky-500"
            />

            <div className="flex items-center justify-between pt-1">
              <label className="text-xs text-slate-400 flex items-center gap-1.5 cursor-pointer">
                <input
                  type="checkbox"
                  checked={amend}
                  onChange={(e) => setAmend(e.target.checked)}
                  className="rounded border-slate-700 text-sky-600 focus:ring-0"
                />
                Amend previous commit
              </label>

              <button
                type="button"
                disabled={loading || !commitMessage.trim()}
                onClick={handleCommit}
                className="py-2 px-4 bg-sky-600 hover:bg-sky-500 active:bg-sky-700 text-white rounded-lg text-xs font-semibold shadow disabled:opacity-50"
              >
                Commit Changes
              </button>
            </div>
          </div>
        </div>

        {/* Footer Actions (Phone-first one-handed buttons) */}
        <div className="p-3 bg-slate-900 border-t border-slate-800 grid grid-cols-3 gap-2">
          <button
            type="button"
            disabled={loading}
            onClick={handlePullRebase}
            className="flex items-center justify-center gap-1.5 py-3 px-2 bg-slate-800 hover:bg-slate-700 active:bg-slate-800 text-slate-200 rounded-xl text-xs font-medium"
          >
            <Download className="w-4 h-4 text-sky-400" />
            Pull/Rebase
          </button>

          <button
            type="button"
            disabled={loading}
            onClick={handlePush}
            className="flex items-center justify-center gap-1.5 py-3 px-2 bg-slate-800 hover:bg-slate-700 active:bg-slate-800 text-slate-200 rounded-xl text-xs font-medium"
          >
            <Upload className="w-4 h-4 text-emerald-400" />
            Push
          </button>

          <button
            type="button"
            disabled={loading}
            onClick={() => setShowPRModal(true)}
            className="flex items-center justify-center gap-1.5 py-3 px-2 bg-emerald-600 hover:bg-emerald-500 active:bg-emerald-700 text-white rounded-xl text-xs font-semibold shadow"
          >
            <GitPullRequest className="w-4 h-4" />
            Open PR
          </button>
        </div>
      </div>

      {/* Confirmation Modal for Pushing to Main / Protected Branch */}
      {showMainConfirmModal && (
        <div className="fixed inset-0 z-60 flex items-center justify-center p-4 bg-black/75">
          <div className="w-full max-w-sm bg-slate-900 border border-amber-500/50 rounded-2xl p-5 shadow-2xl space-y-4">
            <div className="flex items-center gap-3 text-amber-400">
              <AlertTriangle className="w-6 h-6 shrink-0" />
              <h3 className="text-base font-bold text-slate-100">Protected Branch Warning</h3>
            </div>
            <p className="text-xs text-slate-300 leading-relaxed">
              You are attempting to push directly to <span className="font-mono text-amber-300 font-semibold">{status?.current_branch}</span>.
              Direct pushes to protected branches are restricted by dashboard guardrails.
            </p>
            <p className="text-xs text-slate-400">
              Do you explicitly confirm that you want to push to this protected branch?
            </p>
            <div className="flex gap-2.5 pt-2">
              <button
                type="button"
                onClick={() => setShowMainConfirmModal(false)}
                className="flex-1 py-2.5 bg-slate-800 text-slate-300 rounded-lg text-xs font-medium"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => executePush(true)}
                className="flex-1 py-2.5 bg-amber-600 hover:bg-amber-500 text-white rounded-lg text-xs font-semibold"
              >
                Confirm Push
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Pull Request Draft & Submit Modal */}
      {showPRModal && (
        <div className="fixed inset-0 z-60 flex items-center justify-center p-4 bg-black/75">
          <div className="w-full max-w-md bg-slate-900 border border-slate-700 rounded-2xl p-5 shadow-2xl space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-base font-bold text-slate-100 flex items-center gap-2">
                <GitPullRequest className="w-5 h-5 text-emerald-400" />
                Open Pull Request
              </h3>
              <button onClick={() => setShowPRModal(false)} className="text-slate-400 hover:text-slate-200">
                ✕
              </button>
            </div>

            <div className="space-y-3">
              <div>
                <label className="text-xs text-slate-300 font-medium mb-1 block">PR Title</label>
                <input
                  type="text"
                  value={prTitle}
                  onChange={(e) => setPrTitle(e.target.value)}
                  className="w-full p-2.5 bg-slate-950 border border-slate-700 rounded-lg text-xs font-mono text-slate-100"
                />
              </div>

              <div>
                <label className="text-xs text-slate-300 font-medium mb-1 block">PR Description</label>
                <textarea
                  rows={5}
                  value={prBody}
                  onChange={(e) => setPrBody(e.target.value)}
                  className="w-full p-2.5 bg-slate-950 border border-slate-700 rounded-lg text-xs font-mono text-slate-100"
                />
              </div>
            </div>

            <div className="flex gap-2.5 pt-2">
              <button
                type="button"
                onClick={() => setShowPRModal(false)}
                className="flex-1 py-2.5 bg-slate-800 text-slate-300 rounded-lg text-xs font-medium"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={loading || !prTitle.trim()}
                onClick={handleCreatePR}
                className="flex-1 py-2.5 bg-emerald-600 hover:bg-emerald-500 text-white rounded-lg text-xs font-semibold disabled:opacity-50"
              >
                Submit PR
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
