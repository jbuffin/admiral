#!/usr/bin/env python3
# A stand-in for Claude Code's AskUserQuestion menu: draws tabs, option rows, an Other row, a Submit row and a review screen,
# and logs every key it reads to argv[2]. argv[1] is the JSON list of {text, labels, multi}.
import json, os, signal, sys, tty, termios, select

qs = json.loads(sys.argv[1]); log = open(sys.argv[2], 'ab', buffering=0)
n = len(qs); tab = 0; cur = [0]*n; tick = [[False]*len(q['labels']) for q in qs]; other = ['']*n; done = [False]*n
review = False; finished = False
def rows(i): return len(qs[i]['labels']) + (2 if qs[i]['multi'] else 1)
def ans(i):
    p = [l for l, t in zip(qs[i]['labels'], tick[i]) if t]
    if other[i]: p.append(other[i])
    return ', '.join(p)
def draw():
    out = ['❯ earlier prompt', '────────']
    single = n == 1 and not qs[0]['multi']
    if review:
        out += ['←  ' + '  '.join('☒ Q' for _ in qs) + '  ✔ Submit  →', '', 'Review your answers', '']
        for i, q in enumerate(qs): out += [' ● ' + q['text'], '   → ' + ans(i)]
        out += ['', 'Ready to submit your answers?', '', '❯ 1. Submit answers', '  2. Cancel']
    elif finished:
        out += ['submitted']
    else:
        if single: out += [' ☐ Q', '']
        else: out += ['←  ' + '  '.join(('☒' if done[i] else '☐') + ' Q' for i in range(n)) + '  ✔ Submit  →', '']
        q = qs[tab]; k = len(q['labels']); c = lambda r: '❯' if cur[tab] == r else ' '
        out += [q['text'], '']
        for j, l in enumerate(q['labels']):
            if q['multi']: out += [f"{c(j)} {j+1}. [{'✔' if tick[tab][j] else ' '}] {l}"]
            else: out += [f"{c(j)} {j+1}. {l}" + (' ✔' if tick[tab][j] else '')]
            out += ['     a description']
        if q['multi']: out += [f"{c(k)} {k+1}. [{'✔' if other[tab] else ' '}] {other[tab] or 'Type something'}"]
        else: out += [f"{c(k)} {k+1}. {other[tab] or 'Type something.'}"]
        if q['multi']: out += [f"{c(k+1)}    " + ('Submit' if tab == n-1 else 'Next')]
        out += ['────────', '  Chat about this', '', 'Enter to select']
    # Like Claude, keep only the bottom of a frame taller than the pane, so the tabs and question scroll off a short one.
    out = out[-(os.get_terminal_size(1).lines - 1):]
    sys.stdout.write('\x1b[2J\x1b[H' + '\r\n'.join(out) + '\r\n'); sys.stdout.flush()
def nxt():
    global tab, review, finished
    done[tab] = True
    if tab < n-1: tab += 1
    elif n > 1 or qs[0]['multi']: review = True
    else: finished = True
tty.setraw(0); signal.signal(signal.SIGWINCH, lambda *_: draw()); draw()
buf = b''
while True:
    if not select.select([0], [], [], 60)[0]: break
    buf += os.read(0, 4096)
    if finished: buf = b''
    while buf and not finished:
        if buf.startswith(b'\x1b[B'): key, buf = '\x1b[B', buf[3:]
        else: key, buf = buf[:1].decode('utf-8', 'replace'), buf[1:]
        log.write(key.encode())
        if review:
            if key == '1': review = False; finished = True
        else:
            q = qs[tab]; k = len(q['labels']); i = tab
            if key == '\x1b[B': cur[i] = (cur[i]+1) % rows(i)
            elif key == '\r':
                if q['multi'] and cur[i] == k+1: nxt()
                elif q['multi'] and cur[i] < k: tick[i][cur[i]] = not tick[i][cur[i]]
                elif not q['multi'] and cur[i] == k and other[i]: nxt()
                elif not q['multi'] and cur[i] < k: tick[i][cur[i]] = True; nxt()
            elif cur[i] == k: other[i] += key
            elif not q['multi'] and key.isdigit() and 1 <= int(key) <= k+1:
                d = int(key)
                if d == k+1: cur[i] = k
                else: tick[i][d-1] = True; nxt()
        draw()
        if finished: buf = b''
