#!/usr/bin/env python3
"""Exercise pinned-source acquisition with an isolated local Git fixture."""
import json, os, subprocess, tempfile, unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parent.parent
class UpstreamTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='clash-upstream-test-')
        self.base=Path(self.temp.name);self.source=self.base/'shared';self.source.mkdir()
        self.git('init','-q','-b','master')
        self.git('config','user.email','fixture@example.invalid');self.git('config','user.name','Fixture')
        (self.source/'VERSION').write_text('1.0.0\n');self.git('add','VERSION');self.git('commit','-qm','first')
        self.first=self.git('rev-parse','HEAD').strip()
        (self.source/'VERSION').write_text('1.0.1\n');self.git('commit','-qam','second')
        self.second=self.git('rev-parse','HEAD').strip();self.git('checkout','-q',self.first)
        self.lock=self.base/'upstream.lock';self.cache=self.base/'cache.git'
        self.write_lock()
    def tearDown(self):self.temp.cleanup()
    def git(self,*args):return subprocess.check_output(['git','-C',str(self.source),*args],text=True)
    def write_lock(self,**changes):
        data=dict(schema=1,repository='https://github.com/chenpingonline/Clash-Manager.git',version='1.0.0',commit=self.first)
        data.update(changes);self.lock.write_text(json.dumps(data))
    def fetch(self,local=True,offline=False):
        env=os.environ.copy()
        for key in ['CLASH_SHARED_SOURCE','CLASH_OFFLINE']:env.pop(key,None)
        env.update(CLASH_UPSTREAM_LOCK=str(self.lock),CLASH_UPSTREAM_CACHE=str(self.cache))
        if local:env['CLASH_SHARED_SOURCE']=str(self.source)
        if offline:env['CLASH_OFFLINE']='1'
        return subprocess.run([str(ROOT/'scripts/fetch-upstream.sh')],env=env,capture_output=True,text=True)
    def test_exact_commit_and_offline_cache(self):
        result=self.fetch();self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(result.stdout.strip(),str(self.cache))
        self.git('checkout','-q',self.second)
        result=self.fetch(local=False,offline=True);self.assertEqual(result.returncode,0,result.stderr)
        version=subprocess.check_output(['git','--git-dir='+str(self.cache),'show',self.first+':VERSION'],text=True)
        self.assertEqual(version.strip(),'1.0.0')
    def test_missing_offline_source_fails(self):
        result=self.fetch(local=False,offline=True)
        self.assertNotEqual(result.returncode,0);self.assertIn('not cached',result.stderr)
    def test_version_mismatch_fails(self):
        self.assertEqual(self.fetch().returncode,0)
        self.write_lock(version='9.9.9');result=self.fetch(local=False,offline=True)
        self.assertNotEqual(result.returncode,0);self.assertIn('VERSION',result.stderr)
    def test_branch_or_partial_commit_is_rejected_before_fetch(self):
        for commit in ['master',self.first[:12]]:
            self.write_lock(commit=commit);result=self.fetch(local=False,offline=True)
            self.assertNotEqual(result.returncode,0);self.assertIn('Invalid upstream.lock',result.stderr)
        self.assertFalse(self.cache.exists())
    def test_dirty_or_wrong_local_source_fails(self):
        (self.source/'VERSION').write_text('dirty\n');result=self.fetch()
        self.assertNotEqual(result.returncode,0);self.assertIn('uncommitted',result.stderr)
        self.git('checkout','--','VERSION');self.git('checkout','-q',self.second);result=self.fetch()
        self.assertNotEqual(result.returncode,0);self.assertIn('pinned commit',result.stderr)
    def test_invalid_repository_is_rejected(self):
        self.write_lock(repository='--upload-pack=malicious');result=self.fetch()
        self.assertNotEqual(result.returncode,0);self.assertIn('Invalid upstream.lock',result.stderr)
        self.assertFalse(self.cache.exists())
if __name__=='__main__':unittest.main()
