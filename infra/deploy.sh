#!/usr/bin/env bash
# Deploys a release of main to the application stack, with the AWS credentials
# of whoever runs it. CI only builds: nothing in GitHub can change the stack.
#
#   infra/deploy.sh v1.0       a release tag
#   infra/deploy.sh 34efeee    any commit of main, e.g. to roll back
set -euo pipefail

STACK=${STACK:-messenger-app}
BOOTSTRAP_STACK=${BOOTSTRAP_STACK:-messenger-bootstrap}
REPOSITORY=messenger
PARAMETER_PREFIX=${PARAMETER_PREFIX:-/messenger}

die() { echo "deploy: $*" >&2; exit 1; }

[ $# -eq 1 ] || die "usage: $0 <release tag | commit of main>"
ref=$1
printf '%s' "$ref" | grep -Eq '^(v[0-9A-Za-z._-]+|[0-9a-f]{7,40})$' \
  || die "not a release tag or a commit hash: $ref"

cd "$(git rev-parse --show-toplevel)"

# main as the remote has it, not the local branch, which may be behind or carry
# commits nobody has reviewed.
upstream=$(git rev-parse --abbrev-ref 'main@{upstream}' 2>/dev/null) \
  || die "local main tracks no remote branch"
git fetch --quiet --tags "${upstream%%/*}"

sha=$(git rev-parse --verify --quiet "$ref^{commit}") || die "no such tag or commit: $ref"
git merge-base --is-ancestor "$sha" "$upstream" || die "$ref is not on $upstream"

# The template comes from the remote main as well, never from the working tree,
# which may be another branch or hold uncommitted edits. On a rollback this
# keeps the infrastructure current and takes only the image back.
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
template="$workdir/app.yaml"
git show "$upstream:infra/app.yaml" > "$template"
template_commit=$(git rev-parse --short "$upstream")

release=""
if git show-ref --verify --quiet "refs/tags/$ref"; then
  release=$ref
fi

# The image is named after the commit resolved here, never looked up by the
# release tag in ECR: a tag there could have been pushed by anyone with the
# push role, and only git says which commit a release is.
image_tag="sha-$sha"
digest=$(aws ecr describe-images --repository-name "$REPOSITORY" \
           --image-ids imageTag="$image_tag" \
           --query 'imageDetails[0].imageDigest' --output text 2>/dev/null) \
  || die "no image $image_tag in ECR; CI pushes it once the commit passes on main"

cfn_role=$(aws cloudformation describe-stacks --stack-name "$BOOTSTRAP_STACK" \
             --query "Stacks[0].Outputs[?OutputKey=='CfnExecutionRoleArn'].OutputValue" \
             --output text)
certificate=$(aws ssm get-parameter --name "$PARAMETER_PREFIX/certificate-arn" \
                --query Parameter.Value --output text)
account=$(aws sts get-caller-identity --query Account --output text)
region=$(aws configure get region || true)

echo "Commit:   $sha ${release:+($release)}"
echo "Image:    $image_tag ($digest)"
echo "Template: infra/app.yaml at $upstream ($template_commit)"
echo "Stack:    $STACK in account $account, region ${region:-from the environment}"
read -r -p "Deploy? [y/N] " answer
[ "$answer" = y ] || die "cancelled"

# The release tag in ECR is for people and for the lifecycle policy, which keeps
# v* images for good. Tags there are immutable: one already on another image
# stops the deploy instead of moving.
if [ -n "$release" ]; then
  existing=$(aws ecr describe-images --repository-name "$REPOSITORY" \
               --image-ids imageTag="$release" \
               --query 'imageDetails[0].imageDigest' --output text 2>/dev/null || true)
  if [ -z "$existing" ]; then
    manifest=$(aws ecr batch-get-image --repository-name "$REPOSITORY" \
                 --image-ids imageTag="$image_tag" \
                 --query 'images[0].imageManifest' --output text)
    media_type=$(aws ecr batch-get-image --repository-name "$REPOSITORY" \
                   --image-ids imageTag="$image_tag" \
                   --query 'images[0].imageManifestMediaType' --output text)
    aws ecr put-image --repository-name "$REPOSITORY" --image-tag "$release" \
      --image-manifest "$manifest" --image-manifest-media-type "$media_type" >/dev/null
  elif [ "$existing" != "$digest" ]; then
    die "$release in ECR already names another image ($existing)"
  fi
fi

# Waits until ECS reports the service stable. A version whose tasks keep failing
# is rolled back by the circuit breaker, and the stack update fails with it.
aws cloudformation deploy \
  --stack-name "$STACK" \
  --template-file "$template" \
  --role-arn "$cfn_role" \
  --parameter-overrides ImageTag="$image_tag" CertificateArn="$certificate" \
  --no-fail-on-empty-changeset

aws cloudformation describe-stacks --stack-name "$STACK" \
  --query "Stacks[0].Outputs[?OutputKey=='LoadBalancerDns'].OutputValue" --output text
