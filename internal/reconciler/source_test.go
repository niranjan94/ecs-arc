package reconciler

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmTypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestSSMSource_Fetch(t *testing.T) {
	mock := &mockSSMClient{paramValue: "hello", paramVersion: 7}
	src := NewSSMSource(mock, "my/param")
	content, version, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Errorf("content = %q want hello", content)
	}
	if version != "7" {
		t.Errorf("version = %q want 7", version)
	}
}

func TestSSMSource_FetchError(t *testing.T) {
	mock := &mockSSMClient{err: errors.New("boom")}
	src := NewSSMSource(mock, "x")
	if _, _, err := src.Fetch(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

var _ = aws.String
var _ = (*ssm.GetParameterOutput)(nil)
var _ = (*ssmTypes.Parameter)(nil)
