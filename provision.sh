#!/bin/bash
# Provision an OPS tenant through the Admin API user provisioning endpoint
# (see spec/user_provisioning.md), authorized by the token in ./token.txt.
#
# Usage: ./provision.sh <user> <password>
set -euo pipefail
cd "$(dirname $0)"

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <user> <password>" >&2
    exit 1
fi
USER_NAME="$1"
PASSWORD="$2"
EMAIL="noreply@example.com"
APIHOST="${APIHOST:-http://miniops.me}"
TOKEN_FILE="token.txt"

if [ ! -s "${TOKEN_FILE}" ]; then
    echo "Missing or empty ${TOKEN_FILE}" >&2
    exit 1
fi
# The token must not carry the trailing newline of the file.
TOKEN="$(tr -d '[:space:]' <"${TOKEN_FILE}")"

# jq builds the JSON so user and password are escaped safely.
BODY="$(jq -n --arg user "${USER_NAME}" --arg password "${PASSWORD}" \
    --arg email "${EMAIL}" \
    '{user: $user, password: $password, email: $email, services: "all"}')"

# The token and the body go through file descriptors, never the command line,
# so they do not show up in the process list. The API returns 503 with
# Retry-After until it has read the token secret: retry a few times. A 503
# after the user was created (route setup failed) must not be retried.
for attempt in 1 2 3 4 5; do
    RESP="$(curl -sS -w '\n%{http_code}' -X POST "${APIHOST}/system/api/v1/users" \
        -H @<(printf 'Authorization: Bearer %s\n' "${TOKEN}") \
        -H 'Content-Type: application/json' \
        --data-binary @<(printf '%s' "${BODY}"))"
    CODE="$(printf '%s\n' "${RESP}" | tail -n1)"
    MSG="$(printf '%s\n' "${RESP}" | sed '$d')"
    echo "${MSG}"
    if [ "${CODE}" != "503" ] || [[ "${MSG}" == *"User created"* ]]; then
        break
    fi
    echo "HTTP 503, retrying (${attempt}/5)..." >&2
    sleep "${attempt}"
done

echo "HTTP ${CODE}"
[ "${CODE}" = "202" ]
