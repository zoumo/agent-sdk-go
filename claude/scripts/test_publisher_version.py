"""Offline publication guards; all payloads below are synthetic."""
import importlib.util
import json
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("publisher", pathlib.Path(__file__).with_name("sanitize-transcript.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)

class PublisherVersionTests(unittest.TestCase):
    def test_wrong_native_source_is_quarantined(self):
        frame = {"ordinal": 1, "direction": "server", "channel": "protocol", "data": {
            "type": "system", "subtype": "init", "claude_code_version": "2.1.310"}}
        with self.assertRaises(ValueError):
            publisher.publish(json.dumps(frame), "/private/synthetic")

if __name__ == "__main__":
    unittest.main()
