#!/usr/bin/env bash
# ==============================================================================
# Pharmaciano ERP — Live API verification (docker)
#
# Boots the app under Docker (via `make docker-verify`) and exercises every
# wired module endpoint end-to-end against the running container. Verifies auth
# (incl. the must-change-password gate on the seeded superadmin), RBAC, and all
# six modules registered in cmd/api/main.go:
#   auth  · organization  · branch  · warehouse  · rbac  · user
#
# Usage:
#   scripts/verify-api.sh                  # against localhost:8080 (after docker up)
#   make docker-verify                      # compose up --wait, then run this
#   BASE_URL=http://localhost:8443 \
#   SUPERADMIN_EMAIL=... SUPERADMIN_PASSWORD=... scripts/verify-api.sh
#
# Exit 0 = all checks passed; 1 = at least one endpoint failed/misbehaved.
# Requires: curl, and either jq or python3 for JSON parsing.
# ==============================================================================
set -u

BASE_URL="${BASE_URL:-http://localhost:8080}"
API="${BASE_URL}/api/v1"
SUPERADMIN_EMAIL="${SUPERADMIN_EMAIL:-superadmin@pharmaciano.local}"
# Seeded via deployments/docker/.env / compose default. MUST match what the seed
# planted (SUPER_ADMIN_INITIAL_PASSWORD). Rotating a seeded account's password on
# first login is expected — the script drives that dance automatically.
SUPERADMIN_PASSWORD="${SUPERADMIN_PASSWORD:-ChangeMe123!}"
NEW_PASSWORD="${NEW_PASSWORD:-Verify-Change-2026!}"
# Used for the first step of the must-change-password gate. Same value as the
# seeded password unless overridden.
CURRENT="${SUPERADMIN_PASSWORD}"

PASS=0
FAIL=0
FAILED_NAMES=()

# Pick a JSON parser once.
if command -v jq >/dev/null 2>&1; then
  JSON_GET='jq -r'
  json_get() { jq -r "$1" <<<"$2"; }
elif command -v python3 >/dev/null 2>&1; then
  json_get() { python3 -c "
import sys, json
try:
    d=json.load(sys.stdin)
except Exception as e:
    print(''); sys.exit(0)
def g(o,p):
    for k in p.split('.'):
        if isinstance(o,dict) and k in o: o=o[k]
        else: return ''
    return o if isinstance(o,(str,int,float,bool)) or o is None else (json.dumps(o) if not isinstance(o,list) else json.dumps(o))
print(g(d, '$1'))
"; }
  JSON_GET='python3 -c ""'
else
  echo "ERR: need jq or python3 for JSON parsing"; exit 2
fi

BASE='/tmp/pharmaciano-verify'
mkdir -p "$BASE"
OUT="$BASE/curl-body.txt"; HDR="$BASE/curl-headers.txt"

# ------------------------------------------------------------------ helpers ---
# http <method> <url> [--data '...'] [extra curl args]  -> writes OUT/HDR, sets STATUS
http() {
  local m="$1" u="$2"; shift 2
  rm -f "$OUT" "$HDR"
  local code
  code=$(curl -sS -o "$OUT" -D "$HDR" -X "$m" "${@}" -w '%{http_code}' "$u")
  STATUS="$code"
  echo "$code"
}

# expect <label> <expected_status...>  — compare $STATUS against the list
expect() {
  local label="$1"; shift
  local ok=0 s
  for s in "$@"; do [ "$STATUS" = "$s" ] && ok=1; done
  if [ "$ok" = 1 ]; then
    PASS=$((PASS+1)); printf 'PASS  [%s] (%s)\n' "$label" "$STATUS"
  else
    FAIL=$((FAIL+1)); FAILED_NAMES+=("$label wanted($*) got($STATUS)")
    printf 'FAIL  [%s] expected (%s) got (%s)\n    body: %s\n' "$label" "$*" "$STATUS" "$(head -c 400 "$OUT")"
  fi
}

require_token() { # fail fast if empty
  if [ -z "${TOKEN:-}" ]; then
    FAIL=$((FAIL+1)); FAILED_NAMES+=("login produced no access token")
    echo "FAIL: no access token — cannot continue protected checks"; exit 1
  fi
}

echo "== Pharmaciano live API verification =="
echo "   API base: $API"
echo ""

# ---------------------------------------------------------------- liveness ---
echo "--- health & operational endpoints ---"
http GET "$BASE_URL/livez"; expect "GET /livez" 200
http GET "$BASE_URL/healthz"; expect "GET /healthz" 200
http GET "$BASE_URL/readyz"; expect "GET /readyz" 200
http GET "$API/status"; expect "GET /api/v1/status" 200

# ------------------------------------------------------------------- auth -----
echo ""
echo "--- auth: login (must-change-password gate) ---"
# First login: the seeded superadmin has must_change_password=true → 401
# PASSWORD_CHANGE_REQUIRED with a single-use token in X-Password-Change-Token.
http POST "$API/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$SUPERADMIN_EMAIL\",\"password\":\"$SUPERADMIN_PASSWORD\"}"
CT=$(grep -i '^X-Password-Change-Token:' "$HDR" | tr -d '\r' | awk '{print $2}')
if [ -n "$CT" ]; then
  expect "login gate (PASSWORD_CHANGE_REQUIRED + token)" 401
  echo "   -> forcing password change with gate token"
  http POST "$API/auth/password/force-change" \
    -H 'Content-Type: application/json' \
    -d "{\"token\":\"$CT\",\"current_password\":\"$CURRENT\",\"new_password\":\"$NEW_PASSWORD\"}"
  TOKEN=$(json_get 'data.access_token' < "$OUT")
  expect "POST /auth/password/force-change → session" 200
  require_token
  echo "   -> rotated password; now using $NEW_PASSWORD for re-login if needed"
else
  # No gate (e.g. must_change_password disabled) — a 200 means a token directly.
  TOKEN=$(json_get 'data.access_token' < "$OUT")
  expect "POST /auth/login (direct session)" 200
  require_token
fi
AUTH="Authorization: Bearer $TOKEN"
echo "   access_token acquired: ${TOKEN:0:16}..."

# Re-login with the new password to prove credential rotation persisted.
http POST "$API/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$SUPERADMIN_EMAIL\",\"password\":\"$NEW_PASSWORD\"}"
expect "relogin with rotated password" 200

echo ""
echo "--- auth: authenticated endpoints ---"
http GET "$API/auth/me" -H "$AUTH"; expect "GET /auth/me" 200
http GET "$API/auth/me/permissions" -H "$AUTH"; expect "GET /auth/me/permissions" 200
http GET "$API/auth/sessions" -H "$AUTH"; expect "GET /auth/sessions" 200
# auth is protected when unauthenticated — ensure the gate actually blocks.
http GET "$API/auth/me"; expect "GET /auth/me (no token → 401)" 401

# ------------------------------------------------------------------- org -------
echo ""
echo "--- organization ---"
http GET "$API/organizations/current" -H "$AUTH"; expect "GET /organizations/current" 200
ORG_ID=$(json_get 'data.organization_id' < "$OUT")
[ -n "$ORG_ID" ] || ORG_ID=$(json_get 'data.id' < "$OUT")
if [ -n "$ORG_ID" ]; then
  http GET "$API/organizations/$ORG_ID" -H "$AUTH"; expect "GET /organizations/{id}" 200
  http GET "$API/organizations/$ORG_ID/summary" -H "$AUTH"; expect "GET /organizations/{id}/summary" 200
  http PATCH "$API/organizations/$ORG_ID" -H "$AUTH" \
    -H 'Content-Type: application/json' -H "Idempotency-Key: org-$(date +%s)" \
    -d '{"name":"Pharmaciano ERP"}'; expect "PATCH /organizations/{id}" 200
fi

# --------------------------------------------------------------------- rbac ----
echo ""
echo "--- rbac: roles & permissions ---"
http GET "$API/roles" -H "$AUTH"; expect "GET /roles" 200
FIRST_ROLE=$(json_get '.data[0].id' < "$OUT" 2>/dev/null)
http GET "$API/roles/$FIRST_ROLE" -H "$AUTH"; expect "GET /roles/{id}" 200
http GET "$API/roles/$FIRST_ROLE/permissions" -H "$AUTH"; expect "GET /roles/{id}/permissions" 200
http GET "$API/permissions" -H "$AUTH"; expect "GET /permissions" 200
FIRST_PERM=$(json_get '.data[0].id' < "$OUT")
http GET "$API/permissions/$FIRST_PERM" -H "$AUTH"; expect "GET /permissions/{id}" 200
# Create a role (exercise write path + RBAC create)
http POST "$API/roles" -H "$AUTH" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: role-$(date +%s)" \
  -d '{"name":"verify-custom-role","description":"verify script"}'; expect "POST /roles" 201

# ------------------------------------------------------------------- branch ----
echo ""
echo "--- branch ---"
BR_CODE="BR-$(date +%s)"
http POST "$API/branches" -H "$AUTH" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: br-$(date +%s)" \
  -d "{\"code\":\"$BR_CODE\",\"name\":\"Verify Branch\",\"city\":\"Dhaka\"}"
expect "POST /branches" 201
BRANCH_ID=$(json_get 'data.id' < "$OUT")
echo "   branch id: $BRANCH_ID"
if [ -n "$BRANCH_ID" ]; then
  http GET "$API/branches" -H "$AUTH"; expect "GET /branches" 200
  http GET "$API/branches/$BRANCH_ID" -H "$AUTH"; expect "GET /branches/{id}" 200
  http PATCH "$API/branches/$BRANCH_ID" -H "$AUTH" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: brp-$(date +%s)" \
    -d '{"name":"Verify Branch Renamed"}'; expect "PATCH /branches/{id}" 200
  http PUT "$API/branches/$BRANCH_ID" -H "$AUTH" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: bru-$(date +%s)" \
    -d "{\"code\":\"$BR_CODE\",\"name\":\"Verify Branch Replaced\"}"; expect "PUT /branches/{id}" 200
  http DELETE "$API/branches/$BRANCH_ID" -H "$AUTH" -H "Idempotency-Key: brd-$(date +%s)"; expect "DELETE /branches/{id}" 204
fi

# --------------------------------------------------------------- warehouse ----
echo ""
echo "--- warehouse ---"
# (re)create a branch to own the warehouse if the prior delete removed ours
if [ -z "${BRANCH_ID:-}" ]; then
  http POST "$API/branches" -H "$AUTH" -H 'Content-Type: application/json' \
    -d "{\"code\":\"WH-OWNER-$(date +%s)\",\"name\":\"WH Owner\"}"
  BRANCH_ID=$(json_get 'data.id' < "$OUT")
fi
WH_CODE="WH-$(date +%s)"
http POST "$API/warehouses" -H "$AUTH" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: wh-$(date +%s)" \
  -d "{\"branch_id\":\"$BRANCH_ID\",\"code\":\"$WH_CODE\",\"name\":\"Verify WH\"}"
expect "POST /warehouses" 201
WH_ID=$(json_get 'data.id' < "$OUT")
if [ -n "$WH_ID" ]; then
  http GET "$API/warehouses" -H "$AUTH"; expect "GET /warehouses" 200
  http GET "$API/warehouses/$WH_ID" -H "$AUTH"; expect "GET /warehouses/{id}" 200
  http GET "$API/branches/$BRANCH_ID/warehouses" -H "$AUTH"; expect "GET /branches/{id}/warehouses" 200
  http PATCH "$API/warehouses/$WH_ID" -H "$AUTH" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: whp-$(date +%s)" \
    -d '{"name":"Verify WH Updated"}'; expect "PATCH /warehouses/{id}" 200
  http DELETE "$API/warehouses/$WH_ID" -H "$AUTH" -H "Idempotency-Key: whd-$(date +%s)"; expect "DELETE /warehouses/{id}" 204
fi

# --------------------------------------------------------------------- user ----
echo ""
echo "--- user ---"
USER_EMAIL="verify.$(date +%s)@pharmaciano.local"
http POST "$API/users" -H "$AUTH" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: usr-$(date +%s)" \
  -d "{\"email\":\"$USER_EMAIL\",\"password\":\"UserPass-2026!\",\"profile\":{\"first_name\":\"Verify\",\"last_name\":\"User\"}}"
expect "POST /users" 201
NEW_USER_ID=$(json_get 'data.id' < "$OUT")
echo "   new user id: $NEW_USER_ID"
if [ -n "$NEW_USER_ID" ]; then
  http GET "$API/users" -H "$AUTH"; expect "GET /users" 200
  http GET "$API/users/me" -H "$AUTH"; expect "GET /users/me" 200
  http GET "$API/users/$NEW_USER_ID" -H "$AUTH"; expect "GET /users/{id}" 200
  http PATCH "$API/users/$NEW_USER_ID" -H "$AUTH" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: usrp-$(date +%s)" \
    -d '{"phone":"+8801700000000"}'; expect "PATCH /users/{id}" 200
  http PATCH "$API/users/$NEW_USER_ID/status" -H "$AUTH" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: usrs-$(date +%s)" \
    -d '{"status":"suspended","reason":"verify"}'; expect "PATCH /users/{id}/status" 200
  http PATCH "$API/users/$NEW_USER_ID/status" -H "$AUTH" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: usra-$(date +%s)" \
    -d '{"status":"active","reason":"re-activate"}'; expect "reactivate user" 200

  # role assignment (SUPER_ADMIN's default role)
  DEFAULT_ROLE=$(json_get '.data[0].id' < "$OUT" 2>/dev/null)
  if [ -z "${DEFAULT_ROLE:-}" ] || [ "$DEFAULT_ROLE" = "null" ]; then
    http GET "$API/roles" -H "$AUTH"; DEFAULT_ROLE=$(json_get '.data[0].id' < "$OUT")
  fi
  if [ -n "$DEFAULT_ROLE" ] && [ "$DEFAULT_ROLE" != "null" ]; then
    http POST "$API/users/$NEW_USER_ID/roles" -H "$AUTH" -H 'Content-Type: application/json' \
      -H "Idempotency-Key: usrro-$(date +%s)" \
      -d "{\"role_id\":\"$DEFAULT_ROLE\"}"; expect "POST /users/{id}/roles (assign)" 200
    http GET "$API/users/$NEW_USER_ID/roles" -H "$AUTH"; expect "GET /users/{id}/roles" 200
    http DELETE "$API/users/$NEW_USER_ID/roles/$DEFAULT_ROLE" -H "$AUTH" -H "Idempotency-Key: usrrd-$(date +%s)"; expect "DELETE /users/{id}/roles/{role_id}" 204
  fi

  # branch assignment
  if [ -n "$BRANCH_ID" ]; then
    http POST "$API/users/$NEW_USER_ID/branches" -H "$AUTH" -H 'Content-Type: application/json' \
      -H "Idempotency-Key: usrb-$(date +%s)" \
      -d "{\"branch_id\":\"$BRANCH_ID\"}"; expect "POST /users/{id}/branches (assign)" 200
    http GET "$API/users/$NEW_USER_ID/branches" -H "$AUTH"; expect "GET /users/{id}/branches" 200
    http DELETE "$API/users/$NEW_USER_ID/branches/$BRANCH_ID" -H "$AUTH" -H "Idempotency-Key: usrbd-$(date +%s)"; expect "DELETE /users/{id}/branches/{branch_id}" 204
  fi

  http DELETE "$API/users/$NEW_USER_ID" -H "$AUTH" -H "Idempotency-Key: usrd-$(date +%s)"; expect "DELETE /users/{id}" 204
fi

# ------------------------------------------------------------ global guards ----
echo ""
echo "--- route guards (uniform envelopes) ---"
http GET "$API/no/such/endpoint" -H "$AUTH"; expect "unknown path → 404" 404
http PATCH "$API/branches" -H "$AUTH" -H 'Content-Type: application/json' -d '{}'; expect "method not allowed → 405" 405

# ---------------------------------------------------------------- summary -----
echo ""
echo "================ RESULT ================"
echo "  PASS: $PASS   FAIL: $FAIL"
if [ "$FAIL" -eq 0 ]; then
  echo "  ✅ Done — all module endpoints verified."
  exit 0
else
  echo "  ❌ FAILURES:"
  for x in "${FAILED_NAMES[@]:-}"; do [ -n "$x" ] && echo "     - $x"; done
  exit 1
fi