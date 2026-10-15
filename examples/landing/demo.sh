#!/usr/bin/env bash
# Four committed branches: a stack, a conflicting peer and independent docs.
# No repository test or structural driver is executed by this static demo.
set -euo pipefail
radar_bin=$(realpath "${1:?usage: demo.sh /path/to/radar}")
python3 - "$radar_bin" <<'PY'
import json, os, pathlib, subprocess, sys, tempfile
binary=sys.argv[1]
with tempfile.TemporaryDirectory(prefix='radar-landing-demo-') as directory:
    root=pathlib.Path(directory)/'repo';root.mkdir()
    env=os.environ.copy();env.update(RADAR_CONFIG_DIR=directory+'/config',RADAR_STATE_DIR=directory+'/state',NO_COLOR='1')
    def git(*args):
        return subprocess.check_output(['git','-C',str(root),'-c','user.name=Demo','-c','user.email=demo@example.invalid','-c','core.hooksPath='+os.devnull,*args],text=True).strip()
    def put(name,text):
        p=root/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
    def commit(message):
        git('add','.');git('commit','-qm',message)
    def radar(*args,exit=0,machine=True):
        argv=[binary,'--root',str(root),*args]+(['--json'] if machine else [])
        p=subprocess.run(argv,env=env,text=True,capture_output=True)
        assert p.returncode==exit,(argv,p.returncode,p.stdout,p.stderr)
        return json.loads(p.stdout) if machine else p.stdout.strip()
    git('init','-qb','main')
    put('pricing.py','def charge():\n    return 1\n')
    for name in ['test_price.py','test_checkout.py']:
        put(name,'import unittest\nfrom pricing import charge\nclass Price(unittest.TestCase):\n def test_positive(self): self.assertGreater(charge(),0)\n')
    put('db/env.py','# Alembic marker; not executed\n')
    put('db/versions/r0.py',"revision = 'r0'\ndown_revision = None\n")
    commit('base')
    git('checkout','-qb','agent-db')
    put('db/versions/r1.py',"revision = 'r1'\ndown_revision = 'r0'\n");commit('schema')
    git('checkout','-qb','agent-api')
    put('pricing.py','def charge():\n    return 2\n');commit('API pricing')
    git('checkout','-qb','agent-client','main')
    put('pricing.py','def charge():\n    return 3\n');commit('client pricing')
    git('checkout','-qb','agent-docs','main')
    put('README.md','Independent documentation.\n');commit('docs')
    git('checkout','-q','main')
    branches=['agent-db','agent-api','agent-client','agent-docs']
    plan=radar('land',*branches,exit=1)
    assert 'agent-docs' in plan['landing']['independent'],plan
    assert ['agent-db','agent-api'] in plan['landing']['chains'],plan
    brief=radar('land','agent-api','agent-client','--conflict-brief',exit=1)
    f=next(x for x in brief['conflict_brief']['files'] if x['path']=='pricing.py')
    assert f['hunks'] and 'charge' in f['symbols'] and len(f['tests'])==2,f
    git('checkout','-q','agent-client')
    put('pricing.py','def charge():\n    return 2\n');commit('reconcile shared pricing')
    git('checkout','-q','main')
    before=git('branch','--format=%(refname):%(objectname)')
    source=git('status','--porcelain')
    landed=radar('land',*branches,'--apply')
    assert landed['landing_apply']['published'] and landed['landing_apply']['objects_imported'],landed
    assert git('branch','--format=%(refname):%(objectname)')==before
    assert git('status','--porcelain')==source
    ref=landed['integration_ref']['ref'];assert ref.startswith('refs/radar/integration/')
    trailer=radar('gate',*branches,'--trailer',machine=False)
    assert trailer.startswith('Radar-Verified:') and 'tests(not_run)' in trailer,trailer
    shown=radar('show',trailer.split('run=',1)[1])
    assert shown['candidate_tree']==landed['candidate_tree'],shown
    print('PASS: four-branch order, stack, independent docs, symbol/two-test conflict brief, private ref and historical trailer.')
    print('No repository tests ran; landing estimates are not measured rebase counts.')
PY
