# Deployment to AWS

Three CloudFormation stacks, from the longest-lived to the shortest:

| Stack | Template | Holds | Lifetime |
|---|---|---|---|
| `messenger-bootstrap` | `bootstrap.yaml` | ECR repository, NAT address, roles | kept |
| `messenger-network` | `network.yaml` | VPC, subnets, NAT gateway | for a demo |
| `messenger-app` | `app.yaml` | ECS cluster, Redis, application nodes, ALB | for a demo, updated by every deploy |

MongoDB is an Atlas cluster outside AWS.

## How a version ships

1. A commit lands on main. CI runs the checks, then `publish` builds the arm64
   image and pushes it to ECR as `sha-<commit>`. That push is all GitHub can
   do in the account.
2. A release is a tag `v*` on a commit of main. `infra/deploy.sh v1.0`, run
   with your own AWS credentials, checks the commit is on main and its image is
   in ECR, adds the release tag to the image, and deploys `app.yaml` with it.
3. `infra/deploy.sh <tag or commit>` with an older one is the rollback.

CloudFormation applies the template as `CfnExecutionRole`, which reaches only
the services the application stack is made of and cannot create IAM roles.

## One-time setup

```bash
aws cloudformation deploy --stack-name messenger-bootstrap \
  --template-file infra/bootstrap.yaml --capabilities CAPABILITY_IAM
```

Pass `--parameter-overrides CreateOidcProvider=false` if the account already
has the GitHub OIDC provider.

Then, by hand:

- **Atlas**: a cluster and a database user with `readWrite` on the `messenger`
  database and nothing more. Add the stack's `NatPublicIp` output to the IP
  access list: the tasks reach Atlas from that address only.
- **ACM**: a certificate for the domain, in the same region, validated by DNS.
- **SSM parameters** under `/messenger/`: `mongo-uri` (the `mongodb+srv://`
  address without the login), `mongo-username`, `mongo-password`,
  `redis-password`, `operator-name`, `operator-email`, `certificate-arn`.
  Passwords as `SecureString`. A task does not start while any of them is
  missing.
- **Repository variables** (Settings, Variables, Actions): `AWS_REGION` and
  `AWS_PUSH_ROLE_ARN` from the bootstrap outputs.

Use short-lived credentials with MFA on the machine that deploys, such as
`aws sso login`, not access keys kept in `~/.aws/credentials`: whoever can
deploy can read every message.

## Bringing it up and down

```bash
aws cloudformation deploy --stack-name messenger-network \
  --template-file infra/network.yaml
```

```bash
infra/deploy.sh v1.0
```

The first deploy creates `messenger-app`. Point the domain at the
`LoadBalancerDns` it prints with a CNAME.

To stop paying for it, delete `messenger-app` first, then `messenger-network`:
the application stack imports the network's outputs, so the order is enforced.
A first creation that failed leaves the stack in `ROLLBACK_COMPLETE`; delete it
before deploying again.
