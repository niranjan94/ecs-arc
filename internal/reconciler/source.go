package reconciler

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// ConfigSource abstracts where the TOML config comes from. Implementations
// return the raw TOML bytes and a version token. Callers use the version token
// only for equality checks: unchanged token means no reparse needed.
type ConfigSource interface {
	Fetch(ctx context.Context) (content []byte, version string, err error)
}

// SSMSource reads TOML config from an AWS SSM parameter.
type SSMSource struct {
	client SSMClient
	name   string
}

// NewSSMSource constructs an SSMSource over the given client and parameter name.
func NewSSMSource(client SSMClient, name string) *SSMSource {
	return &SSMSource{client: client, name: name}
}

// Fetch returns the parameter value and its numeric version as a string.
func (s *SSMSource) Fetch(ctx context.Context) ([]byte, string, error) {
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(s.name),
	})
	if err != nil {
		return nil, "", fmt.Errorf("ssm get parameter %q: %w", s.name, err)
	}
	return []byte(aws.ToString(out.Parameter.Value)), strconv.FormatInt(out.Parameter.Version, 10), nil
}
