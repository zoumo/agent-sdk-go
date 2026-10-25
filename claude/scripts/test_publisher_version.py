"""Offline publication guards; all payloads below are synthetic."""
import importlib.util
import json
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("publisher", pathlib.Path(__file__).with_name("sanitize-transcript.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)

class PublisherVersionTests(unittest.TestCase):
    def test_child_facts_require_separate_review_before_publication(self):
        for data in [
            {"type": "system", "subtype": "task_notification", "task_id": "task-a", "status": "completed"},
            {"type": "system", "subtype": "background_tasks_changed", "tasks": []},
            {"type": "assistant", "parent_tool_use_id": "tool-a", "message": {"content": "ALPHA"}},
            {"type": "control_request", "request_id": "p", "request": {
                "subtype": "can_use_tool", "tool_name": "Read", "agent_id": "child-a", "input": {}}},
        ]:
            with self.subTest(data=data):
                frame = {"ordinal": 1, "direction": "server", "channel": "protocol", "data": data}
                with self.assertRaisesRegex(ValueError, "child"):
                    publisher.publish(json.dumps(frame), "/private/synthetic")

    def test_wrong_native_source_is_quarantined(self):
        frame = {"ordinal": 1, "direction": "server", "channel": "protocol", "data": {
            "type": "system", "subtype": "init", "claude_code_version": "2.1.310"}}
        with self.assertRaises(ValueError):
            publisher.publish(json.dumps(frame), "/private/synthetic")

if __name__ == "__main__":
    unittest.main()
