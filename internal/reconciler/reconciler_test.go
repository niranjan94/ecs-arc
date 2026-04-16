package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmTypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// --- Mock SSM Client ---

type mockSSMClient struct {
	paramValue   string
	paramVersion int64
	err          error
}

func (m *mockSSMClient) GetParameter(_ context.Context, _ *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmTypes.Parameter{
			Value:   aws.String(m.paramValue),
			Version: m.paramVersion,
		},
	}, nil
}

// --- Mock ECS Registrar ---

type mockECSRegistrar struct {
	registerCalled int
	describeErr    error
	describeTags   []ecsTypes.Tag
	listFamilies   []string
}

func newMockECSRegistrar() *mockECSRegistrar {
	return &mockECSRegistrar{}
}

func (m *mockECSRegistrar) RegisterTaskDefinition(_ context.Context, input *ecs.RegisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	m.registerCalled++
	return &ecs.RegisterTaskDefinitionOutput{
		TaskDefinition: &ecsTypes.TaskDefinition{
			TaskDefinitionArn: aws.String(fmt.Sprintf("arn:aws:ecs:us-east-1:123:task-definition/%s:%d", aws.ToString(input.Family), m.registerCalled)),
			Family:            input.Family,
			Revision:          int32(m.registerCalled),
		},
	}, nil
}

func (m *mockECSRegistrar) DeregisterTaskDefinition(_ context.Context, _ *ecs.DeregisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DeregisterTaskDefinitionOutput, error) {
	return &ecs.DeregisterTaskDefinitionOutput{}, nil
}

func (m *mockECSRegistrar) DescribeTaskDefinition(_ context.Context, _ *ecs.DescribeTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error) {
	if m.describeErr != nil {
		return nil, m.describeErr
	}
	return &ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &ecsTypes.TaskDefinition{},
		Tags:           m.describeTags,
	}, nil
}

func (m *mockECSRegistrar) ListTaskDefinitionFamilies(_ context.Context, _ *ecs.ListTaskDefinitionFamiliesInput, _ ...func(*ecs.Options)) (*ecs.ListTaskDefinitionFamiliesOutput, error) {
	return &ecs.ListTaskDefinitionFamiliesOutput{
		Families: m.listFamilies,
	}, nil
}

// --- Test helpers ---

const testTOML = `
[[runner]]
family = "test-runner"
cpu = 1024
memory = 2048
`

var testReconcilerInfra = InfraConfig{
	ExecutionRoleARN: "arn:exec",
	TaskRoleARN:      "arn:task",
	LogGroup:         "/ecs/runners",
	Region:           "us-east-1",
}

func newTestReconciler(ssmClient SSMClient, ecsClient ECSRegistrar, events chan<- ReconcileEvent) *Reconciler {
	return New(ssmClient, ecsClient, "/param", 5*time.Minute, testReconcilerInfra, events, slog.Default())
}

// --- Tests ---

func TestReconciler_VersionSkip(t *testing.T) {
	mockSSM := &mockSSMClient{paramValue: testTOML, paramVersion: 1}
	mockECS := newMockECSRegistrar()
	mockECS.describeErr = fmt.Errorf("not found") // startup will register
	events := make(chan ReconcileEvent, 16)

	rec := newTestReconciler(mockSSM, mockECS, events)

	// First run: startup
	ctx := context.Background()
	rec.reconcileStartup(ctx)

	// Drain startup events
	close(events)
	for range events {
	}

	// Reset events channel and register count
	events = make(chan ReconcileEvent, 16)
	rec.events = events
	initialCalls := mockECS.registerCalled

	// Second run: same version -> no events, no ECS calls
	rec.reconcile(ctx)
	close(events)

	eventCount := 0
	for range events {
		eventCount++
	}
	if eventCount != 0 {
		t.Errorf("expected 0 events on version skip, got %d", eventCount)
	}
	if mockECS.registerCalled != initialCalls {
		t.Errorf("RegisterTaskDefinition called %d extra times on version skip", mockECS.registerCalled-initialCalls)
	}
}

func TestReconciler_NewFamily(t *testing.T) {
	mockSSM := &mockSSMClient{paramValue: testTOML, paramVersion: 1}
	mockECS := newMockECSRegistrar()
	mockECS.describeErr = fmt.Errorf("not found") // task def doesn't exist
	events := make(chan ReconcileEvent, 16)

	rec := newTestReconciler(mockSSM, mockECS, events)

	ctx := context.Background()
	rec.reconcileStartup(ctx)

	close(events)
	var created []ReconcileEvent
	for e := range events {
		created = append(created, e)
	}

	if len(created) != 1 {
		t.Fatalf("expected 1 event, got %d", len(created))
	}
	if created[0].Kind != EventCreate {
		t.Errorf("expected EventCreate, got %d", created[0].Kind)
	}
	if created[0].Family != "test-runner" {
		t.Errorf("Family = %q, want test-runner", created[0].Family)
	}
	if mockECS.registerCalled != 1 {
		t.Errorf("RegisterTaskDefinition called %d times, want 1", mockECS.registerCalled)
	}
}

func TestReconciler_ChangedFamily(t *testing.T) {
	toml1 := `
[[runner]]
family = "test-runner"
cpu = 1024
memory = 2048
`
	toml2 := `
[[runner]]
family = "test-runner"
cpu = 2048
memory = 4096
`
	mockSSM := &mockSSMClient{paramValue: toml1, paramVersion: 1}
	mockECS := newMockECSRegistrar()
	mockECS.describeErr = fmt.Errorf("not found")
	events := make(chan ReconcileEvent, 16)

	rec := newTestReconciler(mockSSM, mockECS, events)

	ctx := context.Background()
	rec.reconcileStartup(ctx)

	// Drain startup events
	close(events)
	for range events {
	}

	// Change config
	events = make(chan ReconcileEvent, 16)
	rec.events = events
	mockSSM.paramValue = toml2
	mockSSM.paramVersion = 2

	rec.reconcile(ctx)
	close(events)

	var updates []ReconcileEvent
	for e := range events {
		updates = append(updates, e)
	}

	if len(updates) != 1 {
		t.Fatalf("expected 1 event, got %d", len(updates))
	}
	if updates[0].Kind != EventUpdate {
		t.Errorf("expected EventUpdate, got %d", updates[0].Kind)
	}
}

func TestReconciler_RemovedFamily(t *testing.T) {
	toml1 := `
[[runner]]
family = "runner-a"
cpu = 1024
memory = 2048

[[runner]]
family = "runner-b"
cpu = 2048
memory = 4096
`
	toml2 := `
[[runner]]
family = "runner-a"
cpu = 1024
memory = 2048
`
	mockSSM := &mockSSMClient{paramValue: toml1, paramVersion: 1}
	mockECS := newMockECSRegistrar()
	mockECS.describeErr = fmt.Errorf("not found")
	events := make(chan ReconcileEvent, 16)

	rec := newTestReconciler(mockSSM, mockECS, events)

	ctx := context.Background()
	rec.reconcileStartup(ctx)

	// Drain startup events
	close(events)
	for range events {
	}

	// Remove runner-b
	events = make(chan ReconcileEvent, 16)
	rec.events = events
	mockSSM.paramValue = toml2
	mockSSM.paramVersion = 2

	rec.reconcile(ctx)
	close(events)

	var removed []ReconcileEvent
	for e := range events {
		if e.Kind == EventRemove {
			removed = append(removed, e)
		}
	}

	if len(removed) != 1 {
		t.Fatalf("expected 1 remove event, got %d", len(removed))
	}
	if removed[0].Family != "runner-b" {
		t.Errorf("removed family = %q, want runner-b", removed[0].Family)
	}
}

func TestReconciler_StartupExistingManaged(t *testing.T) {
	mockSSM := &mockSSMClient{paramValue: testTOML, paramVersion: 1}
	mockECS := newMockECSRegistrar()
	mockECS.describeTags = []ecsTypes.Tag{
		{Key: aws.String("ecs-arc:managed"), Value: aws.String("true")},
	}
	events := make(chan ReconcileEvent, 16)

	rec := newTestReconciler(mockSSM, mockECS, events)

	ctx := context.Background()
	rec.reconcileStartup(ctx)

	close(events)
	var created []ReconcileEvent
	for e := range events {
		created = append(created, e)
	}

	// Should still register a new revision (to ensure state matches config)
	if mockECS.registerCalled != 1 {
		t.Errorf("RegisterTaskDefinition called %d times, want 1", mockECS.registerCalled)
	}
	if len(created) != 1 || created[0].Kind != EventCreate {
		t.Errorf("expected 1 EventCreate, got %d events", len(created))
	}
}

func TestReconciler_StartupExistingUnmanaged(t *testing.T) {
	mockSSM := &mockSSMClient{paramValue: testTOML, paramVersion: 1}
	mockECS := newMockECSRegistrar()
	// No managed tag
	mockECS.describeTags = []ecsTypes.Tag{}
	events := make(chan ReconcileEvent, 16)

	rec := newTestReconciler(mockSSM, mockECS, events)

	ctx := context.Background()
	rec.reconcileStartup(ctx)

	close(events)
	eventCount := 0
	for range events {
		eventCount++
	}

	// Should NOT register -- unmanaged task def should be skipped
	if mockECS.registerCalled != 0 {
		t.Errorf("RegisterTaskDefinition called %d times, want 0 (unmanaged)", mockECS.registerCalled)
	}
	if eventCount != 0 {
		t.Errorf("expected 0 events for unmanaged task def, got %d", eventCount)
	}
}

func TestReconciler_StartupOrphanCleanup(t *testing.T) {
	mockSSM := &mockSSMClient{paramValue: testTOML, paramVersion: 1}
	mockECS := newMockECSRegistrar()
	mockECS.describeErr = fmt.Errorf("not found") // primary family doesn't exist
	mockECS.listFamilies = []string{"test-runner", "orphan-runner"}
	events := make(chan ReconcileEvent, 16)

	// We need a more sophisticated mock for this test since DescribeTaskDefinition
	// is called for both startup (test-runner) and orphan check (orphan-runner).
	// Use a custom mock that returns different results per family.
	customECS := &familyAwareMockECS{
		families: map[string]describeResult{
			"test-runner": {
				err: fmt.Errorf("not found"),
			},
			"orphan-runner": {
				tags: []ecsTypes.Tag{
					{Key: aws.String("ecs-arc:managed"), Value: aws.String("true")},
				},
			},
		},
		listFamilies: []string{"test-runner", "orphan-runner"},
	}

	rec := newTestReconciler(mockSSM, customECS, events)

	ctx := context.Background()
	rec.reconcileStartup(ctx)

	close(events)
	var removeEvents []ReconcileEvent
	for e := range events {
		if e.Kind == EventRemove {
			removeEvents = append(removeEvents, e)
		}
	}

	if len(removeEvents) != 1 {
		t.Fatalf("expected 1 remove event for orphan, got %d", len(removeEvents))
	}
	if removeEvents[0].Family != "orphan-runner" {
		t.Errorf("removed family = %q, want orphan-runner", removeEvents[0].Family)
	}
}

// --- Family-aware mock for complex startup scenarios ---

type describeResult struct {
	tags []ecsTypes.Tag
	err  error
}

type familyAwareMockECS struct {
	families       map[string]describeResult
	listFamilies   []string
	registerCalled int
}

func (m *familyAwareMockECS) RegisterTaskDefinition(_ context.Context, input *ecs.RegisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	m.registerCalled++
	return &ecs.RegisterTaskDefinitionOutput{
		TaskDefinition: &ecsTypes.TaskDefinition{
			TaskDefinitionArn: aws.String(fmt.Sprintf("arn:aws:ecs:us-east-1:123:task-definition/%s:%d", aws.ToString(input.Family), m.registerCalled)),
			Family:            input.Family,
			Revision:          int32(m.registerCalled),
		},
	}, nil
}

func (m *familyAwareMockECS) DeregisterTaskDefinition(_ context.Context, _ *ecs.DeregisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DeregisterTaskDefinitionOutput, error) {
	return &ecs.DeregisterTaskDefinitionOutput{}, nil
}

func (m *familyAwareMockECS) DescribeTaskDefinition(_ context.Context, input *ecs.DescribeTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error) {
	family := aws.ToString(input.TaskDefinition)
	if result, ok := m.families[family]; ok {
		if result.err != nil {
			return nil, result.err
		}
		return &ecs.DescribeTaskDefinitionOutput{
			TaskDefinition: &ecsTypes.TaskDefinition{Family: aws.String(family)},
			Tags:           result.tags,
		}, nil
	}
	return nil, fmt.Errorf("task definition %q not found", family)
}

func (m *familyAwareMockECS) ListTaskDefinitionFamilies(_ context.Context, _ *ecs.ListTaskDefinitionFamiliesInput, _ ...func(*ecs.Options)) (*ecs.ListTaskDefinitionFamiliesOutput, error) {
	return &ecs.ListTaskDefinitionFamiliesOutput{
		Families: m.listFamilies,
	}, nil
}
