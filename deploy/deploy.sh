#!/usr/bin/env bash
# Creates or updates the whole stack: S3 state bucket, IAM role, Lambda, and the
# two EventBridge schedules. Safe to re-run — every step is idempotent, so this
# doubles as the "ship a code change" command.
#
#   ./deploy/deploy.sh
#
# Prerequisites (see DEPLOY.md): an AWS account, `aws configure` already run,
# and the recipient address verified in SES.

set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"
FUNCTION="${FUNCTION_NAME:-swe-intern-sentinel}"
ROLE_NAME="${ROLE_NAME:-swe-intern-sentinel-role}"
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

# Bucket names are globally unique, so default to one suffixed with the account
# id rather than something that is certainly taken already.
ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"
BUCKET="${S3_BUCKET:-swe-intern-sentinel-${ACCOUNT_ID}}"

cd "$(dirname "$0")/.."
echo "account=${ACCOUNT_ID} region=${REGION} bucket=${BUCKET} function=${FUNCTION}"

# --- state bucket ------------------------------------------------------------
if aws s3api head-bucket --bucket "$BUCKET" >/dev/null 2>&1; then
  echo "bucket exists"
else
  echo "creating bucket"
  if [ "$REGION" = "us-east-1" ]; then
    aws s3api create-bucket --bucket "$BUCKET" --region "$REGION" >/dev/null
  else
    aws s3api create-bucket --bucket "$BUCKET" --region "$REGION" \
      --create-bucket-configuration "LocationConstraint=$REGION" >/dev/null
  fi
  aws s3api put-public-access-block --bucket "$BUCKET" \
    --public-access-block-configuration \
    "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true"
fi

# --- IAM role ----------------------------------------------------------------
if aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
  echo "role exists"
else
  echo "creating role"
  aws iam create-role --role-name "$ROLE_NAME" \
    --assume-role-policy-document file://deploy/trust-policy.json >/dev/null
fi

# The policy grants s3:ListBucket on the bucket itself as well as object access.
# Without it, GetObject on a key that does not exist returns 403 AccessDenied
# rather than 404 NoSuchKey, so the first-run empty-state path never triggers.
# It carries no s3:prefix condition because GetObject supplies no prefix context
# key, so such a condition would never match.
sed "s/SENTINEL_BUCKET/${BUCKET}/g" deploy/lambda-policy.json > /tmp/sentinel-policy.json
aws iam put-role-policy --role-name "$ROLE_NAME" \
  --policy-name sentinel-policy --policy-document file:///tmp/sentinel-policy.json
ROLE_ARN="$(aws iam get-role --role-name "$ROLE_NAME" --query 'Role.Arn' --output text)"

# --- build -------------------------------------------------------------------
echo "building"
GOOS=linux GOARCH=amd64 go build -tags lambda.norpc -o bootstrap .
zip -q -j sentinel.zip bootstrap
rm -f bootstrap

# --- SNS topic ---------------------------------------------------------------
# Delivery goes through SNS rather than SES. SES must send as a verified
# identity and signs as amazonses.com, so a From address at a domain publishing
# DMARC p=reject, as many university domains do, fails alignment and is
# hard-bounced by the recipient. SNS sends from AWS's own signed domain instead.
TOPIC_ARN="$(aws sns create-topic --name "${FUNCTION}-alerts" --region "$REGION" \
  --query 'TopicArn' --output text)"
echo "topic ${TOPIC_ARN}"

# create-topic and subscribe are both idempotent, but re-subscribing an already
# confirmed address would send another confirmation mail, so check first.
if aws sns list-subscriptions-by-topic --topic-arn "$TOPIC_ARN" --region "$REGION" \
  --query 'Subscriptions[].Endpoint' --output text 2>/dev/null | grep -qF "$EMAIL"; then
  echo "subscription exists"
else
  echo "subscribing ${EMAIL} — check your inbox and click Confirm subscription"
  aws sns subscribe --topic-arn "$TOPIC_ARN" --protocol email \
    --notification-endpoint "$EMAIL" --region "$REGION" >/dev/null
fi

ENV_VARS="{SENTINEL_TO_EMAIL=${EMAIL},SES_FROM=${EMAIL},S3_BUCKET=${BUCKET},SOURCES=all,SNS_TOPIC_ARN=${TOPIC_ARN}}"

# --- lambda ------------------------------------------------------------------
if aws lambda get-function --function-name "$FUNCTION" --region "$REGION" >/dev/null 2>&1; then
  echo "updating function code"
  aws lambda update-function-code --function-name "$FUNCTION" \
    --zip-file fileb://sentinel.zip --region "$REGION" >/dev/null
  aws lambda wait function-updated --function-name "$FUNCTION" --region "$REGION"
  aws lambda update-function-configuration --function-name "$FUNCTION" \
    --timeout 300 --memory-size 512 --environment "Variables=$ENV_VARS" \
    --region "$REGION" >/dev/null
else
  echo "creating function"
  # A freshly created role is not immediately usable by Lambda; retry until IAM
  # has propagated rather than failing on a race.
  for attempt in 1 2 3 4 5 6; do
    if aws lambda create-function --function-name "$FUNCTION" \
      --runtime provided.al2023 --handler bootstrap --role "$ROLE_ARN" \
      --zip-file fileb://sentinel.zip --timeout 300 --memory-size 512 \
      --environment "Variables=$ENV_VARS" --region "$REGION" >/dev/null 2>&1; then
      break
    fi
    echo "  waiting for IAM role to propagate (attempt ${attempt}/6)"
    sleep 10
  done
fi
aws lambda wait function-updated --function-name "$FUNCTION" --region "$REGION"
FUNCTION_ARN="$(aws lambda get-function --function-name "$FUNCTION" --region "$REGION" \
  --query 'Configuration.FunctionArn' --output text)"

# --- schedules ---------------------------------------------------------------
# LinkedIn is time-windowed so it runs often; the board sweep is ~50 requests
# and runs rarely. Both target the same function with different SOURCES.
add_schedule() {
  local name="$1" expr="$2" sources="$3"
  aws events put-rule --name "$name" --schedule-expression "$expr" \
    --region "$REGION" >/dev/null
  aws lambda add-permission --function-name "$FUNCTION" --statement-id "$name" \
    --action lambda:InvokeFunction --principal events.amazonaws.com \
    --source-arn "arn:aws:events:${REGION}:${ACCOUNT_ID}:rule/${name}" \
    --region "$REGION" >/dev/null 2>&1 || true

  # The --targets shorthand cannot express Input, whose value is itself a JSON
  # string full of commas and quotes; it parses on the first comma and fails.
  # Build the structure properly and pass it as a file.
  python3 -c '
import json, sys
arn, email, sources, path = sys.argv[1:5]
json.dump(
    [{"Id": "1", "Arn": arn,
      "Input": json.dumps({"email": email, "sources": sources})}],
    open(path, "w"))
' "$FUNCTION_ARN" "$EMAIL" "$sources" /tmp/sentinel-targets.json

  aws events put-targets --rule "$name" --region "$REGION" \
    --targets file:///tmp/sentinel-targets.json >/dev/null
}

add_schedule "${FUNCTION}-linkedin" "rate(10 minutes)" "linkedin"
add_schedule "${FUNCTION}-sweep" "rate(6 hours)" "all"

rm -f sentinel.zip
echo
echo "deployed. tail logs with:"
echo "  aws logs tail /aws/lambda/${FUNCTION} --follow --region ${REGION}"
