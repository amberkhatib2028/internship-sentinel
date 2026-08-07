#!/usr/bin/env bash
# Checks everything the deploy needs and reports exactly what is missing.
# Safe to run at any point, including before an AWS account exists.
#
#   ./deploy/preflight.sh

REGION="${AWS_REGION:-us-east-1}"
# Recipient comes from deploy/config.env, which is gitignored so the address
# never lands in version control. See deploy/config.env.example.
if [ -f "$(dirname "$0")/config.env" ]; then
  # shellcheck disable=SC1091
  . "$(dirname "$0")/config.env"
fi
EMAIL="${SENTINEL_TO_EMAIL:-}"
if [ -z "$EMAIL" ]; then
  echo "SENTINEL_TO_EMAIL is not set. Copy deploy/config.env.example to deploy/config.env and fill it in." >&2
  exit 1
fi
ready=1

pass() { printf '  \033[32m✓\033[0m %s\n' "$1"; }
fail() { printf '  \033[31m✗\033[0m %s\n' "$1"; ready=0; }
note() { printf '      %s\n' "$1"; }

echo
echo "Preflight — region ${REGION}, recipient ${EMAIL}"
echo

# --- toolchain ---------------------------------------------------------------
if command -v go >/dev/null 2>&1; then
  pass "go $(go version | awk '{print $3}')"
else
  fail "go not on PATH"
  note 'export PATH="/usr/local/opt/go/bin:$PATH"'
fi

if command -v aws >/dev/null 2>&1; then
  pass "aws $(aws --version 2>&1 | awk '{print $1}' | cut -d/ -f2)"
else
  fail "aws CLI not installed"
  note "brew install awscli"
fi

# --- credentials -------------------------------------------------------------
if ! command -v aws >/dev/null 2>&1; then
  echo; echo "Stopping — install the CLI first."; exit 1
fi

IDENTITY="$(aws sts get-caller-identity --output json 2>/dev/null)"
if [ -z "$IDENTITY" ]; then
  fail "no AWS credentials configured"
  note "Create an access key in IAM, then run:  aws configure"
  note "Region: ${REGION}   Output: json"
  echo
  echo "Stopping — everything below needs credentials."
  exit 1
fi

ACCOUNT_ID="$(echo "$IDENTITY" | python3 -c 'import json,sys;print(json.load(sys.stdin)["Account"])')"
ARN="$(echo "$IDENTITY" | python3 -c 'import json,sys;print(json.load(sys.stdin)["Arn"])')"
pass "credentials work — account ${ACCOUNT_ID}"
note "$ARN"

case "$ARN" in
  *":root")
    fail "you are using ROOT credentials"
    note "Create an IAM user with AdministratorAccess and use its access key instead."
    note "Root keys cannot be scoped or easily revoked if leaked."
    ;;
esac

# --- SES ---------------------------------------------------------------------
STATUS="$(aws ses get-identity-verification-attributes \
  --identities "$EMAIL" --region "$REGION" --output json 2>/dev/null |
  python3 -c "
import json,sys
try:
    d=json.load(sys.stdin)['VerificationAttributes']
    print(d.get('${EMAIL}',{}).get('VerificationStatus','NotFound'))
except Exception:
    print('NotFound')
" 2>/dev/null)"

case "$STATUS" in
  Success) pass "SES: ${EMAIL} verified" ;;
  Pending)
    fail "SES: verification pending"
    note "Click the link AWS emailed to ${EMAIL}, then re-run this."
    ;;
  *)
    fail "SES: ${EMAIL} not verified"
    note "aws ses verify-email-identity --email-address ${EMAIL} --region ${REGION}"
    note "then click the link AWS emails you"
    ;;
esac

# Sandbox is expected and fine — the only recipient is the verified address.
QUOTA="$(aws ses get-send-quota --region "$REGION" --output json 2>/dev/null |
  python3 -c 'import json,sys;print(int(json.load(sys.stdin)["Max24HourSend"]))' 2>/dev/null)"
if [ -n "$QUOTA" ]; then
  if [ "$QUOTA" -le 200 ]; then
    pass "SES sandbox (${QUOTA} sends/day) — expected, plenty here"
  else
    pass "SES production access (${QUOTA} sends/day)"
  fi
fi

# --- existing stack ----------------------------------------------------------
FUNCTION="${FUNCTION_NAME:-swe-intern-sentinel}"
if aws lambda get-function --function-name "$FUNCTION" --region "$REGION" >/dev/null 2>&1; then
  pass "Lambda ${FUNCTION} already deployed (deploy.sh will update it)"
else
  note "Lambda ${FUNCTION} not deployed yet — that's what deploy.sh does"
fi

echo
if [ "$ready" -eq 1 ]; then
  printf '\033[32mReady.\033[0m Run: ./deploy/deploy.sh\n\n'
else
  printf '\033[31mNot ready yet\033[0m — fix the ✗ items above, then re-run this.\n\n'
  exit 1
fi
