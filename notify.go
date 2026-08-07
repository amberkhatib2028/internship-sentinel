package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// Notifier delivers the digest.
//
// Which one is used depends on SNS_TOPIC_ARN. SNS is the default for a reason:
// SES can only send as a verified identity, and it DKIM-signs as amazonses.com.
// When the From address is at a domain publishing DMARC p=reject, as many
// university and corporate domains do, the signature does not align with the From domain and the receiving
// server hard-bounces every message. SNS sidesteps this entirely by sending
// from AWS's own domain, which AWS signs, so nothing is being spoofed.
//
// The cost is formatting: SNS delivers plain text only, so the HTML body is
// unused on that path. URLs stay clickable in practically every mail client.
type Notifier interface {
	Send(ctx context.Context, results []Result) error
}

// snsSubjectLimit is imposed by SNS. Subjects must also be single-line ASCII.
const snsSubjectLimit = 100

type SNSNotifier struct {
	client   *sns.Client
	topicARN string
}

func (n SNSNotifier) Send(ctx context.Context, results []Result) error {
	subject := fmt.Sprintf("%s %d new SWE internship listing(s)", subjectPrefix, totalJobs(results))
	if len(subject) > snsSubjectLimit {
		subject = subject[:snsSubjectLimit]
	}

	body := buildTextBody(results) +
		"\nSummer 2027 SWE/ML internships. Quant and defense employers filtered out.\n"

	_, err := n.client.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(n.topicARN),
		Subject:  aws.String(subject),
		Message:  aws.String(body),
	})
	return err
}

type SESNotifier struct {
	client *ses.Client
	to     string
	from   string
}

func (n SESNotifier) Send(ctx context.Context, results []Result) error {
	subject := fmt.Sprintf("%s %d new SWE internship listing(s)", subjectPrefix, totalJobs(results))

	_, err := n.client.SendEmail(ctx, &ses.SendEmailInput{
		Destination: &sestypes.Destination{ToAddresses: []string{n.to}},
		Message: &sestypes.Message{
			Subject: &sestypes.Content{Data: aws.String(subject)},
			Body: &sestypes.Body{
				Text: &sestypes.Content{Data: aws.String(buildTextBody(results))},
				Html: &sestypes.Content{Data: aws.String(buildHTMLBody(results))},
			},
		},
		Source: aws.String(n.from),
	})
	return err
}

// newNotifier picks SNS when a topic is configured, otherwise SES.
func newNotifier(cfg aws.Config, toEmail string) Notifier {
	if topic := strings.TrimSpace(envOr("SNS_TOPIC_ARN", "")); topic != "" {
		return SNSNotifier{client: sns.NewFromConfig(cfg), topicARN: topic}
	}
	return SESNotifier{
		client: ses.NewFromConfig(cfg),
		to:     toEmail,
		from:   envOr("SES_FROM", toEmail),
	}
}

// loadAWSConfig is a thin wrapper so callers don't import the config package.
func loadAWSConfig(ctx context.Context) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx)
}
