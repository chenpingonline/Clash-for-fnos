#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import unittest
spec = importlib.util.spec_from_file_location('security', Path(__file__).with_name('security-toolchains.py'))
security = importlib.util.module_from_spec(spec)
spec.loader.exec_module(security)
class SecurityFloors(unittest.TestCase):
    def test_fixed_versions(self):
        for value in ['go1.26.10', 'go1.26.11', 'go1.27.3', 'go1.28.0']:
            self.assertEqual(security.require_safe_go(value), value)
    def test_unsafe_and_unverified(self):
        for value in ['go1.25.6', 'go1.26.8', 'go1.26.9', 'go1.27.2', 'go1.28rc1', 'devel go1.28', 'go1.26.10-custom']:
            with self.subTest(value=value), self.assertRaises(ValueError):
                security.require_safe_go(value)
unittest.main()
