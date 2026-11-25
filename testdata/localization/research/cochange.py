#!/usr/bin/env python3
"""Offline co-change / commit-history channel, fused by RRF onto the shipped
40ea5c869 ranked list. Dev split only. History = non-merge commits that are
ancestors of each case's base (no leakage from the case's own change).

usage: cochange.py <bt|id>
"""
import json, math, re, subprocess, sys, os, collections, itertools

S = os.path.dirname(os.path.abspath(__file__))
SP = os.path.dirname(S)
tag = sys.argv[1]
repo = '/Users/ben/Documents/GitHub/' + ('BenTech' if tag == 'bt' else 'idiom')
cases_f = SP + '/idiom-res/testdata/localization/eval/' + ('bentech' if tag == 'bt' else 'idiom') + '-cases.json'
res_f = SP + '/loc1/exp/' + ('F-bt-bt.json' if tag == 'bt' else 'F-id-id.json')

def git(*a):
    return subprocess.run(['git', '-C', repo, *a], capture_output=True, text=True, check=True).stdout

# --- history ---
commits = {}
raw = open(f"{S}/log-{'BenTech' if tag == 'bt' else 'idiom'}.txt").read().split('\x00')[1:]
for blk in raw:
    head, _, rest = blk.partition('\x01')
    h, ts = head.split()
    subj, _, rest = rest.partition('\x01')
    body, _, files = rest.partition('\x02')
    fl = [f for f in files.split('\n') if f.strip()]
    commits[h] = (int(ts), subj + ' ' + body, fl)

TOK = re.compile(r'[A-Za-z][a-z]+|[A-Z]+(?![a-z])|\d+')
STOP = set('the a an and or of to in for on with by from at is are be as it this that into not no per one two its'.split())
def toks(s):
    out = []
    for w in re.split(r'[^A-Za-z0-9]+', s):
        for t in TOK.findall(w):
            t = t.lower()
            if len(t) > 1 and t not in STOP:
                out.append(t)
    return out

def rrf(lists, weights, k=60, depth=24):
    sc = collections.defaultdict(float)
    for L, w in zip(lists, weights):
        for r, f in enumerate(L[:depth]):
            sc[f] += w / (k + r + 1)
    return sorted(sc, key=lambda f: (-sc[f], f))

def metrics(ranked, gold, k=8):
    top = []
    for p in ranked:
        if p not in top:
            top.append(p)
        if len(top) == k:
            break
    g = set(gold); hits = [i for i, p in enumerate(top) if p in g]
    rc = len(hits) / min(k, len(g))
    return rc, (1.0 if hits else 0.0), (1 / (hits[0] + 1) if hits else 0.0)

cases = {c['id']: c for c in json.load(open(cases_f))['cases'] if c['split'] == 'dev'}
res = {c['id']: c for c in json.load(open(res_f))['cases']}
indexed_ext = {os.path.splitext(p)[1] for c in res.values() for p in c['ranked']}

prep = []
for cid, c in cases.items():
    base = c['base']
    anc = set(git('rev-list', '--no-merges', base).split())
    tree = set(git('ls-tree', '-r', '--name-only', base).split('\n'))
    t0 = int(git('show', '-s', '--format=%ct', base).strip())
    hist = [commits[h] for h in anc if h in commits]
    prep.append((c, res[cid], hist, tree, t0))

# sanity: recomputed metrics on the shipped list equal stored ones
for c, r, *_ in prep:
    m = metrics(r['ranked'], c['gold'])
    assert abs(m[0] - r['metrics_k8']['recall_capped']) < 1e-9 and abs(m[2] - r['metrics_k8']['mrr']) < 1e-9, c['id']

def cochange_list(ranked, hist, tree, t0, seeds, half_days, maxc, allowed):
    """ROSE-style: partners of the top seeds, confidence-normalised, recency-decayed,
    large commits down-weighted."""
    sd = ranked[:seeds]
    sw = {s: 1 / (i + 1) for i, s in enumerate(sd)}
    sup = collections.Counter(); co = collections.defaultdict(float)
    for ts, _, fl in hist:
        if len(fl) > maxc:
            continue
        d = 0.5 ** (max(0, t0 - ts) / 86400 / half_days)
        fs = set(fl)
        inter = fs & sw.keys()
        if not inter:
            continue
        for s in inter:
            sup[s] += d
            for f in fs:
                if f != s:
                    co[(s, f)] += d / math.log2(1 + len(fs))
    sc = collections.defaultdict(float)
    for (s, f), v in co.items():
        if f in tree and allowed(f):
            sc[f] += sw[s] * v / (sup[s] + 1.0)
    return sorted(sc, key=lambda f: (-sc[f], f))

def message_list(query, hist, tree, t0, topc, half_days, maxc, allowed):
    """Commit-message BM25: past commits whose messages match the task, then
    the files they touched (Locus/similar-report style history channel)."""
    docs = [(toks(m), fl, ts) for ts, m, fl in hist if len(fl) <= maxc]
    N = len(docs) or 1
    df = collections.Counter(t for d, _, _ in docs for t in set(d))
    avg = sum(len(d) for d, _, _ in docs) / N
    q = set(toks(query))
    scored = []
    for d, fl, ts in docs:
        tf = collections.Counter(d); s = 0.0
        for t in q:
            if tf[t]:
                idf = math.log(1 + (N - df[t] + 0.5) / (df[t] + 0.5))
                s += idf * tf[t] * 2.2 / (tf[t] + 1.2 * (0.25 + 0.75 * len(d) / avg))
        if s > 0:
            dec = 0.5 ** (max(0, t0 - ts) / 86400 / half_days)
            scored.append((s * dec, fl))
    scored.sort(key=lambda x: -x[0])
    sc = collections.defaultdict(float)
    for s, fl in scored[:topc]:
        for f in fl:
            if f in tree and allowed(f):
                sc[f] += s / math.log2(1 + len(fl))
    return sorted(sc, key=lambda f: (-sc[f], f))

def evaluate(fn):
    tot = [0, 0, 0]
    for c, r, hist, tree, t0 in prep:
        m = metrics(fn(c, r, hist, tree, t0), c['gold'])
        for i in range(3):
            tot[i] += m[i]
    n = len(prep)
    return tuple(x / n for x in tot)

base = evaluate(lambda c, r, *a: r['ranked'])
print(f'{tag} dev n={len(prep)} shipped 40ea5c869: rc8 {base[0]:.3f} hit {base[1]:.3f} mrr {base[2]:.3f}')
allow_all = lambda f: True
allow_idx = lambda f: os.path.splitext(f)[1] in indexed_ext
rows = []
for allowed_name, allowed in [('idx', allow_idx), ('all', allow_all)]:
    for seeds, half, maxc, w in itertools.product([3, 5], [30, 90], [20, 50], [0.5, 1.0]):
        m = evaluate(lambda c, r, h, t, t0: rrf([r['ranked'], cochange_list(r['ranked'], h, t, t0, seeds, half, maxc, allowed)], [1, w]))
        rows.append(('cochange', allowed_name, f'seeds={seeds} half={half}d maxc={maxc} w={w}', m))
    for topc, half, maxc, w in itertools.product([10, 30], [30, 365], [20, 50], [0.5, 1.0]):
        m = evaluate(lambda c, r, h, t, t0: rrf([r['ranked'], message_list(c['query'], h, t, t0, topc, half, maxc, allowed)], [1, w]))
        rows.append(('message', allowed_name, f'topc={topc} half={half}d maxc={maxc} w={w}', m))
    # both history channels together at one fixed setting
    m = evaluate(lambda c, r, h, t, t0: rrf([r['ranked'], cochange_list(r['ranked'], h, t, t0, 5, 90, 50, allowed),
                                            message_list(c['query'], h, t, t0, 30, 365, 50, allowed)], [1, 0.5, 0.5]))
    rows.append(('both', allowed_name, 'cc(5,90,50) msg(30,365,50) w=.5/.5', m))
for kind, al, cfg, m in rows:
    print(f'{kind:8s} {al:3s} {cfg:38s} rc8 {m[0]:.3f} ({m[0]-base[0]:+.3f}) hit {m[1]:.3f} ({m[1]-base[1]:+.3f}) mrr {m[2]:.3f} ({m[2]-base[2]:+.3f})')
