"""Contract tests for the systemd notifier configuration."""

from pathlib import Path
import unittest


class TestNotifierService(unittest.TestCase):
    """The user service must consume the documented notification configuration."""

    def test_service_uses_user_owned_config(self):
        service = Path(__file__).with_name("pr-notify.service").read_text()

        self.assertIn(
            "ExecStart=/usr/bin/python3 notify.py --config config.toml", service
        )
        self.assertNotIn("config*.example.toml", service)


if __name__ == "__main__":
    unittest.main()
