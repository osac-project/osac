"""Parse the HTTPS URLs used for Fulfillment authentication and requests."""

from urllib.parse import urlsplit

from ansible.errors import AnsibleError


def parse_https_url(raw_url: str, label: str):
    try:
        parsed = urlsplit(raw_url.strip())
        hostname = parsed.hostname
        port = parsed.port
    except ValueError:
        raise AnsibleError(f"{label} is not a valid HTTPS URL.") from None

    if (
        parsed.scheme.lower() != "https"
        or not parsed.netloc
        or not hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
    ):
        raise AnsibleError(f"{label} must be an HTTPS URL without credentials, query, or fragment.")

    return parsed, (hostname.lower(), port if port is not None else 443)


def parse_fulfillment_api_url(raw_url: str):
    label = "Fulfillment internal API URL"
    raw_url = raw_url.strip()
    parsed, _ = parse_https_url(raw_url, label)
    if parsed.netloc.endswith(":") or any(char.isspace() for char in raw_url):
        raise AnsibleError(f"{label} is not a valid HTTPS URL.")
    if "?" in raw_url or "#" in raw_url:
        raise AnsibleError(f"{label} must be an HTTPS URL without credentials, query, or fragment.")
    if parsed.path not in ("", "/"):
        raise AnsibleError(f"The {label} must identify the listener root.")
    return parsed
