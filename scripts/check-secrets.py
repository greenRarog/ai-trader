#!/usr/bin/env python3
"""Inspect staged files and reachable commits; never print matching secret data."""
import pathlib,re,subprocess,sys
root=pathlib.Path(__file__).resolve().parent.parent

def git(*args):return subprocess.check_output(['git','-C',str(root),*args])
known=[]
env_file=root/'.env'
if env_file.exists():
    for line in env_file.read_text().splitlines():
        key,sep,value=line.partition('=');value=value.strip().strip('\"\'')
        if sep and any(word in key.upper() for word in ('TOKEN','PASSWORD','API_KEY','DATABASE_URL')) and len(value)>=12 and value not in ('replace-with-a-random-local-password','local_test_only','local_restore_check_only'):
            known.append(value.encode())
patterns=[re.compile(rb't\.[A-Za-z0-9_-]{40,}'),re.compile(rb'sk-(?:proj-)?[A-Za-z0-9_-]{30,}'),re.compile(rb'\b[0-9]{7,}:[A-Za-z0-9_-]{30,}'),re.compile(rb'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----')]
seen=set();failed=[]
def inspect(path,oid):
    p=pathlib.PurePosixPath(path)
    if p.name=='.env' or p.name.startswith('.env.') and p.name!='.env.example' or any(part in ('.runtime','.state','.tools','.backups','bin') for part in p.parts) or p.suffix in ('.dump','.bundle'):
        failed.append((path,'private/generated file is tracked'));return
    if oid in seen:return
    seen.add(oid);data=git('cat-file','blob',oid)
    if any(value in data for value in known) or any(pattern.search(data) for pattern in patterns):failed.append((path,'possible credential found'))
for entry in git('ls-files','--stage','-z').split(b'\0'):
    if not entry:continue
    meta,path=entry.split(b'\t',1);mode,oid,stage=meta.split();name=path.decode()
    if stage!=b'0':failed.append((name,'unresolved merge'));continue
    inspect(name,oid.decode())
for commit in git('rev-list','--all').splitlines():
    for entry in git('ls-tree','-rz',commit.decode()).split(b'\0'):
        if not entry:continue
        meta,path=entry.split(b'\t',1);mode,kind,oid=meta.split()
        if kind==b'blob':inspect(path.decode(),oid.decode())
if failed:
    for path,reason in sorted(set(failed)):print(f'{path}: {reason}',file=sys.stderr)
    sys.exit(1)
print(f'Secret check OK: {len(seen)} distinct staged/history blobs; no credential contents printed.')
