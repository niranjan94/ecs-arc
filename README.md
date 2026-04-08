# ecs-arc

A Go-based controller that autoscales GitHub Actions self-hosted runners as ECS tasks. It uses the [`actions/scaleset`](https://github.com/actions/scaleset) library to replace the Kubernetes dependency of `actions-runner-controller` with Amazon ECS, supporting EC2 Managed Instances, ECS Anywhere (EXTERNAL), and Fargate Spot launch types.

## Prerequisites

- A [GitHub App](https://docs.github.com/en/apps/creating-github-apps) installed on your organization with the following permissions:
  - **Organization permissions**: Self-hosted runners (Read & Write)
- An AWS account with an ECS cluster
- ECS task definitions for your runner sizes (the controller reads these at startup)
- The official ARC runner image: `ghcr.io/actions/actions-runner-dind`

## Quick Start

1. Store your GitHub App private key in AWS Secrets Manager
2. Deploy the CloudFormation template:

```bash
aws cloudformation deploy \
  --template-file deploy/template.yaml \
  --stack-name ecs-arc \
  --parameter-overrides \
    GitHubAppClientId=Iv1.abc123 \
    GitHubAppInstallationId=12345 \
    GitHubAppPrivateKeyArn=arn:aws:secretsmanager:us-east-1:123456789:secret:gh-app-key \
    GitHubOrg=my-org \
    ControllerImageUri=ghcr.io/niranjan94/ecs-arc:latest \
    VpcId=vpc-xxx \
    PrivateSubnetIds=subnet-aaa,subnet-bbb \
    ServiceSecurityGroupId=sg-xxx \
  --capabilities CAPABILITY_NAMED_IAM
```

3. Target runners in your workflows:

```yaml
jobs:
  build:
    runs-on: runner-small  # matches the task definition family name
```

## Configuration

### Environment Variables

| Variable | Required | Description |
|---|---|---|
| `GITHUB_APP_CLIENT_ID` | Yes | GitHub App Client ID |
| `GITHUB_APP_INSTALLATION_ID` | Yes | GitHub App Installation ID |
| `GITHUB_APP_PRIVATE_KEY` | Yes | PEM-encoded GitHub App private key |
| `GITHUB_ORG` | Yes | GitHub organization name |
| `ECS_CLUSTER` | Yes | ECS cluster name or ARN |
| `ECS_SUBNETS` | Yes | Comma-separated subnet IDs for awsvpc mode |
| `ECS_SECURITY_GROUPS` | Yes | Comma-separated security group IDs |
| `TASK_DEFINITIONS` | Yes | Comma-separated ECS task definition family names |
| `ECS_CAPACITY_PROVIDER` | No | Capacity provider name (omit for Fargate or EXTERNAL) |
| `SCALESET_NAME_PREFIX` | No | Prefix for scale set names (e.g. `prod` -> `prod-runner-small`) |

### Task Definition Tags

Per-scale-set configuration is set via tags on the ECS task definition. All tags use the `ecs-arc:` prefix.

| Tag | Default | Description |
|---|---|---|
| `ecs-arc:max-runners` | `10` | Maximum concurrent runners for this scale set |
| `ecs-arc:min-runners` | `0` | Minimum idle runners to maintain |
| `ecs-arc:max-runtime` | `6h` | Maximum time a runner task can run before being stopped |
| `ecs-arc:subnets` | Global config | Override subnets for this scale set |
| `ecs-arc:security-groups` | Global config | Override security groups for this scale set |
| `ecs-arc:capacity-provider` | Global config | Override capacity provider for this scale set |

## Architecture

The controller runs as a single ECS service (desired count 1) and spawns one goroutine per configured scale set:

```
main process
  |- goroutine: scale set "runner-small"  -> listener.Run(ctx, scaler)
  |- goroutine: scale set "runner-medium" -> listener.Run(ctx, scaler)
  |- goroutine: scale set "runner-large"  -> listener.Run(ctx, scaler)
```

### Scaling Flow

1. The `listener` polls GitHub for desired runner count via long-polling message sessions
2. `HandleDesiredRunnerCount` computes the target (clamped by min/max), then calls `GenerateJitRunnerConfig` + `ecs:RunTask` for each new runner needed
3. The JIT config is injected via the `ACTIONS_RUNNER_INPUT_JITCONFIG` environment variable as a container override
4. When a job starts, `HandleJobStarted` marks the runner as busy
5. When a job completes, `HandleJobCompleted` removes the runner from tracking; the ephemeral ECS task exits on its own
6. A reaper goroutine periodically stops tasks stuck in PENDING (>5min) or exceeding max runtime

### Workflow Targeting

Scale set names become runner labels. With `TASK_DEFINITIONS=runner-small,runner-large` and `SCALESET_NAME_PREFIX=prod`:

```yaml
runs-on: prod-runner-small   # routes to the runner-small task definition
runs-on: prod-runner-large   # routes to the runner-large task definition
```

Without a prefix, the task definition family name is used directly as the label.

## Development

### Building

```bash
go build ./cmd/controller
```

### Running Locally

```bash
export GITHUB_APP_CLIENT_ID=Iv1.abc123
export GITHUB_APP_INSTALLATION_ID=12345
export GITHUB_APP_PRIVATE_KEY="$(cat path/to/private-key.pem)"
export GITHUB_ORG=my-org
export ECS_CLUSTER=my-cluster
export ECS_SUBNETS=subnet-aaa
export ECS_SECURITY_GROUPS=sg-xxx
export TASK_DEFINITIONS=runner-small

go run ./cmd/controller
```

### Testing

```bash
go test ./... -v -race
```

### Docker

Pre-built multi-arch images (linux/amd64, linux/arm64) are published to GitHub Container Registry:

```bash
# Latest tip from main
docker pull ghcr.io/niranjan94/ecs-arc:tip

# Specific release
docker pull ghcr.io/niranjan94/ecs-arc:1.0.0

# Latest stable release
docker pull ghcr.io/niranjan94/ecs-arc:latest
```

To build locally:

```bash
docker build -t ecs-arc:dev .
```

## License

See [LICENSE](LICENSE) for details.
