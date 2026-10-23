#!/usr/bin/env python3
"""Exercise the real lifecycle script with delayed/failed service processes.

Run on Linux as root (a disposable container is sufficient).
"""
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest

MAIN = Path(__file__).resolve().parents[1] / "fpk/cmd/main"


@unittest.skipUnless(sys.platform == "linux" and os.geteuid() == 0, "requires Linux root/container")
class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="clash-lifecycle-")
        self.root = Path(self.tmp.name)
        self.app = self.root / "app"
        self.var = self.root / "var"
        self.etc = self.root / "etc"
        self.bin = self.app / "server/bin"
        for path in (self.bin, self.var, self.etc):
            path.mkdir(parents=True)
        self.env = dict(os.environ, TRIM_APPDEST=str(self.app), TRIM_PKGVAR=str(self.var),
                        TRIM_PKGETC=str(self.etc), TRIM_USERNAME="root", TRIM_GROUPNAME="root",
                        TRIM_TEMP_LOGFILE=str(self.root / "error"))
        self.arch = {"x86_64": "x86_64", "aarch64": "arm64"}[os.uname().machine]
        self.service("helper", 0)
        self.service("web", 0)

    def service(self, kind, delay, fail=False):
        path = self.bin / f"clash-for-fnos-{kind}-{self.arch}"
        key = "PRIV_SOCKET_PATH" if kind == "helper" else "SOCKET_PATH"
        path.write_text(f"#!{sys.executable}\nimport os,socket,time\ntime.sleep({delay})\n"
                        + ("raise SystemExit(42)\n" if fail else
                           f"s=socket.socket(socket.AF_UNIX)\ns.bind(os.environ['{key}'])\ns.listen()\ntime.sleep(120)\n"))
        path.chmod(0o755)

    def call(self, action):
        return subprocess.run(["bash", str(MAIN), action], env=self.env,
                              capture_output=True, text=True, timeout=45)

    def tearDown(self):
        self.call("stop")
        self.tmp.cleanup()

    def test_child_waits_for_exec_before_identity_check(self):
        # A freshly forked child still has its shell cmdline until exec completes.
        # Delay that transition deterministically rather than relying on load.
        helper = self.bin / f"clash-for-fnos-helper-{self.arch}"
        definitions = MAIN.read_text().split('if [ "${1:-}" = start ]')[0]
        script = definitions + f"""
PRIV_SOCKET_PATH="$PRIV_SOCKET_PATH" /bin/sh -c 'sleep 0.6; exec {helper}' &
STARTED_HELPER_PID=$!
echo "$STARTED_HELPER_PID" > "$HELPER_PID_FILE"
wait_ready helper
result=$?
# Reap the synthetic child even when the old lifecycle rejects it.
kill -TERM "$STARTED_HELPER_PID" 2>/dev/null || true
wait "$STARTED_HELPER_PID" 2>/dev/null || true
stop_app
exit "$result"
"""
        result = subprocess.run(["bash", "-c", script], env=self.env,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_cleanup_owns_child_before_exec(self):
        definitions = MAIN.read_text().split('if [ "${1:-}" = start ]')[0]
        script = definitions + """
/bin/sh -c 'exec sleep 30' &
STARTED_HELPER_PID=$!
echo "$STARTED_HELPER_PID" > "$HELPER_PID_FILE"
stop_app
if kill -0 "$STARTED_HELPER_PID" 2>/dev/null; then
  kill -KILL "$STARTED_HELPER_PID"
  exit 1
fi
"""
        result = subprocess.run(["bash", "-c", script], env=self.env,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_delayed_start_and_idempotence(self):
        self.service("helper", 2.5)
        self.service("web", 1.5)
        result = self.call("start")
        self.assertEqual(result.returncode, 0, result.stderr)
        pid = (self.var / "clash-for-fnos-helper.pid").read_text()
        self.assertEqual(self.call("status").returncode, 0)
        self.assertEqual(self.call("start").returncode, 0)
        self.assertEqual((self.var / "clash-for-fnos-helper.pid").read_text(), pid)
        self.assertEqual(self.call("stop").returncode, 0)
        self.assertEqual(self.call("status").returncode, 3)

    def test_failed_children_are_cleaned(self):
        for kind in ("helper", "web"):
            with self.subTest(kind=kind):
                self.service(kind, 0.1, fail=True)
                result = self.call("start")
                self.assertNotEqual(result.returncode, 0)
                for name in ("clash-for-fnos.pid", "clash-for-fnos-helper.pid", "clash-for-fnos.sock", "clash-for-fnos-priv.sock"):
                    self.assertFalse((self.var / name).exists(), name)
                self.service(kind, 0)

    def test_reused_pid_is_not_killed(self):
        proc = subprocess.Popen(["sleep", "120"])
        try:
            for name in ("clash-for-fnos.pid", "clash-for-fnos-helper.pid"):
                (self.var / name).write_text(str(proc.pid))
            self.assertEqual(self.call("start").returncode, 0)
            self.assertIsNone(proc.poll())
            self.assertEqual(self.call("stop").returncode, 0)
            self.assertIsNone(proc.poll())
        finally:
            proc.send_signal(signal.SIGTERM)
            proc.wait()

    def test_timeout_cleans_up_and_allows_retry(self):
        self.service("helper", 40)
        started = time.monotonic()
        self.assertNotEqual(self.call("start").returncode, 0)
        self.assertGreaterEqual(time.monotonic() - started, 30)
        self.assertFalse((self.var / "clash-for-fnos-helper.pid").exists())
        self.assertFalse((self.var / "clash-for-fnos-priv.sock").exists())
        self.service("helper", 0)
        self.assertEqual(self.call("start").returncode, 0)

    def test_concurrent_start_is_serialized(self):
        self.service("helper", 0.5)
        first = subprocess.Popen(["bash", str(MAIN), "start"], env=self.env,
                                 stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        second = self.call("start")
        self.assertEqual(first.wait(timeout=45), 0)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(self.call("status").returncode, 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
