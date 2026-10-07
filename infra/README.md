# Deployment to AWS

Three CloudFormation stacks, from the longest-lived to the shortest:

| Stack | Template | Holds | Lifetime |
|---|---|---|---|
| `messenger-bootstrap` | `bootstrap.yaml` | ECR repository, NAT address, roles for GitHub Actions | kept |
| `messenger-network` | `network.yaml` | VPC, subnets, NAT gateway | for a demo |
| `messenger-app` | `app.yaml` | ECS cluster, Redis, application nodes, ALB | for a demo, updated by every deploy |

MongoDB is an Atlas cluster outside AWS.

## How a version ships

1. A commit lands on main. CI runs the checks, then `publish` builds the arm64
   image and pushes it to ECR as `sha-<commit>`.
2. A tag `v*` on a commit of main runs `deploy.yml`: it adds the tag to that
   image and deploys `app.yaml` with `ImageTag=sha-<commit>`.
3. Run `deploy.yml` by hand with a tag or a commit to roll back, or to deploy
   after the stacks were brought back up.

## One-time setup

```bash
aws cloudformation deploy --stack-name messenger-bootstrap \
  --template-file infra/bootstrap.yaml --capabilities CAPABILITY_IAM
```

Pass `--parameter-overrides CreateOidcProvider=false` if the account already
has the GitHub OIDC provider.

Then, by hand:

- **Atlas**: a cluster and a database user. Add the stack's `NatPublicIp`
  output to the IP access list: the tasks reach Atlas from that address only.
- **ACM**: a certificate for the domain, in the same region, validated by DNS.
- **SSM parameters** under `/messenger/`: `mongo-uri` (the `mongodb+srv://`
  address without the login), `mongo-username`, `mongo-password`,
  `redis-password`, `operator-name`, `operator-email`. Passwords as
  `SecureString`. A task does not start while any of them is missing.
- **Repository variables** (Settings, Variables, Actions): `AWS_REGION`,
  `AWS_PUSH_ROLE_ARN`, `AWS_DEPLOY_ROLE_ARN`, `AWS_CFN_ROLE_ARN` from the
  bootstrap outputs, and `CERTIFICATE_ARN`.

## Bringing it up and down

```bash
aws cloudformation deploy --stack-name messenger-network \
  --template-file infra/network.yaml
```

Then run the Deploy workflow by hand with the release to show: it creates
`messenger-app` if it is not there. Point the domain at the `LoadBalancerDns`
output with a CNAME.

To stop paying for it, delete `messenger-app` first, then `messenger-network`:
the application stack imports the network's outputs, so the order is enforced.
A first creation that failed leaves the stack in `ROLLBACK_COMPLETE`; delete it
before deploying again.
