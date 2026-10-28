"""Packaging script control-flow tests; mocks do NOT establish isolation."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
MOCK = r'''
import os
from pathlib import Path
import sys

name = Path(sys.argv[0]).name
args = sys.argv[1:]
if name == "bwrap":
    assert "--clearenv" in args
    assert "--unshare-all" in args
    assert "--tmpfs" in args
    online = "--share-net" in args
    env = {}
    module = None
    i = 0
    while i < len(args):
        flag = args[i]
        if flag == "--setenv":
            env[args[i+1]] = args[i+2]
            i += 3
        elif flag in ("--ro-bind", "--bind"):
            i += 3
        elif flag in ("--dev", "--proc", "--tmpfs", "--chdir"):
            if flag == "--chdir":
                module = Path(args[i+1])
            i += 2
        elif flag.startswith("--"):
            i += 1
        else:
            break
    command = args[i:]
    assert command
    assert not Path(env["GO2LEAN_CACHE"]).exists() or not online
    cache = Path(env["GOMODCACHE"])
    assert cache.name == "target-modules"
    if online:
        assert command == ["go", "mod", "download"]
        assert not list(cache.iterdir()), "target cache was not fresh"
        (cache / "prepared").touch()
    else:
        assert command[:4] == ["env", "GOPROXY=off", "GOSUMDB=off", "GOVCS=*:off"]
        if command[5] == "run":
            assert command[6:8] == ["-compare-order", "Compare"]
            assert "-mutate=false" in command and "-prove=false" in command
            assert command[-1] == "."
            assert (cache / "prepared").exists()
            with (cache / "order-commands").open("a") as f:
                f.write("resume\n" if "-resume" in command else "run\n")
    for key in ("HOST_SECRET", "GOFLAGS", "GO2LEAN_LEAN_SANDBOX", "GO2LEAN_SANDBOX"):
        assert key not in env, key
    assert env["GOENV"] == "off"
    assert env["GOWORK"] == "off"
    assert env["GOTOOLCHAIN"] == "local"
    if not online:
        # doctor needs the host's own namespaces, so it never runs in here.
        assert "doctor" not in command
        assert (cache / "prepared").exists()
    sys.exit(0)

if name == "go2lean":
    # Only doctor runs directly on the host, offline and allowlisted.
    env = os.environ
    for key in ("HOST_SECRET", "GOFLAGS", "GO2LEAN_LEAN_SANDBOX", "GO2LEAN_SANDBOX"):
        assert key not in env, key
    assert env["GOENV"] == "off"
    assert env["GOWORK"] == "off"
    assert env["GOTOOLCHAIN"] == "local"
    assert env["GOPROXY"] == "off" and env["GOSUMDB"] == "off" and env["GOVCS"] == "*:off"
    cache = Path(env["GOMODCACHE"])
    assert cache.name == "target-modules"
    module = Path.cwd()
    assert args[0] == "doctor", args
    if not (cache / "prepared").exists():
        if args[1:] == ["-deps", "./..."]:
            if (module / "no-external-deps").exists():
                print("ok    dependencies    no external dependencies")
                sys.exit(0)
            if (module / "unrelated-error").exists():
                print("FAIL  Go version      unavailable toolchain")
                sys.exit(1)
            if (module / "module-load-error").exists():
                print("FAIL  dependencies    go list failed: exit status 1")
                print("go: example.com/dep@v1.0.0: module lookup disabled by GOPROXY=off")
                sys.exit(1)
            if (module / "other-list-error").exists():
                print("FAIL  dependencies    go list failed: exit status 1")
                print("go: cannot read module file: permission denied")
                sys.exit(1)
            print("FAIL  dependency      external dependency not available offline")
            sys.exit(1)
        assert args[1:] == [], args
    else:
        assert args[1:] == ["-deps", "./..."], args
    sys.exit(0)

assert name == "go"
assert "HOST_SECRET" not in os.environ
assert "GO2LEAN_LEAN_SANDBOX" not in os.environ
assert os.environ["GOENV"] == "off"
assert os.environ["GOTOOLCHAIN"] == "local"
if args == ["env", "GOROOT"]:
    print("/explicit/go/toolchain")
else:
    assert args == ["install", "./cmd/go2lean"]
    assert os.environ["GOFLAGS"] == "-mod=readonly"
    cache = Path(os.environ["GOMODCACHE"])
    assert cache.name == "install-modules" and not list(cache.iterdir())
    assert not list(Path(os.environ["GOCACHE"]).iterdir())
    (cache / "installed").touch()
    tool = Path(os.environ["GOBIN"]) / "go2lean"
    tool.write_text(Path(sys.argv[0]).read_text())
    tool.chmod(0o755)
'''


class InstallSmokeTest(unittest.TestCase):
    def test_fresh_caches_and_allowlisted_environment(self):
        self.check_install(False)

    def test_missing_dependencies_before_provisioning(self):
        self.check_install(True)

    def test_missing_module_before_package_json(self):
        self.check_install(True, "module-load-error")

    def test_explicit_ordering_run_and_resume(self):
        self.check_install(False, compare_order=True)

    def test_ordering_with_dependency_diagnosis(self):
        self.check_install(True, compare_order=True)

    def test_invalid_ordering_arguments(self):
        for args in (["--compare-order"], ["--compare-order", "../Compare"],
                     ["--compare-order", "Compare", "--compare-order", "Compare"],
                     ["--unknown"]):
            run = subprocess.run(["bash", str(ROOT / "scripts/install-smoke.sh"),
                                  "/unused", "/unused", *args],
                                 capture_output=True, text=True)
            self.assertEqual(run.returncode, 2)
            self.assertIn("usage:", run.stderr)

    def test_required_dependencies_must_really_be_missing(self):
        self.check_install(True, "no-external-deps", "expected missing external dependencies")

    def test_unrelated_doctor_failure_is_not_dependency_evidence(self):
        self.check_install(True, "unrelated-error", "without a missing-dependency diagnostic")

    def test_unrelated_go_list_failure_is_not_dependency_evidence(self):
        self.check_install(True, "other-list-error", "without a missing-dependency diagnostic")

    def check_install(self, require_deps, scenario=None, expected_error=None, compare_order=False):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            tools = base / "tools"
            tools.mkdir()
            for name in ("go", "bwrap"):
                tool = tools / name
                tool.write_text(f"#!{sys.executable}\n" + MOCK)
                tool.chmod(0o755)
            module = base / "module"
            module.mkdir()
            if scenario:
                (module / scenario).touch()
            result = base / "result"
            env = dict(os.environ, PATH=f"{tools}:/usr/bin:/bin",
                       HOST_SECRET="must-not-pass", GOFLAGS="-invalid-host-flag",
                       GOENV="/nonexistent-host-config", GOWORK="/invalid/go.work",
                       GO2LEAN_LEAN_SANDBOX="off", GO2LEAN_SANDBOX="off")
            command = ["bash", str(ROOT / "scripts/install-smoke.sh"),
                       str(module), str(result)]
            if require_deps:
                command.append("--require-external-deps")
            if compare_order:
                command.extend(["--compare-order", "Compare"])
            run = subprocess.run(command, env=env, capture_output=True, text=True)
            logs = "\n".join(p.read_text() for p in result.glob("*.log"))
            if expected_error:
                self.assertNotEqual(run.returncode, 0, run.stdout + logs)
                self.assertIn(expected_error, run.stderr)
                self.assertFalse((result / "dependencies.log").exists())
                self.assertFalse((result / "compile.log").exists())
                return
            self.assertEqual(run.returncode, 0, run.stderr + logs)
            self.assertIn("offline after dependency setup", run.stdout)
            if compare_order:
                self.assertEqual((result / "target-modules/order-commands").read_text(),
                                 "run\nresume\n")
                self.assertIn("not an acceptance verdict", run.stdout)
            # Old results must not silently become fresh-install evidence.
            run = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertNotEqual(run.returncode, 0)
            # A writable results bind must never make target files writable.
            command[3] = str(module / "results")
            run = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertNotEqual(run.returncode, 0)
            self.assertIn("must not overlap", run.stderr)
            # A symlink alias must not bypass the overlap guard.
            alias = base / "module-alias"
            alias.symlink_to(module, target_is_directory=True)
            command[3] = str(alias / "symlink-results")
            run = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertNotEqual(run.returncode, 0)
            self.assertIn("must not overlap", run.stderr)


if __name__ == "__main__":
    unittest.main()
