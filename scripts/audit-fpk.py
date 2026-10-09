#!/usr/bin/env python3
"""Audit all three FPKs against the locked shared source and fnOS manifest."""
import gzip, hashlib, json, re, struct, subprocess, tarfile, tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parent.parent
def meta(path):return dict(tuple(x.strip() for x in line.split('=',1)) for line in path.read_text().splitlines() if '=' in line)
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def extract(path,directory):
    with tarfile.open(path) as archive:
        for member in archive.getmembers():
            assert not member.name.startswith('/') and '..' not in Path(member.name).parts
            assert not Path(member.name).name.startswith('._') and '__MACOSX' not in member.name
        archive.extractall(directory,filter='data')
version=meta(ROOT/'fpk/manifest')['version']
assert re.search(r'^## \[([^]]+)\]',(ROOT/'CHANGELOG.md').read_text(),re.M)[1]==version
lock=json.loads((ROOT/'upstream.lock').read_text())
cache=subprocess.check_output([str(ROOT/'scripts/fetch-upstream.sh')],text=True).strip()
def source(path):return subprocess.check_output(['git','--git-dir='+cache,'show',lock['commit']+':'+path])
assert source('VERSION').decode().strip()==lock['version']
records=[]
for architecture,platform,arches in [('x86_64','x86',['x86_64']),('arm64','arm',['arm64']),('all','all',['x86_64','arm64'])]:
    package=ROOT/'dist'/f'Clash for fnos_{version}_{architecture}.fpk'
    assert package.with_suffix('.fpk.sha256').read_text().split()[0]==sha(package)
    with tempfile.TemporaryDirectory(prefix='clash-package-audit-') as temporary:
        outer=Path(temporary)/'outer';outer.mkdir();extract(package,outer)
        manifest=meta(outer/'manifest')
        assert manifest['version']==version and manifest['platform']==platform and manifest['appname']=='clash-for-fnos'
        assert manifest['os_min_version']=='1.1.3100' and not manifest['install_dep_apps']
        assert manifest['checksum']==hashlib.md5((outer/'app.tgz').read_bytes()).hexdigest()
        assert (outer/'cmd/main').read_bytes()==(ROOT/'fpk/cmd/main').read_bytes()
        app=Path(temporary)/'app';app.mkdir();extract(outer/'app.tgz',app)
        assert (app/'licenses/LICENSE').read_bytes()==(ROOT/'LICENSE').read_bytes()==source('LICENSE')
        assert (app/'licenses/Mihomo-LICENSE-GPL-3.txt').read_bytes()==source('assets/licenses/Mihomo-LICENSE-GPL-3.txt')
        for geo in ['Country.mmdb','geoip.dat','geosite.dat']:
            assert (app/'geodata'/geo).read_bytes()==source('assets/geodata/'+geo)
        info=json.loads((app/'build-info.json').read_text())
        assert info['upstream']==lock and info['packageVersion']==version
        public=app/'server/public'
        assert info['frontend']=={str(p.relative_to(public)):sha(p) for p in public.rglob('*') if p.is_file()}
        index=(public/'index.html').read_text()
        assert '<title>Clash for fnos</title>' in index and '/app/clash-for-fnos/assets/' in index
        for asset in re.findall(r'(?:src|href)="(/app/clash-for-fnos/[^\"]+)"',index):assert (public/asset.removeprefix('/app/clash-for-fnos/')).exists()
        js='\n'.join(p.read_text() for p in (public/'assets').glob('*.js'))
        for needle in ['clash-manager.language.v1','zh-CN','en-US','## ['+version+']','连接超时，请检查服务状态后重试','Connection timed out. Check the service status and retry.','软件图标','Other settings']:
            assert needle in js,needle
        icons=json.loads((app/'ui/images/icons/manifest.json').read_text())
        assert len(icons['icons'])==4
        for icon in icons['icons']:
            for size in [64,256]:assert (public/'icons'/f"{icon['id']}_{size}.png").exists()
        expected=sorted(f'clash-for-fnos-{role}-{arch}' for arch in arches for role in ['web','helper'])
        assert sorted(p.name for p in (app/'server/bin').iterdir())==expected
        for binary in (app/'server/bin').iterdir():
            arm=binary.name.endswith('arm64');header=binary.read_bytes()[:64]
            assert header[:6]==b'\x7fELF\x02\x01' and struct.unpack_from('<H',header,18)[0]==(183 if arm else 62)
            build=subprocess.check_output(['go','version','-m',str(binary)],text=True)
            assert 'Clash-Manager/backend' in build and 'GOOS=linux' in build and 'CGO_ENABLED=0' in build
            assert 'GOARCH='+('arm64' if arm else 'amd64') in build and binary.stat().st_mode & 0o111
        assert not any(p.name in ['node','node_modules','package.json','package-lock.json','server.js','privileged-helper.js'] for p in (app/'server').rglob('*'))
        assets=list((app/'core').glob('mihomo-linux-*.gz'))
        if architecture=='all':assert not assets and (app/'core/online-core.json').exists()
        else:
            metadata=json.loads((app/'core/bundled-core.json').read_text())
            assert metadata==json.loads(source('resources/core/'+platform+'/bundled-core.json'))
            assert len(assets)==1 and assets[0].name==source('resources/core/'+platform+'/EXPECTED_ASSET.txt').decode().splitlines()[0].strip()
            assert sha(assets[0])==metadata['sha256'] and assets[0].stat().st_size==metadata['size']
            with gzip.open(assets[0],'rb') as gz:header=gz.read(64)
            assert header[:4]==b'\x7fELF' and struct.unpack_from('<H',header,18)[0]==(183 if platform=='arm' else 62)
        records.append(dict(package=package.name,version=version,platform=platform,sharedVersion=lock['version'],sharedCommit=lock['commit'],sha256=sha(package),result='passed'))
report=ROOT/'dist'/f'Clash for fnos_{version}_build-audit.json'
report.write_text(json.dumps(records,indent=2)+'\n')
print(json.dumps(records,indent=2))
