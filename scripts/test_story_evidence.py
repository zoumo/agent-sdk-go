"""Offline regression checks for the published evidence inventory gate."""
import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("story_evidence", Path(__file__).with_name("check-story-evidence.py"))
gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate)


class StoryEvidenceTests(unittest.TestCase):
    def test_inventory_reports_missing_duplicate_and_unexpected_ids(self):
        expected = {"SDK-US-001", "SDK-US-002"}
        self.assertEqual([], gate.inventory_errors("| SDK-US-001 | x |\n| SDK-US-002 | y |", "SDK-US", expected))
        errors = gate.inventory_errors("| SDK-US-001 | x |\n| SDK-US-001 | y |\n| SDK-US-020 | z |", "SDK-US", expected)
        self.assertEqual(["SDK-US: missing SDK-US-002", "SDK-US: unexpected SDK-US-020", "SDK-US: duplicate SDK-US-001"], errors)

    def test_relative_links_resolve_from_each_document(self):
        root = Path(__file__).resolve().parents[1]
        self.assertEqual([], gate.link_errors(root / "docs/index.md", "[ok](testing.md) [web](https://example.org)"))
        errors = gate.link_errors(root / "docs/index.md", "[bad](missing-issue8-file.md)")
        self.assertEqual(1, len(errors))
        self.assertIn("missing-issue8-file.md", errors[0])

    def test_local_fragment_must_name_a_real_heading(self):
        root = Path(__file__).resolve().parents[1]
        path = root / "docs/index.md"
        self.assertEqual([], gate.link_errors(path, "[ok](testing.md#external-consumers)"))
        errors = gate.link_errors(path, "[bad](testing.md#missing-evidence-heading)")
        self.assertEqual(1, len(errors))
        self.assertIn("missing-evidence-heading", errors[0])


if __name__ == "__main__":
    unittest.main()
