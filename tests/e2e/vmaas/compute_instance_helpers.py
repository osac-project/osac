"""Shared helpers for VMaaS ComputeInstance tests."""

from datetime import UTC, datetime, timedelta


def next_restart_timestamp(last_restarted_at: str) -> str:
    """Return a UTC timestamp later than the previous restart."""
    timestamp = datetime.now(tz=UTC).replace(microsecond=0)
    if last_restarted_at:
        previous = datetime.fromisoformat(last_restarted_at.replace("Z", "+00:00"))
        if timestamp <= previous:
            timestamp = previous + timedelta(seconds=1)
    return timestamp.strftime("%Y-%m-%dT%H:%M:%SZ")
