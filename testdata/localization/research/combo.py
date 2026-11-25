import sys,os
tag=sys.argv[1]
src=open('dense.py').read().replace("tag, mode = sys.argv[1], sys.argv[2]","tag, mode = sys.argv[1], 'eval'")
src=src.split("def ev(fn):")[0]
g={'__file__':os.path.abspath('dense.py'),'__name__':'x'}; sys.argv=['x',tag,'eval']; exec(compile(src,'d','exec'),g)
cc=g['g']; lists=g['lists']; res=g['res']; cases=g['cases']; rrf=g['rrf']; metrics=g['metrics']
prep={c['id']:(h,t,t0) for c,r,h,t,t0 in cc['prep']}
ie=cc['indexed_ext']; al=lambda f: os.path.splitext(f)[1] in ie
def ev(fn):
    tot=[0,0,0]
    for c in cases: tot=[a+b for a,b in zip(tot,metrics(fn(c),c['gold']))]
    return [x/len(cases) for x in tot]
b=ev(lambda c: res[c['id']]['ranked'])
MS=lambda c: cc['message_list'](c['query'],*prep[c['id']],30,365,50,al)
for name,fn in [('dense w1 + msg w.25', lambda c: rrf([res[c['id']]['ranked'],lists[c['id']],MS(c)],[1,1,.25])),
                ('dense w.5 + msg w.25', lambda c: rrf([res[c['id']]['ranked'],lists[c['id']],MS(c)],[1,.5,.25]))]:
    m=ev(fn); print(f'{tag} {name:22s} rc8 {m[0]:.3f} ({m[0]-b[0]:+.3f}) hit {m[1]:.3f} ({m[1]-b[1]:+.3f}) mrr {m[2]:.3f} ({m[2]-b[2]:+.3f})')
