#!/usr/bin/env python3
"""Reject Go builds missing the CVE-2026-111943 fix, including embedded cores."""
import re
import subprocess
import sys

def require_safe_go(version):
    match = re.fullmatch(r'go1\.(\d+)\.(\d+)', version)
    if not match:
        raise ValueError(f'Unverified Go toolchain: {version}')
    minor, patch = map(int, match.groups())
    if not ((minor == 26 and patch >= 10) or (minor == 27 and patch >= 3) or minor >= 28):
        raise ValueError(f'Unsafe Go toolchain: {version}; require Go 1.26.10+ or 1.27.3+')
    return version

def binary_go(path):
    build = subprocess.check_output(['go', 'version', '-m', str(path)], text=True)
    return require_safe_go(build.splitlines()[0].rsplit(': ', 1)[-1].strip())

if __name__ == '__main__':
    if len(sys.argv) == 1:
        print(require_safe_go(subprocess.check_output(['go', 'env', 'GOVERSION'], text=True).strip()))
    else:
        for path in sys.argv[1:]:
            print(path, binary_go(path))
