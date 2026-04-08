package taskdef

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ECSTaskDefDescriber implements ECSDescriber using the real AWS ECS SDK.
type ECSTaskDefDescriber struct {
	client *ecs.Client
}

// NewECSTaskDefDescriber creates a new ECSTaskDefDescriber.
func NewECSTaskDefDescriber(client *ecs.Client) *ECSTaskDefDescriber {
	return &ECSTaskDefDescriber{client: client}
}

// DescribeTaskDefinition fetches a task definition by family name and
// returns the definition along with its tags.
func (d *ECSTaskDefDescriber) DescribeTaskDefinition(ctx context.Context, family string) (*ecsTypes.TaskDefinition, []ecsTypes.Tag, error) {
	output, err := d.client.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(family),
		Include:        []ecsTypes.TaskDefinitionField{ecsTypes.TaskDefinitionFieldTags},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to describe task definition %q: %w", family, err)
	}
	return output.TaskDefinition, output.Tags, nil
}
