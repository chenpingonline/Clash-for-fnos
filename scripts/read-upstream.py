#!/usr/bin/env python3
"""Validate the pinned source before any Git fetch or source extraction."""
import json, re, sys
from pathlib import Path
try:
    data = json.loads(Path(sys.argv[1]).read_text())
    if not isinstance(data, dict) or data.get('schema') != 1: raise ValueError('unsupported upstream.lock schema')
    if not re.fullmatch(r'https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git', data['repository']): raise ValueError('invalid repository URL')
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', data['version']): raise ValueError('invalid shared version')
    if not re.fullmatch(r'[0-9a-f]{40}', data['commit']): raise ValueError('commit must be a full lowercase SHA')
    print(data['repository'], data['version'], data['commit'])
except (AssertionError, KeyError, TypeError, ValueError, OSError) as error:
    sys.exit('Invalid upstream.lock: ' + str(error))
