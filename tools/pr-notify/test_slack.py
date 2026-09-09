"""Unit tests for slack.py."""

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from slack import already_posted_today, post_message


def _make_response(body: dict) -> MagicMock:
    """Create a mock urllib response with the given JSON body."""
    resp = MagicMock()
    resp.read.return_value = json.dumps(body).encode()
    resp.__enter__ = lambda s: s
    resp.__exit__ = MagicMock(return_value=False)
    return resp


def _make_creds_dir(tmp: str, token: str = "xoxc-tok", cookie: str = "d-cookie") -> str:
    """Write token and cookie files into a temp directory."""
    Path(tmp, "xoxc_token").write_text(token + "\n")
    Path(tmp, "d_cookie").write_text(cookie + "\n")
    return tmp


class TestPostMessage(unittest.TestCase):
    """Tests for the post_message function."""

    def test_missing_token_file_raises(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            Path(tmp, "d_cookie").write_text("c")
            with self.assertRaises(SystemExit) as ctx:
                post_message("C123", "hi", tmp)
            self.assertIn("xoxc_token", str(ctx.exception))

    def test_missing_cookie_file_raises(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            Path(tmp, "xoxc_token").write_text("t")
            with self.assertRaises(SystemExit) as ctx:
                post_message("C123", "hi", tmp)
            self.assertIn("d_cookie", str(ctx.exception))

    @patch("slack.urllib.request.urlopen")
    def test_successful_post(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.return_value = _make_response({"ok": True, "ts": "1234.5678"})

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            post_message("C123", "hello", tmp)

        mock_urlopen.assert_called_once()
        req = mock_urlopen.call_args[0][0]
        self.assertIn(b"token=xoxc-tok", req.data)
        self.assertIn(b"channel=C123", req.data)
        self.assertIn(b"unfurl_links=false", req.data)
        self.assertEqual(req.get_header("Cookie"), "d=d-cookie")

    @patch("slack.urllib.request.urlopen")
    def test_auth_error_raises_system_exit(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.return_value = _make_response({"ok": False, "error": "invalid_auth"})

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            with self.assertRaises(SystemExit) as ctx:
                post_message("C123", "hello", tmp)
            self.assertIn("expired", str(ctx.exception))

    @patch("slack.urllib.request.urlopen")
    def test_api_error_raises_system_exit(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.return_value = _make_response({"ok": False, "error": "channel_not_found"})

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            with self.assertRaises(SystemExit) as ctx:
                post_message("C123", "hello", tmp)
            self.assertIn("channel_not_found", str(ctx.exception))


class TestAlreadyPostedToday(unittest.TestCase):
    """Tests for the already_posted_today function."""

    @patch("slack.urllib.request.urlopen")
    def test_returns_true_when_pr_status_found(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.side_effect = [
            _make_response({"ok": True, "user_id": "U123"}),
            _make_response({"ok": True, "messages": [
                {"user": "U999", "text": "unrelated message"},
                {"user": "U123", "text": "*PR Status* - 2026-06-25 | 3 need review"},
            ]}),
        ]

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            self.assertTrue(already_posted_today("C123", tmp))

    @patch("slack.urllib.request.urlopen")
    def test_returns_false_when_no_pr_status_found(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.side_effect = [
            _make_response({"ok": True, "user_id": "U123"}),
            _make_response({"ok": True, "messages": [
                {"user": "U123", "text": "some other message"},
                {"user": "U456", "text": "*PR Status* - posted by someone else"},
            ]}),
        ]

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            self.assertFalse(already_posted_today("C123", tmp))

    @patch("slack.urllib.request.urlopen")
    def test_returns_false_when_no_messages(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.side_effect = [
            _make_response({"ok": True, "user_id": "U123"}),
            _make_response({"ok": True, "messages": []}),
        ]

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            self.assertFalse(already_posted_today("C123", tmp))

    @patch("slack.urllib.request.urlopen")
    def test_auth_error_raises_system_exit(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.return_value = _make_response({"ok": False, "error": "not_authed"})

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            with self.assertRaises(SystemExit):
                already_posted_today("C123", tmp)

    @patch("slack.urllib.request.urlopen")
    def test_returns_false_when_user_id_missing(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.return_value = _make_response({"ok": True})

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            self.assertFalse(already_posted_today("C123", tmp))

    @patch("slack.urllib.request.urlopen")
    def test_ignores_other_users_pr_status(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.side_effect = [
            _make_response({"ok": True, "user_id": "U123"}),
            _make_response({"ok": True, "messages": [
                {"user": "U456", "text": "*PR Status* - 2026-06-25 | 5 need review"},
            ]}),
        ]

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            self.assertFalse(already_posted_today("C123", tmp))

    @patch("slack.urllib.request.urlopen")
    def test_matches_pr_status_summary_variant(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.side_effect = [
            _make_response({"ok": True, "user_id": "U123"}),
            _make_response({"ok": True, "messages": [
                {"user": "U123", "text": "*PR Status Summary* - 2026-06-25\n5 ready"},
            ]}),
        ]

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            self.assertTrue(already_posted_today("C123", tmp))

    @patch("slack.urllib.request.urlopen")
    def test_matches_all_clear_message(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.side_effect = [
            _make_response({"ok": True, "user_id": "U123"}),
            _make_response({"ok": True, "messages": [
                {"user": "U123", "text": "All clear - no actionable PRs across 5 repos :tada:"},
            ]}),
        ]

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            self.assertTrue(already_posted_today("C123", tmp))

    @patch("slack.urllib.request.urlopen")
    def test_returns_false_on_history_api_error(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.side_effect = [
            _make_response({"ok": True, "user_id": "U123"}),
            _make_response({"ok": False, "error": "channel_not_found"}),
        ]

        with tempfile.TemporaryDirectory() as tmp:
            _make_creds_dir(tmp)
            self.assertFalse(already_posted_today("C123", tmp))


if __name__ == "__main__":
    unittest.main()
