# ecs-arc

A Go-based controller that autoscales GitHub Actions self-hosted runners as ECS tasks. It uses the [`actions/scaleset`](https://github.com/actions/scaleset) library to replace the Kubernetes dependency of `actions-runner-controller` with Amazon ECS, supporting EC2 Managed Instances, ECS Anywhere (EXTERNAL), and Fargate Spot launch types.

## Prerequisites

- A [GitHub App](https://docs.github.com/en/apps/creating-github-apps) installed on your organization with the following permissions:
  - **Organization permissions**: Self-hosted runners (Read & Write)
- An AWS account with an ECS cluster
- ECS task definitions for your runner sizes (the controller reads these at startup)
- The official ARC runner image: `ghcr.io/actions/actions-runner`

## Quick Start

1. Store your GitHub App private key in AWS Secrets Manager:

```bash
aws secretsmanager create-secret \
  --name "ecs-arc/github-app-private-key" \
  --secret-string file://private-key.pem
```

2. Obtain the CloudFormation template. Download the latest `template.yaml` from the [releases page](https://github.com/niranjan94/ecs-arc/releases), or generate it locally:

```bash
go run ./cmd/ecs-arc generate-template -o template.yaml
```

3. Deploy the CloudFormation template.

**ECS Anywhere (EXTERNAL)** -- no VPC/subnets/security groups needed:

```bash
aws cloudformation deploy \
  --template-file template.yaml \
  --stack-name ecs-arc \
  --parameter-overrides \
    EcsLaunchType=EXTERNAL \
    GitHubAppClientId=Iv1.abc123 \
    GitHubAppInstallationId=12345 \
    GitHubAppPrivateKeyArn=arn:aws:secretsmanager:us-east-1:123456789:secret:gh-app-key-AbCdEf \
    GitHubOrg=my-org \
    ExistingClusterName=my-cluster \
  --capabilities CAPABILITY_NAMED_IAM
```

**Fargate Spot** (default):

```bash
aws cloudformation deploy \
  --template-file template.yaml \
  --stack-name ecs-arc \
  --parameter-overrides \
    GitHubAppClientId=Iv1.abc123 \
    GitHubAppInstallationId=12345 \
    GitHubAppPrivateKeyArn=arn:aws:secretsmanager:us-east-1:123456789:secret:gh-app-key-AbCdEf \
    GitHubOrg=my-org \
    PrivateSubnetIds=subnet-aaa,subnet-bbb \
    ServiceSecurityGroupId=sg-xxx \
  --capabilities CAPABILITY_NAMED_IAM
```

4. Target runners in your workflows:

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
| `ECS_SUBNETS` | No | Comma-separated subnet IDs (required for Fargate/Managed Instances) |
| `ECS_SECURITY_GROUPS` | No | Comma-separated security group IDs (required for Fargate/Managed Instances) |
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

## IAM Permissions

The CloudFormation template creates all required IAM roles automatically. If you are setting up IAM manually, the controller task role needs the following permissions:

### Controller Task Role

The controller needs to manage ECS runner tasks and describe task definitions. `ecs:TagResource` is required because `RunTask` applies tags (`ecs-arc:scale-set`, `ecs-arc:runner-name`) and propagates task definition tags to each task. Cluster-scoped actions are restricted to the target cluster; `DescribeTaskDefinition` is account-global and cannot be cluster-scoped.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "EcsClusterScoped",
      "Effect": "Allow",
      "Action": [
        "ecs:RunTask",
        "ecs:StopTask",
        "ecs:DescribeTasks",
        "ecs:ListTasks",
        "ecs:TagResource"
      ],
      "Resource": "*",
      "Condition": {
        "ArnEquals": {
          "ecs:cluster": "arn:aws:ecs:REGION:ACCOUNT_ID:cluster/CLUSTER_NAME"
        }
      }
    },
    {
      "Sid": "EcsDescribeTaskDefinitions",
      "Effect": "Allow",
      "Action": [
        "ecs:DescribeTaskDefinition"
      ],
      "Resource": "*"
    },
    {
      "Sid": "PassRunnerRoles",
      "Effect": "Allow",
      "Action": "iam:PassRole",
      "Resource": [
        "arn:aws:iam::ACCOUNT_ID:role/RUNNER_EXECUTION_ROLE",
        "arn:aws:iam::ACCOUNT_ID:role/RUNNER_TASK_ROLE"
      ]
    }
  ]
}
```

### Controller Execution Role

The execution role needs the standard ECS task execution policy plus access to the Secrets Manager secret containing the GitHub App private key:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SecretsAccess",
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:REGION:ACCOUNT_ID:secret:SECRET_NAME"
    }
  ]
}
```

This is in addition to the AWS managed policy `AmazonECSTaskExecutionRolePolicy` which handles ECR image pulls and CloudWatch Logs.

### Runner Execution Role

Attach the AWS managed policy `AmazonECSTaskExecutionRolePolicy`. No additional permissions are needed unless your runner image is in a private ECR registry that requires cross-account access.

### Runner Task Role

Add permissions based on what your GitHub Actions workflows need (e.g. S3 access, ECR push, etc.). The template creates this role with no extra policies -- customize it for your workloads.

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
go build ./cmd/ecs-arc
```

### Running Locally

```bash
export GITHUB_APP_CLIENT_ID=Iv1.abc123
export GITHUB_APP_INSTALLATION_ID=12345
export GITHUB_APP_PRIVATE_KEY="$(cat path/to/private-key.pem)"
export GITHUB_ORG=my-org
export ECS_CLUSTER=my-cluster
export TASK_DEFINITIONS=runner-small

# For Fargate/Managed Instances (awsvpc networking):
export ECS_SUBNETS=subnet-aaa
export ECS_SECURITY_GROUPS=sg-xxx

go run ./cmd/ecs-arc controller
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
