import React, { useEffect, useRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';

interface TerminalProps {
  agentId?: string;
  onClose?: () => void;
}

export const Terminal: React.FC<TerminalProps> = ({ agentId, onClose }) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    if (!containerRef.current) return;

    const term = new XTerm({
      cursorBlink: true,
      fontFamily: 'monospace',
      fontSize: 13,
      theme: {
        background: '#0f172a',
        foreground: '#f8fafc',
        cursor: '#38bdf8',
      },
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(containerRef.current);
    fitAddon.fit();
    termRef.current = term;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/terminal/ws${agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = 'arraybuffer';
    wsRef.current = ws;

    ws.onopen = () => {
      // Send initial dimensions
      ws.send(JSON.stringify({ type: 'resize', rows: term.rows, cols: term.cols }));
    };

    ws.onmessage = (event) => {
      if (typeof event.data === 'string') {
        term.write(event.data);
      } else {
        term.write(new Uint8Array(event.data));
      }
    };

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data));
      }
    });

    const handleResize = () => {
      fitAddon.fit();
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', rows: term.rows, cols: term.cols }));
      }
    };

    window.addEventListener('resize', handleResize);

    return () => {
      window.removeEventListener('resize', handleResize);
      ws.close();
      term.dispose();
    };
  }, [agentId]);

  // Terminal mobile extra keys
  const sendKey = (key: string) => {
    if (termRef.current && wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      wsRef.current.send(new TextEncoder().encode(key));
      termRef.current.focus();
    }
  };

  return (
    <div className="flex flex-col h-full bg-slate-900 rounded-lg overflow-hidden border border-slate-700">
      <div className="flex items-center justify-between px-3 py-2 bg-slate-800 border-b border-slate-700 text-xs text-slate-300">
        <div className="flex items-center gap-2">
          <span className="w-2.5 h-2.5 rounded-full bg-emerald-500 animate-pulse"></span>
          <span className="font-mono">{agentId ? `Session: ${agentId}` : 'Host Shell'}</span>
        </div>
        {onClose && (
          <button
            onClick={onClose}
            className="px-2 py-1 bg-slate-700 hover:bg-slate-600 rounded text-slate-200"
          >
            Close
          </button>
        )}
      </div>

      <div ref={containerRef} className="flex-1 w-full p-2 overflow-hidden bg-slate-950" />

      {/* Phone-first mobile toolbar row for terminal (Esc, Tab, Ctrl-C, Ctrl-D, Arrows) */}
      <div className="flex items-center gap-1.5 p-2 bg-slate-800/90 border-t border-slate-700 overflow-x-auto text-xs">
        <button
          onClick={() => sendKey('\x1b')}
          className="px-2.5 py-1.5 bg-slate-700 active:bg-sky-600 rounded font-mono text-slate-200"
        >
          Esc
        </button>
        <button
          onClick={() => sendKey('\t')}
          className="px-2.5 py-1.5 bg-slate-700 active:bg-sky-600 rounded font-mono text-slate-200"
        >
          Tab
        </button>
        <button
          onClick={() => sendKey('\x03')}
          className="px-2.5 py-1.5 bg-slate-700 active:bg-rose-600 rounded font-mono text-rose-300"
        >
          Ctrl+C
        </button>
        <button
          onClick={() => sendKey('\x04')}
          className="px-2.5 py-1.5 bg-slate-700 active:bg-sky-600 rounded font-mono text-slate-200"
        >
          Ctrl+D
        </button>
        <button
          onClick={() => sendKey('\x1b[A')}
          className="px-2.5 py-1.5 bg-slate-700 active:bg-sky-600 rounded font-mono text-slate-200"
        >
          ▲
        </button>
        <button
          onClick={() => sendKey('\x1b[B')}
          className="px-2.5 py-1.5 bg-slate-700 active:bg-sky-600 rounded font-mono text-slate-200"
        >
          ▼
        </button>
        <button
          onClick={() => sendKey('\x1b[D')}
          className="px-2.5 py-1.5 bg-slate-700 active:bg-sky-600 rounded font-mono text-slate-200"
        >
          ◀
        </button>
        <button
          onClick={() => sendKey('\x1b[C')}
          className="px-2.5 py-1.5 bg-slate-700 active:bg-sky-600 rounded font-mono text-slate-200"
        >
          ▶
        </button>
      </div>
    </div>
  );
};
