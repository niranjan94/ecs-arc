// Package reconciler manages dynamic ECS task definition lifecycle from
// TOML configuration stored in SSM Parameter Store.
package reconciler

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/niranjan94/ecs-arc/internal/tomlcfg"
)

// InfraConfig holds environment-level config not stored in TOML.
type InfraConfig struct {
	ExecutionRoleARN string
	TaskRoleARN      string
	LogGroup         string
	Region           string
}

// BuildRegisterInput creates an ecs.RegisterTaskDefinitionInput from a
// resolved runner config and infrastructure config.
func BuildRegisterInput(cfg *tomlcfg.ResolvedRunnerConfig, infra InfraConfig) *ecs.RegisterTaskDefinitionInput {
	input := &ecs.RegisterTaskDefinitionInput{
		Family:      aws.String(cfg.Family),
		Cpu:         aws.String(fmt.Sprintf("%d", cfg.CPU)),
		Memory:      aws.String(fmt.Sprintf("%d", cfg.Memory)),
		NetworkMode: mapNetworkMode(cfg.NetworkMode),
		RequiresCompatibilities: []ecsTypes.Compatibility{
			mapCompatibility(cfg.Compatibility),
		},
		ExecutionRoleArn: aws.String(infra.ExecutionRoleARN),
		TaskRoleArn:      aws.String(infra.TaskRoleARN),
		Tags: []ecsTypes.Tag{
			{Key: aws.String("ecs-arc:managed"), Value: aws.String("true")},
		},
	}

	// RuntimePlatform
	if cfg.OS != "" {
		rp := &ecsTypes.RuntimePlatform{
			OperatingSystemFamily: ecsTypes.OSFamily(cfg.OS),
		}
		if cfg.Architecture != "" {
			rp.CpuArchitecture = ecsTypes.CPUArchitecture(cfg.Architecture)
		}
		input.RuntimePlatform = rp
	}

	switch {
	case cfg.EnableDind && cfg.DindMode == tomlcfg.DindModeInline:
		input.Volumes = []ecsTypes.Volume{{Name: aws.String("docker-data")}}
		input.ContainerDefinitions = inlineDindContainers(cfg, infra)
	case cfg.EnableDind:
		input.Volumes = dindVolumes()
		input.ContainerDefinitions = dindContainers(cfg, infra)
	default:
		input.ContainerDefinitions = standaloneContainers(cfg, infra)
	}

	return input
}

func mapNetworkMode(mode string) ecsTypes.NetworkMode {
	switch mode {
	case "awsvpc":
		return ecsTypes.NetworkModeAwsvpc
	case "bridge":
		return ecsTypes.NetworkModeBridge
	default:
		return ecsTypes.NetworkMode(mode)
	}
}

func mapCompatibility(compat string) ecsTypes.Compatibility {
	switch compat {
	case "FARGATE":
		return ecsTypes.CompatibilityFargate
	case "EC2":
		return ecsTypes.CompatibilityEc2
	case "EXTERNAL":
		return ecsTypes.CompatibilityExternal
	default:
		return ecsTypes.Compatibility(compat)
	}
}

func logConfig(infra InfraConfig, streamPrefix string) *ecsTypes.LogConfiguration {
	return &ecsTypes.LogConfiguration{
		LogDriver: "awslogs",
		Options: map[string]string{
			"awslogs-group":         infra.LogGroup,
			"awslogs-region":        infra.Region,
			"awslogs-stream-prefix": streamPrefix,
		},
	}
}

func dindVolumes() []ecsTypes.Volume {
	return []ecsTypes.Volume{
		{Name: aws.String("dind-sock")},
		{Name: aws.String("dind-externals")},
		{Name: aws.String("work")},
	}
}

func standaloneContainers(cfg *tomlcfg.ResolvedRunnerConfig, infra InfraConfig) []ecsTypes.ContainerDefinition {
	return []ecsTypes.ContainerDefinition{
		{
			Name:             aws.String("runner"),
			Image:            aws.String(cfg.RunnerImage),
			Essential:        aws.Bool(true),
			Command:          []string{"/home/runner/run.sh"},
			LogConfiguration: logConfig(infra, cfg.Family),
		},
	}
}

func dindContainers(cfg *tomlcfg.ResolvedRunnerConfig, infra InfraConfig) []ecsTypes.ContainerDefinition {
	return []ecsTypes.ContainerDefinition{
		// init-dind-externals
		{
			Name:      aws.String("init-dind-externals"),
			Image:     aws.String(cfg.RunnerImage),
			Essential: aws.Bool(false),
			User:      aws.String("0"),
			Command:   []string{"sh", "-c", "cp -a /home/runner/externals/. /home/runner/tmpDir/ && chown -R 1001:123 /home/runner/tmpDir /home/runner/_work"},
			MountPoints: []ecsTypes.MountPoint{
				{SourceVolume: aws.String("dind-externals"), ContainerPath: aws.String("/home/runner/tmpDir")},
				{SourceVolume: aws.String("work"), ContainerPath: aws.String("/home/runner/_work")},
			},
			LogConfiguration: logConfig(infra, cfg.Family+"-init"),
		},
		// dind sidecar
		{
			Name:       aws.String("dind"),
			Image:      aws.String(cfg.DindImage),
			Essential:  aws.Bool(true),
			Privileged: aws.Bool(true),
			Command:    []string{"dockerd", "--host=unix:///var/run/docker.sock", "--group=123"},
			HealthCheck: &ecsTypes.HealthCheck{
				Command:     []string{"CMD", "docker", "info"},
				StartPeriod: aws.Int32(120),
				Interval:    aws.Int32(5),
				Timeout:     aws.Int32(5),
				Retries:     aws.Int32(3),
			},
			MountPoints: []ecsTypes.MountPoint{
				{SourceVolume: aws.String("work"), ContainerPath: aws.String("/home/runner/_work")},
				{SourceVolume: aws.String("dind-sock"), ContainerPath: aws.String("/var/run")},
				{SourceVolume: aws.String("dind-externals"), ContainerPath: aws.String("/home/runner/externals")},
			},
			LogConfiguration: logConfig(infra, cfg.Family+"-dind"),
		},
		// runner
		{
			Name:      aws.String("runner"),
			Image:     aws.String(cfg.RunnerImage),
			Essential: aws.Bool(true),
			Command:   []string{"/home/runner/run.sh"},
			Environment: []ecsTypes.KeyValuePair{
				{Name: aws.String("DOCKER_HOST"), Value: aws.String("unix:///var/run/docker.sock")},
				{Name: aws.String("RUNNER_WAIT_FOR_DOCKER_IN_SECONDS"), Value: aws.String("120")},
			},
			DependsOn: []ecsTypes.ContainerDependency{
				{ContainerName: aws.String("init-dind-externals"), Condition: ecsTypes.ContainerConditionSuccess},
				{ContainerName: aws.String("dind"), Condition: ecsTypes.ContainerConditionHealthy},
			},
			MountPoints: []ecsTypes.MountPoint{
				{SourceVolume: aws.String("work"), ContainerPath: aws.String("/home/runner/_work")},
				{SourceVolume: aws.String("dind-sock"), ContainerPath: aws.String("/var/run")},
			},
			LogConfiguration: logConfig(infra, cfg.Family),
		},
	}
}

// inlineDindScript starts dockerd inside the runner container, waits for it,
// then hands over to the runner. It relies on the runner image shipping dockerd
// and iptables and granting the runner user passwordless sudo, as the default
// runner image does.
const inlineDindScript = `set -e
# cgroup v2: move this container's processes into a child cgroup and delegate
# every controller to its children, as moby's hack/dind does. Without it runc
# fails with "cannot enter cgroupv2 ... in threaded mode" when a job starts a
# container with resource limits (for example docker compose with mem_limit).
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
  sudo sh -c 'mkdir -p /sys/fs/cgroup/init \
    && xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs || true; \
    sed -e "s/ / +/g" -e "s/^/+/" < /sys/fs/cgroup/cgroup.controllers > /sys/fs/cgroup/cgroup.subtree_control'
fi
set +e
# DOCKERD_ARGS (optional, e.g. set in the runner image) adds dockerd flags such
# as --registry-mirror.
sudo dockerd --host=unix:///var/run/docker.sock --group=123 --data-root=/var/lib/docker ${DOCKERD_ARGS:-} >/tmp/dockerd.log 2>&1 &
for i in $(seq 120); do docker info >/dev/null 2>&1 && break; sleep 1; done
docker info >/dev/null 2>&1 || { echo "dockerd did not start:"; cat /tmp/dockerd.log; exit 1; }
exec /home/runner/run.sh`

// inlineDindContainers runs dockerd inside the privileged runner container
// instead of a sidecar. Job containers then live in the runner's network
// namespace and see the runner's filesystem, which a sidecar cannot offer in
// bridge network mode: there every task container has its own namespace, so
// "services:" are not on the runner's localhost and "docker run -v /tmp/x:..."
// resolves /tmp/x on the sidecar.
func inlineDindContainers(cfg *tomlcfg.ResolvedRunnerConfig, infra InfraConfig) []ecsTypes.ContainerDefinition {
	return []ecsTypes.ContainerDefinition{
		{
			Name:       aws.String("runner"),
			Image:      aws.String(cfg.RunnerImage),
			Essential:  aws.Bool(true),
			Privileged: aws.Bool(true),
			Command:    []string{"bash", "-c", inlineDindScript},
			Environment: []ecsTypes.KeyValuePair{
				{Name: aws.String("DOCKER_HOST"), Value: aws.String("unix:///var/run/docker.sock")},
			},
			// dockerd's data root must not sit on the container's overlay filesystem.
			MountPoints: []ecsTypes.MountPoint{
				{SourceVolume: aws.String("docker-data"), ContainerPath: aws.String("/var/lib/docker")},
			},
			LogConfiguration: logConfig(infra, cfg.Family),
		},
	}
}
