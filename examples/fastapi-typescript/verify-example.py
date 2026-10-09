#!/usr/bin/env python3
"""Explicit executable fixture qualification in an ephemeral committed repo."""
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

binary = str(pathlib.Path(sys.argv[1]).resolve())
with tempfile.TemporaryDirectory(prefix='radar-fastapi-qualified-') as tmp:
    root = pathlib.Path(tmp)/'repo'
    shutil.copytree(pathlib.Path(__file__).resolve().parent, root, ignore=shutil.ignore_patterns('__pycache__', '*.pyc', '.radar', '.git'))
    for args in [('init','-q'),('config','user.name','Fixture'),('config','user.email','fixture@example.invalid'),('add','.'),('commit','-qm','qualified source')]:
        subprocess.run(['git',*args],cwd=root,check=True,stdout=subprocess.DEVNULL)
    policy=pathlib.Path(tmp)/'policy.json'
    policy.write_text('{"version":1,"require":["textual_merge","test_selection","integration_execution"]}')
    result=subprocess.run([binary,'merge-check','--root',str(root),'--base','HEAD','--branches','HEAD','--verify','--allow-execution','--suite','full','--timeout','30s','--policy',str(policy),'--json'],capture_output=True,text=True)
    report=json.loads(result.stdout)
    assert result.returncode==0 and report['gate']['verdict']=='pass', report
    observations=report['executions']
    assert len(observations)==3 and all(o['observation']['tests_run']==1 and o['status']=='passed' for o in observations), observations
    print(json.dumps({'verdict':report['gate']['verdict'],'observed_commands':[{'cwd':o['cwd'],'command':o['command'],'harness':o['observation']['harness'],'tests_run':o['observation']['tests_run'],'source':o['source_after_execution']} for o in observations]},indent=2))
