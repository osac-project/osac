if [[ -n "${OC_IMPERSONATE:-}" ]]; then
    oc() { command oc --as "${OC_IMPERSONATE}" "$@"; }
fi
