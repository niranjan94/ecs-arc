package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/niranjan94/ecs-arc/internal/tomlcfg"
)

// SSMClient is the subset of the SSM API the reconciler needs.
type SSMClient interface {
	GetParameter(ctx context.Context, input *ssm.GetParameterInput,
		opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// ECSRegistrar is the subset of the ECS API the reconciler needs.
type ECSRegistrar interface {
	RegisterTaskDefinition(ctx context.Context, input *ecs.RegisterTaskDefinitionInput,
		opts ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error)
	DeregisterTaskDefinition(ctx context.Context, input *ecs.DeregisterTaskDefinitionInput,
		opts ...func(*ecs.Options)) (*ecs.DeregisterTaskDefinitionOutput, error)
	DescribeTaskDefinition(ctx context.Context, input *ecs.DescribeTaskDefinitionInput,
		opts ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error)
	ListTaskDefinitionFamilies(ctx context.Context, input *ecs.ListTaskDefinitionFamiliesInput,
		opts ...func(*ecs.Options)) (*ecs.ListTaskDefinitionFamiliesOutput, error)
}

// EventKind describes the type of reconciliation event.
type EventKind int

const (
	EventCreate EventKind = iota
	EventUpdate
	EventRemove
)

// ReconcileEvent is emitted by the reconciler to inform the controller
// about task definition lifecycle changes.
type ReconcileEvent struct {
	Kind           EventKind
	Family         string
	Config         *tomlcfg.ResolvedRunnerConfig // nil for Remove
	TaskDefinition *ecsTypes.TaskDefinition       // populated after Register
}

// Reconciler polls SSM for TOML config changes and reconciles ECS task definitions.
type Reconciler struct {
	ssmClient    SSMClient
	ecsClient    ECSRegistrar
	paramName    string
	pollInterval time.Duration
	lastVersion  int64
	infra        InfraConfig
	desired      map[string]*tomlcfg.ResolvedRunnerConfig
	events       chan<- ReconcileEvent
	logger       *slog.Logger
}

// New creates a new Reconciler.
func New(
	ssmClient SSMClient,
	ecsClient ECSRegistrar,
	paramName string,
	pollInterval time.Duration,
	infra InfraConfig,
	events chan<- ReconcileEvent,
	logger *slog.Logger,
) *Reconciler {
	return &Reconciler{
		ssmClient:    ssmClient,
		ecsClient:    ecsClient,
		paramName:    paramName,
		pollInterval: pollInterval,
		infra:        infra,
		desired:      make(map[string]*tomlcfg.ResolvedRunnerConfig),
		events:       events,
		logger:       logger,
	}
}

// Run starts the reconciliation loop. It performs startup reconciliation
// first, then polls SSM on the configured interval.
func (r *Reconciler) Run(ctx context.Context) {
	r.reconcileStartup(ctx)

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) {
	// 1. Fetch SSM parameter
	param, err := r.ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(r.paramName),
	})
	if err != nil {
		r.logger.Error("failed to get SSM parameter", "error", err.Error())
		return
	}

	// 2. Version check
	version := param.Parameter.Version
	if version == r.lastVersion {
		return
	}

	// 3. Parse and resolve
	cfg, err := tomlcfg.Parse([]byte(aws.ToString(param.Parameter.Value)))
	if err != nil {
		r.logger.Error("failed to parse TOML config", "error", err.Error())
		return
	}
	newDesired, err := tomlcfg.Resolve(cfg)
	if err != nil {
		r.logger.Error("failed to resolve config", "error", err.Error())
		return
	}

	// 4. Diff: new and changed families
	for family, newCfg := range newDesired {
		if _, exists := r.desired[family]; !exists {
			// New family
			td, regErr := r.registerTaskDef(ctx, newCfg)
			if regErr != nil {
				r.logger.Error("failed to register task definition", "family", family, "error", regErr.Error())
				continue
			}
			r.events <- ReconcileEvent{Kind: EventCreate, Family: family, Config: newCfg, TaskDefinition: td}
		} else {
			// Existing family -- re-register (ECS creates a new revision only if changed)
			td, regErr := r.registerTaskDef(ctx, newCfg)
			if regErr != nil {
				r.logger.Error("failed to register task definition", "family", family, "error", regErr.Error())
				continue
			}
			r.events <- ReconcileEvent{Kind: EventUpdate, Family: family, Config: newCfg, TaskDefinition: td}
		}
	}

	// 5. Diff: removed families
	for family := range r.desired {
		if _, exists := newDesired[family]; !exists {
			r.events <- ReconcileEvent{Kind: EventRemove, Family: family}
		}
	}

	// 6. Update state
	r.desired = newDesired
	r.lastVersion = version
}

func (r *Reconciler) reconcileStartup(ctx context.Context) {
	// Fetch and parse
	param, err := r.ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(r.paramName),
	})
	if err != nil {
		r.logger.Error("failed to get SSM parameter on startup", "error", err.Error())
		return
	}

	cfg, err := tomlcfg.Parse([]byte(aws.ToString(param.Parameter.Value)))
	if err != nil {
		r.logger.Error("failed to parse TOML config on startup", "error", err.Error())
		return
	}
	newDesired, err := tomlcfg.Resolve(cfg)
	if err != nil {
		r.logger.Error("failed to resolve config on startup", "error", err.Error())
		return
	}

	// For each family in TOML, check ECS state
	for family, newCfg := range newDesired {
		existing, descErr := r.ecsClient.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
			TaskDefinition: aws.String(family),
			Include:        []ecsTypes.TaskDefinitionField{ecsTypes.TaskDefinitionFieldTags},
		})
		if descErr != nil {
			// Does not exist -- register as new
			td, regErr := r.registerTaskDef(ctx, newCfg)
			if regErr != nil {
				r.logger.Error("failed to register new task definition", "family", family, "error", regErr.Error())
				continue
			}
			r.events <- ReconcileEvent{Kind: EventCreate, Family: family, Config: newCfg, TaskDefinition: td}
			continue
		}

		// Exists -- check managed tag
		if !hasManaged(existing.Tags) {
			r.logger.Error("task definition exists but is not managed by ecs-arc, skipping",
				"family", family)
			continue
		}

		// Managed -- register new revision
		td, regErr := r.registerTaskDef(ctx, newCfg)
		if regErr != nil {
			r.logger.Error("failed to register task definition revision", "family", family, "error", regErr.Error())
			continue
		}
		r.events <- ReconcileEvent{Kind: EventCreate, Family: family, Config: newCfg, TaskDefinition: td}
	}

	// Orphan cleanup: find managed families not in TOML
	r.cleanupOrphans(ctx, newDesired)

	r.desired = newDesired
	r.lastVersion = param.Parameter.Version
}

func (r *Reconciler) cleanupOrphans(ctx context.Context, desired map[string]*tomlcfg.ResolvedRunnerConfig) {
	var nextToken *string
	for {
		output, err := r.ecsClient.ListTaskDefinitionFamilies(ctx, &ecs.ListTaskDefinitionFamiliesInput{
			Status:    ecsTypes.TaskDefinitionFamilyStatusActive,
			NextToken: nextToken,
		})
		if err != nil {
			r.logger.Error("failed to list task definition families", "error", err.Error())
			return
		}
		for _, family := range output.Families {
			if _, inDesired := desired[family]; inDesired {
				continue
			}
			// Check if managed by ecs-arc
			desc, descErr := r.ecsClient.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
				TaskDefinition: aws.String(family),
				Include:        []ecsTypes.TaskDefinitionField{ecsTypes.TaskDefinitionFieldTags},
			})
			if descErr != nil {
				continue
			}
			if hasManaged(desc.Tags) {
				r.logger.Info("cleaning up orphaned managed task definition", "family", family)
				r.events <- ReconcileEvent{Kind: EventRemove, Family: family}
			}
		}
		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}
}

func (r *Reconciler) registerTaskDef(ctx context.Context, cfg *tomlcfg.ResolvedRunnerConfig) (*ecsTypes.TaskDefinition, error) {
	input := BuildRegisterInput(cfg, r.infra)
	output, err := r.ecsClient.RegisterTaskDefinition(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("register task definition %q: %w", cfg.Family, err)
	}
	r.logger.Info("registered task definition",
		slog.String("family", cfg.Family),
		slog.Int("revision", int(output.TaskDefinition.Revision)),
	)
	return output.TaskDefinition, nil
}

func hasManaged(tags []ecsTypes.Tag) bool {
	for _, tag := range tags {
		if aws.ToString(tag.Key) == "ecs-arc:managed" && aws.ToString(tag.Value) == "true" {
			return true
		}
	}
	return false
}
