"""Slack message posting via xoxc token + browser cookie auth.

Posts messages to Slack channels using the undocumented xoxc/cookie
authentication method, matching the pattern used by daily-status.
"""

import json
import logging
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

_SLACK_API_BASE = "https://slack.com/api"

log = logging.getLogger(__name__)


def _read_creds(creds_dir: str) -> tuple[str, str]:
    """Read xoxc token and d cookie from the credentials directory."""
    creds_path = Path(creds_dir)
    token_file = creds_path / "xoxc_token"
    cookie_file = creds_path / "d_cookie"

    for path, name in [(token_file, "xoxc_token"), (cookie_file, "d_cookie")]:
        if not path.is_file():
            raise SystemExit(
                f"Missing Slack credential file: {path}\n"
                f"Create '{name}' in {creds_dir} with the appropriate value."
            )

    return token_file.read_text().strip(), cookie_file.read_text().strip()


def _slack_api(method: str, token: str, d_cookie: str, params: dict) -> dict:
    """Make a Slack API call and return the parsed JSON response."""
    params["token"] = token
    data = urllib.parse.urlencode(params).encode()

    req = urllib.request.Request(
        f"{_SLACK_API_BASE}/{method}",
        data=data,
        headers={
            "Content-Type": "application/x-www-form-urlencoded",
            "Cookie": f"d={d_cookie}",
        },
    )

    with urllib.request.urlopen(req, timeout=30) as resp:
        result = json.loads(resp.read().decode())

    if not result.get("ok"):
        error = result.get("error", "unknown")
        if error in ("invalid_auth", "token_revoked", "not_authed"):
            raise SystemExit(
                f"Slack auth error: {error}\n"
                "The xoxc token or d cookie has expired.\n"
                f"Refresh the credentials in the creds directory."
            )
    return result


def already_posted_today(channel: str, creds_dir: str) -> bool:
    """Check if a PR status message was already posted to the channel today.

    Searches the last 24 hours of channel history for a message from this
    user containing the PR status prefix.
    """
    token, d_cookie = _read_creds(creds_dir)

    auth_result = _slack_api("auth.test", token, d_cookie, {})
    user_id = auth_result.get("user_id")
    if not user_id:
        return False

    day_start = datetime.now(timezone.utc).replace(hour=0, minute=0, second=0, microsecond=0)

    result = _slack_api("conversations.history", token, d_cookie, {
        "channel": channel,
        "oldest": str(day_start.timestamp()),
        "limit": "50",
    })

    for msg in result.get("messages", []):
        text = msg.get("text", "")
        if msg.get("user") == user_id and (
            text.startswith("*PR Status") or text.startswith("All clear")
        ):
            return True

    return False


def post_message(channel: str, text: str, creds_dir: str) -> None:
    """Post a message to a Slack channel.

    Args:
        channel: Slack channel ID (e.g. C08ESMFV85Q).
        text: Message text (Slack mrkdwn format).
        creds_dir: Directory containing ``xoxc_token`` and ``d_cookie`` files.

    Raises:
        SystemExit: On missing credential files or Slack API auth errors.
    """
    token, d_cookie = _read_creds(creds_dir)

    result = _slack_api("chat.postMessage", token, d_cookie, {
        "channel": channel,
        "text": text,
        "unfurl_links": "false",
    })

    if not result.get("ok"):
        raise SystemExit(f"Slack API error: {result.get('error', 'unknown')}")

    ts = result.get("ts", "")
    log.info("Posted to Slack (channel=%s, ts=%s)", channel, ts)
