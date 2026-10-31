"""Private tmux supervisor. Logs are redacted before the first write to disk."""
import json
import os
import re
import signal
import subprocess
import sys
import time
import urllib.request
import urllib.parse
import uuid
import urllib.error
import socket

MAX_LINE = 10 * 1024 * 1024
PREFIXES = [b"ghp_", b"gho_", b"ghu_", b"ghs_", b"ghr_", b"github_pat_", b"sk-", b"AIza", b"Bearer "]
MAX_LOG = 64 * 1024 * 1024
PATTERN = re.compile(rb'(?:gh[pousr]_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+|sk-[A-Za-z0-9_-]+|AIza[A-Za-z0-9_-]+|Bearer[ \t]+[^\s"\x27]+)')

def secret_values():
    return [value.encode() for key, value in os.environ.items()
            if value and re.search(r'TOKEN|KEY|SECRET|PASSWORD|CREDENTIAL', key, re.I)]

def redact(data):
    for value in secret_values():
        data = data.replace(value, b'[REDACTED]')
    return PATTERN.sub(b'[REDACTED]', data)

def safe_prefix_length(data):
    # Retain a potential split secret/pattern until the next chunk resolves it.
    keep = 0
    for value in secret_values() + PREFIXES:
        for length in range(1, min(len(value), len(data) + 1)):
            if data.endswith(value[:length]):
                keep = max(keep, length)
    unfinished = re.search(rb'(?:gh[pousr]_|github_pat_|sk-|AIza|Bearer[ \t]+)[^\s"\x27]*$', data)
    if unfinished:
        keep = max(keep, len(data) - unfinished.start())
    return len(data) - keep

def run(log, status, setup, command):
    # Keep the supervisor alive when tmux delivers control-C to the foreground
    # process group. Exec resets this handled disposition for the child CLI.
    signal.signal(signal.SIGINT, lambda _signal, _frame: None)
    code = 125
    try:
        with open(log, 'xb', buffering=0) as output:
            size = 0
            commands = ([['/bin/sh', '-c', setup]] if setup else []) + [command]
            for argv in commands:
                if os.environ.get('AGENT_DASHBOARD_MODE') == 'interactive' and argv is command:
                    code = subprocess.call(argv)
                    break
                child = subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
                pending = b''
                while True:
                    chunk = os.read(child.stdout.fileno(), 65536)
                    pending += chunk
                    safe = len(pending) if not chunk else safe_prefix_length(pending)
                    if len(pending) > MAX_LINE or size + safe > MAX_LOG:
                        child.kill()
                        output.write(b'agentd: transcript size limit exceeded\n')
                        child.wait()
                        code = 125
                        return
                    clean = redact(pending[:safe])
                    pending = pending[safe:]
                    if clean:
                        output.write(clean)
                        os.write(sys.stdout.fileno(), clean)
                        size += len(clean)
                    if not chunk:
                        break
                code = child.wait()
                if code:
                    break
    except Exception as exc:
        # Exception repr can include command arguments, so report only its class.
        os.write(sys.stderr.fileno(), ('agentd supervisor: ' + type(exc).__name__ + '\n').encode())
    finally:
        with open(status + '.tmp', 'x') as f:
            f.write(str(code if code >= 0 else 128 - code))
        os.replace(status + '.tmp', status)

def follow(log, status):
    while not os.path.exists(log):
        if os.path.exists(status):
            return
        time.sleep(.1)
    with open(log, 'rb', buffering=0) as f:
        while True:
            data = f.read(65536)
            if data:
                os.write(sys.stdout.fileno(), data)
            elif os.path.exists(status):
                return
            else:
                time.sleep(.1)

def hook():
    action = 'deny'
    reason = 'Dashboard approval unavailable; tool execution denied'
    try:
        raw = sys.stdin.buffer.read(1024 * 1024 + 1)
        if len(raw) > 1024 * 1024:
            raise ValueError('hook input exceeds limit')
        event = json.loads(raw)
        tool_input = event.get('tool_input', {})
        payload = {'request_id': str(uuid.uuid4()), 'agent_id': os.environ['AGENT_DASHBOARD_AGENT_ID'],
                   'tool_name': event['tool_name'], 'tool_input': tool_input,
                   'session_id': event.get('session_id', ''),
                   'command': tool_input.get('command', ''),
                   'file_path': tool_input.get('file_path', ''),
                   'diff': tool_input.get('diff', '')}
        url = os.environ['AGENT_DASHBOARD_HOOK_URL']
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme not in ('https', 'http') or parsed.username or parsed.password:
            raise ValueError('invalid dashboard callback URL')
        if os.environ.get('AGENT_DASHBOARD_RUNTIME') == 'docker' and parsed.hostname in ('localhost', '127.0.0.1', '::1'):
            reason = 'Docker approvals require a reachable Tailscale dashboard origin'
            raise ValueError('loopback is inside the container')
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, req, fp, code, msg, headers, newurl):
                raise ValueError('dashboard callback redirects are refused')
        request = urllib.request.Request(url, json.dumps(payload).encode(),
            {'Authorization': 'Bearer ' + os.environ['AGENT_DASHBOARD_HOOK_TOKEN'],
             'Content-Type': 'application/json'}, method='POST')
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        deadline = time.monotonic() + 300
        while True:
            try:
                with opener.open(request, timeout=max(1, deadline - time.monotonic())) as response:
                    result = json.loads(response.read(1024 * 1024))
                break
            except urllib.error.HTTPError as error:
                if error.code < 500 or time.monotonic() >= deadline:
                    raise
            except (urllib.error.URLError, OSError, socket.timeout):
                if time.monotonic() >= deadline:
                    raise
            time.sleep(min(1, max(0, deadline - time.monotonic())))
        if result.get('action') in ('allow', 'deny'):
            action = result['action']
            reason = 'Dashboard user ' + ('approved' if action == 'allow' else 'denied') + ' tool execution'
    except Exception:
        pass  # Missing/expired credentials, timeout, and transport errors fail closed.
    print(json.dumps({'hookSpecificOutput': {'hookEventName': 'PreToolUse',
        'permissionDecision': action, 'permissionDecisionReason': reason}}))

if __name__ == '__main__':
    if len(sys.argv) > 1 and sys.argv[1] == 'hook':
        hook()
        sys.exit(0)
    mode, log, status = sys.argv[1:4]
    if mode == 'run':
        run(log, status, sys.argv[4], sys.argv[5:])
    elif mode == 'follow':
        follow(log, status)
    elif mode == 'container':
        session, setup = sys.argv[4:6]
        os.makedirs(os.path.dirname(log), mode=0o700, exist_ok=True)
        subprocess.run(['tmux', '-L', 'agentd', '-f', '/dev/null', 'new-session', '-d', '-s', session,
                        sys.executable, __file__, 'run', log, status, setup] + sys.argv[6:], check=True)
        follow(log, status)
        with open(status) as f:
            sys.exit(int(f.read()))
