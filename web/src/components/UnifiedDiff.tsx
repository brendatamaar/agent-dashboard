import React from 'react';

interface UnifiedDiffProps {
  diff: string;
  className?: string;
}

export const UnifiedDiff: React.FC<UnifiedDiffProps> = ({ diff, className = '' }) => {
  if (!diff || !diff.trim()) {
    return (
      <div className="p-4 text-center text-xs text-slate-400 bg-slate-900/50 rounded border border-slate-800">
        No differences
      </div>
    );
  }

  const lines = diff.split('\n');

  return (
    <div className={`font-mono text-xs overflow-x-auto rounded border border-slate-800 bg-slate-950 p-2 text-slate-300 ${className}`}>
      <table className="w-full border-collapse">
        <tbody>
          {lines.map((line, idx) => {
            let rowBg = '';
            let textColor = 'text-slate-300';

            if (line.startsWith('+++') || line.startsWith('---')) {
              rowBg = 'bg-slate-800/60 font-semibold';
              textColor = 'text-slate-200';
            } else if (line.startsWith('diff --git') || line.startsWith('index ')) {
              rowBg = 'bg-slate-800/40 text-slate-400 font-semibold';
              textColor = 'text-sky-400';
            } else if (line.startsWith('@@')) {
              rowBg = 'bg-sky-950/40 border-y border-sky-900/30';
              textColor = 'text-sky-400 font-semibold';
            } else if (line.startsWith('+')) {
              rowBg = 'bg-emerald-950/40';
              textColor = 'text-emerald-300';
            } else if (line.startsWith('-')) {
              rowBg = 'bg-rose-950/40';
              textColor = 'text-rose-300';
            }

            return (
              <tr key={idx} className={`${rowBg} hover:bg-slate-800/50 leading-5`}>
                <td className="w-8 select-none pr-2 text-right text-slate-600 border-r border-slate-800/60">
                  {idx + 1}
                </td>
                <td className={`pl-2 whitespace-pre font-mono ${textColor}`}>
                  {line}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
};
