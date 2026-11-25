#!/usr/bin/env python3
"""Offline dense channel: qwen3-embedding:0.6b via the local ollama server.
Files at each dev case's base (blob-hash cache), cosine top-24, RRF onto the
shipped 40ea5c869 list. usage: dense.py <bt|id> [embed|eval]"""
import json, os, subprocess, sys, time, urllib.request, collections, struct, math
from array import array
import operator, statistics
def unit(v):
    n = math.sqrt(sum(x*x for x in v)); return [x/n for x in v]
def save(p, v): open(p, 'wb').write(array('f', v).tobytes())
def load(p):
    a = array('f'); a.frombytes(open(p, 'rb').read()); return a

S = os.path.dirname(os.path.abspath(__file__)); SP = os.path.dirname(S)
tag, mode = sys.argv[1], sys.argv[2]
repo = '/Users/ben/Documents/GitHub/' + ('BenTech' if tag == 'bt' else 'idiom')
cases = [c for c in json.load(open(f"{SP}/idiom-res/testdata/localization/eval/{'bentech' if tag=='bt' else 'idiom'}-cases.json"))['cases'] if c['split'] == 'dev']
res = {c['id']: c for c in json.load(open(f"{SP}/loc1/exp/{'F-bt-bt' if tag=='bt' else 'F-id-id'}.json"))['cases']}
ie = {os.path.splitext(p)[1] for c in res.values() for p in c['ranked']}
MODEL = 'qwen3-embedding:0.6b'; MAXCH = 6000
cache_dir = f'{S}/emb-{tag}'; os.makedirs(cache_dir, exist_ok=True)

def embed(texts):
    req = urllib.request.Request('http://localhost:11434/api/embed', data=json.dumps(
        {'model': MODEL, 'input': texts, 'truncate': True, 'options': {'num_ctx': 2048}}).encode(),
        headers={'Content-Type': 'application/json'})
    return json.load(urllib.request.urlopen(req, timeout=600))['embeddings']

def tree(base):
    out = subprocess.run(['git', '-C', repo, 'ls-tree', '-r', '-l', base], capture_output=True, text=True).stdout
    fs = []
    for line in out.splitlines():
        meta, path = line.split('\t', 1); _, _, h, size = meta.split()
        if os.path.splitext(path)[1] in ie and size != '-' and int(size) < 400000:
            fs.append((path, h))
    return fs

def key(path, h):  # path is part of the text, so key on both
    import hashlib; return hashlib.sha1(f'{path}\0{h}'.encode()).hexdigest()

if mode == 'embed':
    todo = {}
    for c in cases:
        for p, h in tree(c['base']):
            k = key(p, h)
            if not os.path.exists(f'{cache_dir}/{k}.f32'):
                todo[k] = (p, h)
    print('to embed', len(todo), flush=True)
    t = time.time(); items = list(todo.items()); ntok = 0
    for i in range(0, len(items), 16):
        batch = items[i:i + 16]
        texts = []
        for k, (p, h) in batch:
            body = subprocess.run(['git', '-C', repo, 'cat-file', 'blob', h], capture_output=True).stdout.decode('utf-8', 'replace')
            texts.append(f'{p}\n{body[:MAXCH]}')
        for (k, _), v in zip(batch, embed(texts)):
            save(f'{cache_dir}/{k}.f32', v)
        if i % 800 == 0:
            print(i, round(time.time() - t, 1), 's', flush=True)
    print('embed seconds', round(time.time() - t, 1), 'docs', len(items))
    sys.exit()

sys.argv = ['x', tag]
src = open(f'{S}/cochange.py').read().split('allow_all = lambda')[0]
g = {'__file__': f'{S}/cochange.py'}; exec(compile(src, 'cc', 'exec'), g)
rrf, metrics = g['rrf'], g['metrics']
INSTR = 'Instruct: Given a software change request, retrieve the source files that must be edited to implement it\nQuery: '
lat = []; lists = {}
for c in cases:
    fs = tree(c['base'])
    M = [unit(load(f'{cache_dir}/{key(p, h)}.f32')) for p, h in fs]
    t = time.time()
    q = unit(embed([INSTR + c['query']])[0])
    lat.append((time.time() - t) * 1000)  # query embed only; scan cost reported separately
    s = [sum(map(operator.mul, m, q)) for m in M]; order = sorted(range(len(fs)), key=lambda i: (-s[i], fs[i][0]))[:24]
    lists[c['id']] = [fs[i][0] for i in order]
def ev(fn):
    tot = [0.0, 0.0, 0.0]
    for c in cases:
        tot = [a + b for a, b in zip(tot, metrics(fn(c), c['gold']))]
    return [x / len(cases) for x in tot]
b = ev(lambda c: res[c['id']]['ranked'])
def r24(L):
    return statistics.mean([len(set(c['gold']) & set(L(c)[:24])) / len(set(c['gold'])) for c in cases])
print(f"{tag} dev n={len(cases)} recall@24 shipped {r24(lambda c: res[c['id']]['ranked']):.3f} dense {r24(lambda c: lists[c['id']]):.3f}")
print(f'{tag} shipped              rc8 {b[0]:.3f} hit {b[1]:.3f} mrr {b[2]:.3f}')
for name, fn in [('dense alone', lambda c: lists[c['id']]),
                 ('rrf w=0.5', lambda c: rrf([res[c['id']]['ranked'], lists[c['id']]], [1, .5])),
                 ('rrf w=1.0', lambda c: rrf([res[c['id']]['ranked'], lists[c['id']]], [1, 1]))]:
    m = ev(fn)
    print(f'{tag} {name:20s} rc8 {m[0]:.3f} ({m[0]-b[0]:+.3f}) hit {m[1]:.3f} ({m[1]-b[1]:+.3f}) mrr {m[2]:.3f} ({m[2]-b[2]:+.3f})')
print(f'{tag} query latency ms p50 {statistics.median(lat):.0f} max {max(lat):.0f} (query embed + {len(fs)}x1024 dot)')
