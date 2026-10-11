#!/usr/bin/env python3
"""G2a S5, G2A-05: 20 Flutter hot restarts in a row, driven through `flutter run --machine`.

Each generation of the Dart program re-runs main(): the core must answer already_started (B4,
no double start) and keep delivering events. Events are flowing when each restart happens (the
app emits 200 events per second for 5 s), so a stale callback pointer or a stuck poll reader
would crash or hang the app.

Usage, from spikes/g2a:
  python hot-restart.py --device windows --model callback [--restarts 20] [--out results.json]
"""
import argparse
import json
import os
import queue
import re
import shutil
import subprocess
import sys
import threading
import time

parser = argparse.ArgumentParser()
parser.add_argument('--device', required=True)
parser.add_argument('--model', choices=['callback', 'poll'], default='callback')
parser.add_argument('--restarts', type=int, default=20)
parser.add_argument('--interval', type=float, default=4.0, help='seconds of activity between restarts')
parser.add_argument('--out')
parser.add_argument('--verbose', action='store_true', help='print every message of the flutter tool')
args = parser.parse_args()

flutter = shutil.which('flutter') or shutil.which('flutter.bat')
app_dir = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'app')
run_args = ['run', '-d', args.device, '--machine', '--dart-define=AUTORUN=restart', f'--dart-define=EVENTS={args.model}']
env = dict(os.environ)
if os.name == 'nt':
    # flutter.bat fails when its stdin is a pipe ("Unable to determine engine version"): start
    # the tool the way flutter.bat does, with the SDK's Dart and the tool snapshot.
    root = os.path.dirname(os.path.dirname(os.path.realpath(flutter)))
    cache = os.path.join(root, 'bin', 'cache')
    env['FLUTTER_ROOT'] = root
    cmd = [os.path.join(cache, 'dart-sdk', 'bin', 'dart.exe'),
           '--packages=' + os.path.join(root, 'packages', 'flutter_tools', '.dart_tool', 'package_config.json'),
           os.path.join(cache, 'flutter_tools.snapshot')] + run_args
else:
    cmd = [flutter] + run_args
proc = subprocess.Popen(cmd, cwd=app_dir, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                        stderr=subprocess.STDOUT, text=True, encoding='utf-8', errors='replace', bufsize=1)

messages = queue.Queue()


def reader():
    for line in proc.stdout:
        line = line.strip()
        if line.startswith('[') and line.endswith(']'):
            try:
                for m in json.loads(line):
                    messages.put(m)
                continue
            except json.JSONDecodeError:
                pass
        messages.put({'raw': line})
    messages.put({'eof': True})


threading.Thread(target=reader, daemon=True).start()

app_id = None
logs = []          # (time, text) of the app's G2A lines
responses = {}
ended = None


def pump(timeout):
    """Reads messages for up to timeout seconds; returns False once the app or the tool is gone."""
    global app_id, ended
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            m = messages.get(timeout=max(0.05, deadline - time.time()))
        except queue.Empty:
            return True
        if m.get('eof'):
            ended = ended or 'flutter tool exited'
            return False
        ev, params = m.get('event'), m.get('params', {})
        if args.verbose and ev != 'app.progress' and 'G2A_RESTART_ALIVE' not in m.get('raw', ''):
            print('<<', json.dumps(m)[:300], flush=True)
        if ev == 'app.started':
            app_id = params.get('appId')
        elif ev == 'app.log':
            text = params.get('log', '')
            if 'G2A' in text:
                logs.append((time.time(), text))
        elif ev == 'app.start' and not app_id:
            app_id = params.get('appId')
        elif ev == 'app.stop':
            ended = ended or f"app.stop {params.get('error', '')}".strip()
            return False
        elif 'id' in m and ('result' in m or 'error' in m):
            responses[m['id']] = m
        elif 'raw' in m and 'G2A' in m['raw']:  # desktop: the app's stdout arrives as plain lines
            logs.append((time.time(), m['raw']))
        elif 'raw' in m and re.search(r'Lost connection|crash|Exception', m['raw'], re.I):
            logs.append((time.time(), 'TOOL ' + m['raw']))
    return True


def send(method, params, rid):
    proc.stdin.write(json.dumps([{'id': rid, 'method': method, 'params': params}]) + '\n')
    proc.stdin.flush()


def wait_for(pred, timeout):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if pred():
            return True
        if not pump(0.2):
            return pred()
    return pred()


def generations():
    """One entry per G2A_RESTART line, with the last ALIVE line that follows it."""
    gens = []
    for t, text in logs:
        if 'G2A_RESTART start=' in text:
            m = re.search(r'already_started: (\w+).*?start_calls: (\d+)', text)
            gens.append({'t': t, 'already_started': m and m.group(1) == 'true',
                         'start_calls': m and int(m.group(2)), 'alive': None})
        elif 'G2A_RESTART_ALIVE' in text and gens:
            m = re.search(r'events=(\d+) goroutines=(\d+) sys_mb=([\d.]+) rss_mb=(\d+)', text)
            if m:
                gens[-1]['alive'] = {'events': int(m.group(1)), 'goroutines': int(m.group(2)),
                                     'sys_mb': float(m.group(3)), 'rss_mb': int(m.group(4))}
    return gens


t0 = time.time()
ok = wait_for(lambda: app_id and len(generations()) >= 1, 900)
print(f'app started in {time.time() - t0:.0f} s, appId={app_id}', flush=True)
restart_ms = []
if ok:
    wait_for(lambda: False, args.interval)
    for i in range(1, args.restarts + 1):
        n = len(generations())
        t = time.time()
        send('app.restart', {'appId': app_id, 'fullRestart': True, 'pause': False, 'reason': 'g2a'}, i)
        got = wait_for(lambda: i in responses and len(generations()) > n, 120)
        restart_ms.append(round((time.time() - t) * 1000))
        wait_for(lambda: False, args.interval)  # events flow; the next restart cuts them
        g = generations()[-1] if generations() else {}
        print(f"restart {i:2d}: {'ok' if got else 'FAILED'} {restart_ms[-1]} ms, "
              f"already_started={g.get('already_started')} start_calls={g.get('start_calls')} alive={g.get('alive')}",
              flush=True)
        if not got or ended:
            break
    if not ended:
        send('app.stop', {'appId': app_id}, 999)
        wait_for(lambda: ended is not None, 60)

gens = generations()
later = gens[1:]
alive = [g['alive'] for g in gens if g['alive']]
result = {
    'device': args.device, 'model': args.model, 'restarts_requested': args.restarts,
    'restarts_done': len(gens) - 1 if gens else 0,
    'ended': ended,
    'restart_ms': restart_ms,
    'first_start_already_started': gens[0]['already_started'] if gens else None,
    'later_all_already_started': all(g['already_started'] for g in later) if later else None,
    'start_calls_sequence': [g['start_calls'] for g in gens],
    'events_per_generation': [g['alive'] and g['alive']['events'] for g in gens],
    'goroutines': [a['goroutines'] for a in alive],
    'go_sys_mb': [a['sys_mb'] for a in alive],
    'rss_mb': [a['rss_mb'] for a in alive],
    'tool_messages': [text for _, text in logs if text.startswith('TOOL')][:20],
}
result['pass'] = bool(
    gens and result['restarts_done'] == args.restarts and result['first_start_already_started'] is False
    and result['later_all_already_started']
    and result['start_calls_sequence'] == list(range(1, args.restarts + 2))
    and all(e and e > 0 for e in result['events_per_generation'])
    and (ended is None or ended.startswith('app.stop')))
print(json.dumps({k: v for k, v in result.items() if k != 'tool_messages'}), flush=True)
if args.out:
    with open(args.out, 'w', encoding='utf-8') as f:
        json.dump(result, f, indent=2)
try:
    proc.wait(timeout=30)
except subprocess.TimeoutExpired:
    proc.kill()
sys.exit(0 if result['pass'] else 1)
