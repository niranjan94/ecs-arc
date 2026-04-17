package reconciler

import (
	"context"
	"errors"
	"os"
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

func TestFileSource_ReturnsContentAndStableHash(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/runners.toml"
	if err := os.WriteFile(path, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(path)
	c1, v1, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c2, v2, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(c1) != "body" || string(c2) != "body" {
		t.Errorf("content mismatch: %q %q", c1, c2)
	}
	if v1 != v2 || v1 == "" {
		t.Errorf("expected stable non-empty version, got %q %q", v1, v2)
	}
}

func TestFileSource_VersionChangesOnEdit(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/runners.toml"
	if err := os.WriteFile(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(path)
	_, v1, _ := src.Fetch(context.Background())
	if err := os.WriteFile(path, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, v2, _ := src.Fetch(context.Background())
	if v1 == v2 {
		t.Fatalf("expected different version after edit, both were %q", v1)
	}
}

func TestFileSource_MissingFile(t *testing.T) {
	src := NewFileSource("/nonexistent/path/runners.toml")
	if _, _, err := src.Fetch(context.Background()); err == nil {
		t.Fatal("expected error for missing file")
	}
}
