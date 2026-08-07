# Deploying to your own AWS account

End state: a Lambda that runs on two schedules and emails you new listings.
Running cost is effectively **$0** — comfortably inside the AWS free tier
(~4,400 Lambda invocations/month at 512 MB, a few KB in S3, ~100 SES emails).

Steps 1–3 only you can do: they need a credit card, your AWS credentials, and a
click in your inbox. Step 4 onward is one command.

---

## 1. Create the AWS account

Sign up at <https://portal.aws.amazon.com/billing/signup>. It requires a credit
card even though this workload is free-tier.

Then create a non-root user to work as — using root credentials day-to-day is
the single most common way student AWS accounts get compromised and run up
crypto-mining bills:

1. IAM → **Users** → *Create user* → name it e.g. `amber-cli`
2. *Attach policies directly* → **AdministratorAccess**
3. Open the user → **Security credentials** → *Create access key* → choose
   **Command Line Interface (CLI)**
4. Copy the Access key ID and Secret access key

> Enable MFA on the root account while you're in there. Root + no MFA + a leaked
> key is how those bills happen.

## 2. Configure the CLI

The CLI is already installed (`aws-cli/2.36.8`). Run this **yourself** — it
prompts for your secret key, and you should not paste credentials into a chat:

```bash
aws configure
```

- Access key ID / Secret access key: from step 1
- Default region: `us-east-1`
- Default output format: `json`

Confirm it worked:

```bash
aws sts get-caller-identity
```

## 3. Verify your email in SES

Delivery actually goes through **SNS**, not SES (see the delivery section in
[README.md](README.md) — many university domains publish DMARC `p=reject`, which hard
-bounces anything SES sends as such an address). Verifying in SES is still worth
doing so the SES path stays available if you later move to your own domain.

The account starts in the SES **sandbox**, which only delivers to verified
addresses. That is fine here — you are the only recipient — and it avoids a
24-hour support request.

```bash
aws ses verify-email-identity --email-address you@example.com --region us-east-1
```

AWS emails you a confirmation link. **Click it**, then check:

```bash
aws ses get-identity-verification-attributes --identities you@example.com --region us-east-1
```

You want `"VerificationStatus": "Success"`. Nothing will send until this passes.

## 4. Deploy

```bash
./deploy/deploy.sh
```

This is idempotent — re-run it to ship code changes. It creates or updates:

- an S3 bucket `swe-intern-sentinel-<account-id>` for dedupe state, with public
  access blocked
- an IAM role scoped to that bucket, `ses:SendEmail`, and CloudWatch Logs
- the `swe-intern-sentinel` Lambda (`provided.al2023`, 300s timeout, 512 MB)
- an SNS topic `swe-intern-sentinel-alerts` and an email subscription. **The
  first deploy emails you a "Confirm subscription" link — click it, or nothing
  is delivered.**
- two EventBridge rules:
  - `rate(10 minutes)` → `sources: linkedin`
  - `rate(6 hours)` → `sources: all`

Override any default with env vars:

```bash
AWS_REGION=us-east-2 SENTINEL_TO_EMAIL=you@example.com ./deploy/deploy.sh
```

## 5. Test it

Invoke once by hand, bypassing the schedule:

```bash
aws lambda invoke --function-name swe-intern-sentinel \
  --payload '{"email":"you@example.com","sources":"github-list"}' \
  --cli-binary-format raw-in-base64-out /tmp/out.json --region us-east-1 && cat /tmp/out.json
```

`null` means success. Watch what it actually did:

```bash
aws logs tail /aws/lambda/swe-intern-sentinel --follow --region us-east-1
```

The first real run emails the **entire current backlog** (~35 listings) and logs
that it is doing so. Every run after that is incremental.

---

## Troubleshooting

**No email arrives and the logs say "sent N new jobs"** — the SNS subscription
was never confirmed. Check:

```bash
aws sns list-subscriptions-by-topic --topic-arn "$(aws sns create-topic --name swe-intern-sentinel-alerts --query TopicArn --output text --region us-east-1)" --region us-east-1
```

A `SubscriptionArn` of `PendingConfirmation` means the link is still unclicked.

**A bounce mentioning DMARC** — you are on the SES path, not SNS. Confirm the
Lambda has `SNS_TOPIC_ARN` set.

**Function times out** — a full `sources: all` sweep makes ~50 requests. The
timeout is already 300s; if you add many companies, raise it further.

**No email but logs say "no new jobs to send"** — working as intended. Every
listing is already in the dedupe key set. To force a resend, delete the state:

```bash
aws s3 rm s3://swe-intern-sentinel-<account-id>/sent_jobs_swe_intern_2027.json
```

**`AccessDenied` on S3 right after the first deploy** — IAM propagation lag.
Re-run `./deploy/deploy.sh`.

**LinkedIn returns nothing for a whole run** — expected sometimes; it throttles
guest requests by serving a false "no results" page. The retry logic and the
overlapping 30-minute window absorb this. See the reliability section in
[README.md](README.md).

## Tearing it down

```bash
aws events remove-targets --rule swe-intern-sentinel-linkedin --ids 1 --region us-east-1
aws events delete-rule --name swe-intern-sentinel-linkedin --region us-east-1
aws events remove-targets --rule swe-intern-sentinel-sweep --ids 1 --region us-east-1
aws events delete-rule --name swe-intern-sentinel-sweep --region us-east-1
aws lambda delete-function --function-name swe-intern-sentinel --region us-east-1
aws iam delete-role-policy --role-name swe-intern-sentinel-role --policy-name sentinel-policy
aws iam delete-role --role-name swe-intern-sentinel-role
```

The S3 bucket is left alone deliberately — delete it yourself once you've
confirmed you don't want the history.
