#!/bin/bash
# osac edit helper for record-demo.sh: clears status.keycloak_user_id so
# the User reconciler re-links the current Keycloak account (it only
# skips the lookup while the field is set).
yq -i 'del(.status.keycloak_user_id)' "$1"
