# ecs-arc

A Go-based controller that autoscales GitHub Actions self-hosted runners as ECS tasks. It uses the [`actions/scaleset`](https://github.com/actions/scaleset) library to replace the Kubernetes dependency of `actions-runner-controller` with Amazon ECS, supporting Fargate, Fargate Spot, ECS Anywhere (EXTERNAL), and EC2-backed launch modes (including EC2 Managed Instances via a capacity provider).

## Prerequisites

- A [GitHub App](https://docs.github.com/en/apps/creating-github-apps) installed on your organization with the following permissions:
  - **Organization permissions**: Self-hosted runners (Read & Write)
- An AWS account with an ECS cluster (the CloudFormation template can create one for you)
- A runner container image. ecs-arc defaults to `ghcr.io/niranjan94/ecs-arc-runner:latest`, a downstream of the official `ghcr.io/actions/actions-runner` with `git`, `zstd`, `jq`, `gh`, `yq` and other common CLI tools pre-installed so stock GitHub-published actions work out of the box. See [`runner/README.md`](runner/README.md) for what is in it, why it exists, and how to use your own image instead.

Runner task definitions are registered and deregistered automatically from the TOML config. You do not need to create them ahead of time.

## Quick Start

1. Store your GitHub App private key in AWS Secrets Manager:

```bash
aws secretsmanager create-secret \
  --name "ecs-arc/github-app-private-key" \
  --secret-string file://private-key.pem
```

2. Grab [`deploy/template.yaml`](deploy/template.yaml) from this repo (it's a static CloudFormation template -- no generator, no release asset). Pin to a tag if you want a specific version.

3. Deploy the CloudFormation template. The stack creates an empty SSM Parameter (`/prod/ecs-arc/runners` by default) that you will populate in step 4.

**ECS Anywhere (EXTERNAL)** controller -- no VPC/subnets/security groups needed for the controller itself:

```bash
aws cloudformation deploy \
  --template-file template.yaml \
  --stack-name ecs-arc \
  --parameter-overrides \
    ControllerLaunchType=EXTERNAL \
    GitHubAppClientId=Iv1.abc123 \
    GitHubAppId=1234567 \
    GitHubAppInstallationId=12345 \
    GitHubAppPrivateKeyArn=arn:aws:secretsmanager:us-east-1:123456789:secret:gh-app-key-AbCdEf \
    GitHubOrg=my-org \
    ExistingClusterName=my-cluster \
  --capabilities CAPABILITY_NAMED_IAM
```

**Fargate Spot** controller (default):

```bash
aws cloudformation deploy \
  --template-file template.yaml \
  --stack-name ecs-arc \
  --parameter-overrides \
    GitHubAppClientId=Iv1.abc123 \
    GitHubAppId=1234567 \
    GitHubAppInstallationId=12345 \
    GitHubAppPrivateKeyArn=arn:aws:secretsmanager:us-east-1:123456789:secret:gh-app-key-AbCdEf \
    GitHubOrg=my-org \
    PrivateSubnetIds=subnet-aaa,subnet-bbb \
    ServiceSecurityGroupId=sg-xxx \
  --capabilities CAPABILITY_NAMED_IAM
```

`ControllerLaunchType` only affects how the **controller** itself runs. Runner tasks are configured per-scale-set in the TOML (see step 4) and are independent of this setting. Each runner's `compatibility` is one of `FARGATE`, `EC2`, or `EXTERNAL`; Fargate Spot and EC2 Managed Instances are selected by also setting `capacity_provider` on the runner (for example, `compatibility = "FARGATE"` + `capacity_provider = "FARGATE_SPOT"`).

Optional stack parameters you may want to override:

| Parameter | Default | Purpose |
|---|---|---|
| `Environment` | `prod` | Prefix for all resource names created by the stack. |
| `ControllerImageUri` | `ghcr.io/niranjan94/ecs-arc:latest` | Controller container image. Pin to a specific tag for reproducible deploys. |
| `SSMParameterName` | `/prod/ecs-arc/runners` | Full path of the SSM parameter that holds the TOML runner config. Must start with `/`. |
| `RunnerExtraLabels` | `""` | Comma-separated GitHub labels added to every runner scale set. |
| `RunnerLogGroupRetentionDays` | `14` | CloudWatch retention for the runner log group. |

4. Populate the SSM parameter with your runner TOML config. Start from [`deploy/sample-runners.toml`](deploy/sample-runners.toml):

```bash
aws ssm put-parameter \
  --name "/prod/ecs-arc/runners" \
  --type String \
  --overwrite \
  --value "$(cat runners.toml)"
```

The controller polls this parameter every `SSM_POLL_INTERVAL` (default 5m) and reconciles ECS task definitions to match. New `[[runner]]` or `[[template]]` entries spawn scale sets; removed entries deregister them.

5. Target runners in your workflows using the scale set name (task definition family name, optionally prefixed by `SCALESET_NAME_PREFIX`):

```yaml
jobs:
  build:
    runs-on: runner-small
```

## Configuration

### Environment Variables

The CloudFormation template sets all of these on the controller task. Only set them manually if you are running the controller outside the provided template.

| Variable | Required | Description |
|---|---|---|
| `GITHUB_APP_CLIENT_ID` | Yes | GitHub App Client ID |
| `GITHUB_APP_ID` | Yes | Numeric GitHub App ID (distinct from `GITHUB_APP_CLIENT_ID`, which is the `Iv23li...` string identifier). Shown as "App ID" at the top of the GitHub App settings page. |
| `GITHUB_APP_INSTALLATION_ID` | Yes | GitHub App Installation ID |
| `GITHUB_APP_PRIVATE_KEY` | Yes | PEM-encoded GitHub App private key |
| `GITHUB_ORG` | Yes | GitHub organization name |
| `ECS_CLUSTER` | Yes | ECS cluster name or ARN where runner tasks will be launched |
| `SSM_PARAMETER_NAME` | One of | Full SSM parameter name holding the TOML runner config (must start with `/`). Mutually exclusive with `TOML_CONFIG_FILE`. |
| `TOML_CONFIG_FILE` | One of | Local filesystem path to the TOML runner config. Mutually exclusive with `SSM_PARAMETER_NAME`. Useful for local development. |
| `SSM_POLL_INTERVAL` | No | How often to poll the config source (SSM parameter or file) for changes (default `5m`) |
| `RUNNER_EXECUTION_ROLE_ARN` | Yes | IAM execution role ARN applied to dynamically-registered runner task definitions |
| `RUNNER_TASK_ROLE_ARN` | Yes | IAM task role ARN applied to dynamically-registered runner task definitions |
| `RUNNER_LOG_GROUP` | Yes | CloudWatch log group for runner containers |
| `RUNNER_EXTRA_LABELS` | No | Comma-separated extra GitHub labels applied to every scale set |
| `SCALESET_NAME_PREFIX` | No | Prefix for scale set names (e.g. `prod` -> `prod-runner-small`). Changes the GitHub label, not the ECS task definition family. |
| `OFFLINE_RUNNER_REAPER_INTERVAL` | No | How often the controller sweeps GitHub for stale runner registrations (default `30m`). |
| `OFFLINE_RUNNER_MIN_AGE` | No | Minimum time a runner must be observed offline before it is deregistered (default `1h`). |

### TOML Runner Configuration

All per-scale-set configuration (CPU, memory, min/max runners, subnets, security groups, capacity provider, launch type, DinD, max runtime, ...) lives in a TOML document. The controller reconciles ECS task definitions and GitHub scale sets to match. The TOML can be stored in SSM Parameter Store (production) or a local file (development); set `SSM_PARAMETER_NAME` or `TOML_CONFIG_FILE` respectively (exactly one).

See [`deploy/sample-runners.toml`](deploy/sample-runners.toml) for a working example. In short:

- `[defaults]` -- baseline applied to every runner (e.g. `compatibility`, `network_mode`, `max_runtime`).
- `[[runner]]` -- declares a single concrete runner variant.
- `[[template]]` -- expands `sizes × features` into many runner variants automatically, with optional `[template.exclude]` combinations.

A single one-off runner looks like this:

```toml
[[runner]]
family = "runner-gpu"
cpu = 4096
memory = 16384
max_runners = 2
compatibility = "EC2"
capacity_provider = "gpu-capacity-provider"
extra_labels = ["gpu"]
```

Updating the config source is the only way to add, remove, or resize scale sets at runtime.

## IAM Permissions

The CloudFormation template creates all required IAM roles automatically. If you are setting up IAM manually, the controller task role needs the following permissions:

### Controller Task Role

The controller manages runner ECS tasks **and** reconciles their task definitions from the TOML in SSM. The cluster-scoped `ecs:TagResource` lets `RunTask` apply per-task tags (`ecs-arc:scale-set`, `ecs-arc:runner-name`). `ecs:RegisterTaskDefinition`/`DeregisterTaskDefinition`/`ListTaskDefinitionFamilies` are account-global (not cluster-scoped) and are required for the reconciler; `ecs:TagResource` must also be allowed in that statement so the reconciler can tag task definitions at registration time with `ecs-arc:managed=true` (task definitions have no cluster context, so the cluster-condition form cannot satisfy it).

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
      "Sid": "EcsTaskDefinitionManagement",
      "Effect": "Allow",
      "Action": [
        "ecs:RegisterTaskDefinition",
        "ecs:DeregisterTaskDefinition",
        "ecs:DescribeTaskDefinition",
        "ecs:ListTaskDefinitionFamilies",
        "ecs:ListTaskDefinitions",
        "ecs:TagResource"
      ],
      "Resource": "*"
    },
    {
      "Sid": "SsmReadRunnerConfig",
      "Effect": "Allow",
      "Action": "ssm:GetParameter",
      "Resource": "arn:aws:ssm:REGION:ACCOUNT_ID:parameter/PATH/TO/RUNNERS"
    },
    {
      "Sid": "PassRunnerRoles",
      "Effect": "Allow",
      "Action": "iam:PassRole",
      "Resource": [
        "arn:aws:iam::ACCOUNT_ID:role/RUNNER_EXECUTION_ROLE",
        "arn:aws:iam::ACCOUNT_ID:role/RUNNER_TASK_ROLE"
      ],
      "Condition": {
        "StringEquals": {
          "iam:PassedToService": "ecs-tasks.amazonaws.com"
        }
      }
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

> **CMK-encrypted secrets.** If the GitHub App private-key secret is encrypted with a customer-managed KMS key (anything other than the AWS-managed `aws/secretsmanager` key), the execution role additionally needs `kms:Decrypt` on that CMK. The bundled CloudFormation template does **not** add this grant; either use the AWS-managed key, or extend the execution role yourself with something like:
>
> ```json
> { "Effect": "Allow", "Action": "kms:Decrypt", "Resource": "arn:aws:kms:REGION:ACCOUNT_ID:key/KEY_ID" }
> ```
>
> **SSM SecureString.** The CloudFormation template creates the runner-config SSM parameter as `Type: String`, so no KMS grant is needed to read it. If you switch the parameter to `SecureString` backed by a customer-managed KMS key, the controller task role will also need `kms:Decrypt` on that key.

### Runner Execution Role

Attach the AWS managed policy `AmazonECSTaskExecutionRolePolicy`. No additional permissions are needed unless your runner image is in a private ECR registry that requires cross-account access.

### Runner Task Role

Add permissions based on what your GitHub Actions workflows need (e.g. S3 access, ECR push, etc.). The template creates this role with no extra policies -- customize it for your workloads.

## Architecture

The controller runs as a single ECS service (desired count 1). A reconciler polls a pluggable `ConfigSource` (SSM parameter or local file) for the TOML config and emits events; the controller spawns one goroutine per active scale set in response:

```
main process
  |- reconciler        -> source.Fetch -> register/deregister task defs -> events
  |- goroutine: scale set "runner-small"  -> listener.Run(ctx, scaler)
  |- goroutine: scale set "runner-medium" -> listener.Run(ctx, scaler)
  |- goroutine: scale set "runner-large"  -> listener.Run(ctx, scaler)
```

### Reconciliation Flow

1. The reconciler reads the TOML from its configured source on startup and every `SSM_POLL_INTERVAL`. The source is either `SSMSource` (backed by `SSM_PARAMETER_NAME`, version token = parameter version) or `FileSource` (backed by `TOML_CONFIG_FILE`, version token = SHA-256 of contents).
2. Templates are expanded into concrete runner configs; the diff against observed state emits `Create` / `Update` / `Remove` events.
3. `Create` events trigger `RegisterTaskDefinition` and start a scale set goroutine. `Remove` deregisters the task definition, cancels the goroutine, and deletes the scale set on GitHub (only if it carries the `ecs-arc.managed` label). `Update` currently logs a warning -- configuration changes to a live scale set require a controller restart.
4. On startup, task definitions that are tagged as managed but no longer present in the TOML are deregistered, and managed scale sets on GitHub whose names no longer correspond to a desired family are deleted (orphan cleanup). Only resources carrying the `ecs-arc.managed` system label are ever deleted; foreign scale sets in the same runner group are left alone.

### Scaling Flow

1. The `listener` polls GitHub for desired runner count via long-polling message sessions.
2. `HandleDesiredRunnerCount` computes the target (clamped by min/max), then calls `GenerateJitRunnerConfig` + `ecs:RunTask` for each new runner needed.
3. The JIT config is injected via the `ACTIONS_RUNNER_INPUT_JITCONFIG` environment variable as a container override.
4. When a job starts, `HandleJobStarted` marks the runner as busy.
5. When a job completes, `HandleJobCompleted` removes the runner from tracking; the ephemeral ECS task exits on its own.
6. A reaper goroutine periodically stops tasks stuck in PENDING (>5min) or exceeding `max_runtime`.

### Workflow Targeting

The scale set name is the runner label used in workflows. It is derived from the TOML runner family, optionally prefixed by `SCALESET_NAME_PREFIX`. For example, with `SCALESET_NAME_PREFIX=prod` and a TOML family `runner-small`:

```yaml
runs-on: prod-runner-small
```

Without a prefix, the family name is used directly as the label.

### Runner registration cleanup

ecs-arc aggressively keeps GitHub's runner registrations in sync with actual ECS runner lifetime. Three layers work together:

1. **On job completion:** the scaler calls `RemoveRunner` for the runner that just finished, using the ID reported in the listener's `JobCompleted` message. Handles the common case with minimal latency.
2. **On task stop:** the per-scale-set reaper (already polling ECS every 30s) looks at tasks it observes in `STOPPED` state, resolves their runner name from the `ecs-arc:runner-name` task tag, and deregisters via `GetRunnerByName`+`RemoveRunner`. Covers crashes, reaper-killed tasks, and lost listener sessions.
3. **Global sweep:** a controller-owned goroutine lists org runners via the GitHub REST API every `OFFLINE_RUNNER_REAPER_INTERVAL` (default 30m) and deregisters any that have been `offline` for at least `OFFLINE_RUNNER_MIN_AGE` (default 1h) and whose name matches a currently-desired scale set. Backstop for runners missed by layers 1 and 2, pre-existing orphans, and scale sets deleted from TOML.

All three layers treat 404 (`RunnerNotFoundError`) as success and skip `JobStillRunningError` (never force-deregister a busy runner).

## Development

### Building

```bash
go build ./cmd/ecs-arc
```

### Running Locally

```bash
export GITHUB_APP_CLIENT_ID=Iv1.abc123
export GITHUB_APP_ID=1234567
export GITHUB_APP_INSTALLATION_ID=12345
export GITHUB_APP_PRIVATE_KEY="$(cat path/to/private-key.pem)"
export GITHUB_ORG=my-org

export ECS_CLUSTER=my-cluster

# Choose ONE of these (not both):
export SSM_PARAMETER_NAME=/dev/ecs-arc/runners
# export TOML_CONFIG_FILE=./deploy/sample-runners.toml

export RUNNER_EXECUTION_ROLE_ARN=arn:aws:iam::123456789012:role/runner-execution
export RUNNER_TASK_ROLE_ARN=arn:aws:iam::123456789012:role/runner-task
export RUNNER_LOG_GROUP=/ecs-arc/runners

go run ./cmd/ecs-arc controller
```

Subnets, security groups, and capacity providers are configured per runner in the TOML, not via environment variables.

### Testing

```bash
go test ./... -v -race
```

### Utility Scripts

#### delete-scalesets

One-shot utility for deleting GitHub Actions scale sets via the same auth path the controller uses. Useful when you need to clean up stale scale sets that the controller is not going to remove on its own (for example, after renaming `SCALESET_NAME_PREFIX`). Edit the `targets` slice in [`scripts/delete-scalesets/main.go`](scripts/delete-scalesets/main.go) to list the scale set names to delete, then:

```bash
# List-only (default): shows which scale sets would be deleted.
go run ./scripts/delete-scalesets \
  --org my-org \
  --client-id Iv1.abc123 \
  --installation-id 12345 \
  --private-key-file ./private-key.pem

# Actually delete. Only scale sets carrying the `ecs-arc.managed` label are
# removed; add --skip-managed-check to override that safety net.
go run ./scripts/delete-scalesets --apply ...
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
