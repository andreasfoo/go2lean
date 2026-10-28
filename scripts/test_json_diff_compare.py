"""The differential release gate must reject missing or inconclusive evidence."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("json-diff-compare.py")


class ComparisonGate(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ws = Path(self.tmp.name)
        for name in ("properties", "counterexamples", "verdicts"):
            (self.ws / name).mkdir()

    def write(self, name, value):
        (self.ws / name).write_text(json.dumps(value))

    def evidence(self, status="confirmed", value="hello"):
        self.write("properties/p_diff.json", {})
        self.write("counterexamples/cx.json", {
            "id": "cx_diff_random_s1", "property": "p_diff", "probes": [{"value": "hello"}]})
        self.write("verdicts/cx.json", {"status": status, "observed": {"ret": value}})

    def run_gate(self, seeds=1):
        return subprocess.run([sys.executable, str(SCRIPT), str(self.ws), str(seeds)],
                              capture_output=True, text=True)

    def test_empty(self):
        self.assertNotEqual(self.run_gate().returncode, 0)

    def test_unsearched_property(self):
        self.write("properties/p_diff.json", {})
        self.assertNotEqual(self.run_gate().returncode, 0)

    def test_missing_verdict(self):
        self.evidence()
        (self.ws / "verdicts/cx.json").unlink()
        self.assertNotEqual(self.run_gate().returncode, 0)

    def test_inconclusive(self):
        self.evidence(status="inconclusive")
        self.assertNotEqual(self.run_gate().returncode, 0)

    def test_difference(self):
        self.evidence(value="different")
        self.assertNotEqual(self.run_gate().returncode, 0)

    def test_insufficient_seeds(self):
        self.evidence()
        self.assertNotEqual(self.run_gate(2).returncode, 0)

    def test_confirmed(self):
        self.evidence()
        self.assertEqual(self.run_gate().returncode, 0)

    def test_duplicate_seed_not_coverage(self):
        self.evidence()
        self.write("counterexamples/duplicate.json", json.loads(
            (self.ws / "counterexamples/cx.json").read_text()))
        self.write("verdicts/duplicate.json", json.loads(
            (self.ws / "verdicts/cx.json").read_text()))
        self.assertNotEqual(self.run_gate(2).returncode, 0)

    def test_uncovered_second_property(self):
        self.evidence()
        self.write("properties/p_otherdiff.json", {})
        self.assertNotEqual(self.run_gate().returncode, 0)


if __name__ == "__main__":
    unittest.main()
