# anypoint-go-client

A Go client library and a compact, single-binary CLI for the MuleSoft Anypoint Platform.
The library uses only the Go standard library. The CLI needs no Node.js, npm or plugins; for governance
it links the (Go) AMF custom validator.

The auth flows and org resolution follow the official
[terraform-provider-anypoint](https://github.com/mulesoft/terraform-provider-anypoint) client.

## Install

Download the archive for your platform from the
[releases page](https://github.com/muratcanozdemir/anypoint-go-client/releases):
Linux, macOS and Windows, each for amd64 and arm64.
Check the archive against `SHA256SUMS`, and optionally its build provenance:

```sh
sha256sum --ignore-missing -c SHA256SUMS
gh attestation verify anypoint_*_linux_amd64.tar.gz --repo muratcanozdemir/anypoint-go-client
```

Or build from source:

```sh
go install github.com/muratcanozdemir/anypoint-go-client/cmd/anypoint@latest
```

## Library

```sh
go get github.com/muratcanozdemir/anypoint-go-client/anypoint
```

```go
c := anypoint.NewClient("eu", // us, eu, gov, ca, jp, or a hostname
	anypoint.WithClientCredentials(os.Getenv("ANYPOINT_CLIENT_ID"), os.Getenv("ANYPOINT_CLIENT_SECRET")),
	anypoint.WithEnv("Sandbox"))

envs, err := c.Envs(ctx)

// Any endpoint: {org} and {env} expand to the resolved IDs.
path, err := c.Expand(ctx, "/amc/application-manager/api/v2/organizations/{org}/environments/{env}/deployments")
var deployments map[string]any
err = c.JSON(ctx, http.MethodGet, path, nil, &deployments)
```

Non-2xx responses come back as `*anypoint.HTTPError`; test for a status with `anypoint.IsStatus(err, 404)`.

## Authenticate

```sh
# Connected app ("acts on its own behalf"): client_credentials
anypoint login --client-id ID --client-secret SECRET --env Sandbox

# Connected app acting on behalf of a user (password grant, falls back to /accounts/login)
anypoint login --client-id ID --client-secret SECRET --username me --password '…'

# Plain username/password
anypoint login --username me --password '…'
```

`login` saves the values you pass into the current profile in `~/.config/anypoint/config.json` (mode 0600).
Tokens are cached in `~/.cache/anypoint/`, refreshed when they expire, and refreshed again on a 401.
In CI, skip the config file and use the same variables as anypoint-cli-v4:
`ANYPOINT_CLIENT_ID`, `ANYPOINT_CLIENT_SECRET`, `ANYPOINT_USERNAME`, `ANYPOINT_PASSWORD`,
`ANYPOINT_HOST` (`us`, `eu`, `gov`, `ca`, `jp` or a hostname), `ANYPOINT_ORG`, `ANYPOINT_ENV`, `ANYPOINT_BEARER`.

The settings are applied in this order, and each one overrides the ones after it: flags, then environment variables, then the profile. `--org` and `--env` accept a name or an ID.

## Commands

The command tree mirrors anypoint-cli-v4 and its plugins; write commands either way:
`anypoint account:environment:list` or `anypoint account environment list`.
[docs/coverage.md](docs/coverage.md) lists every v4 command and its status here.

```
anypoint login | logout | whoami | version
anypoint config list|get|set|unset|use          # profiles: --profile NAME
anypoint account ...        # business groups, environments, the current user
anypoint exchange ...       # assets, portals, pages, resources
anypoint secrets-mgr ...    # secret groups, shared secrets, keystores, truststores, TLS contexts
anypoint api-mgr ...        # API instances, policies, tiers, contracts, alerts, upstreams
anypoint runtime-mgr ...    # CloudHub 1.0/2.0, Runtime Fabric and hybrid apps, servers, alerts, managed Flex Gateways
anypoint cloudhub ...       # CloudHub 1.0 VPCs and load balancers, 2.0 private spaces, regions, runtimes
anypoint designcenter ...   # API Designer projects
anypoint datagraph ...      # DataGraph load balancer setting
anypoint governance ...     # profiles, API and ruleset validation, ruleset authoring
anypoint agent-network ...  # setup:gateways
anypoint dx ...             # Mule runtimes and JDKs for local development
anypoint api PATH           # any endpoint, {org}/{env} expanded
```

Every command accepts `-o json` for machine output, which you can pipe to `jq`; it prints the API's
own JSON. Add `--verbose` to log the HTTP requests. `anypoint COMMAND --help` lists a command's flags.

`dx:mule:project:create` and `dx:mule:describe-connector` run MuleSoft's mule-dx jars with Java 17+
(`JAVA_HOME` or `java` on PATH). The jars ship only inside MuleSoft's npm plugin, so `anypoint` fetches that
package on first use, pinned to a version and its registry sha512 (a tarball that doesn't match is refused),
and unpacks it into `~/.cache/anypoint/tooling` (`ANYPOINT_TOOLING_DIR`; `ANYPOINT_NPM_REGISTRY` for a
mirror). Node.js is never needed.

The governance commands that validate APIs and rulesets (`api:validate`, `api:inspect`, `ruleset:validate`,
`ruleset:info`, `ruleset:init`, `ruleset:clone`, `document`) parse them with AMF on the JVM, also Java 17+.
AMF's classpath is pinned jar by jar (`cmd/anypoint/tooling/amf.lock`, sha256 each) and fetched from Maven
Central and MuleSoft's repository on first use (`ANYPOINT_MAVEN_MIRROR` for a repository manager); rulesets
are applied by the AMF custom validator, which is Go and built into `anypoint`. The ruleset-authoring aids
(`ruleset:classes`, `properties`, `validate-authoring`, `completions`, ...) read the model metadata in the
governance plugin's npm package, fetched and pinned like the dx-mule jars. Exchange dependencies and remote
rulesets are cached in `~/.anypoint_deps`, as anypoint-cli-v4 does. Where v4's output is not valid (`ruleset:init`
writes a `version` the dialect rejects, `ruleset:clone --remove` keeps the removed rule's validation), `anypoint`
writes what v4 meant to.

The agent-network project commands are listed but not supported yet (`docs/coverage.md` says why); use
anypoint-cli-v4 for them.

### Examples

```sh
# Deploy a Mule app from Exchange to CloudHub 2.0, then follow its logs
anypoint runtime-mgr:application:deploy hello-api <target-id> 4.9.0:2e-java17 hello-api \
  --env Sandbox --replicas 2 --property http.port:8081 --secureProperty db.password:s3cret
anypoint runtime-mgr:application:logs <app-id> <spec-id>

# CloudHub 1.0
anypoint runtime-mgr:cloudhub-application:deploy hello-api target/hello-api.jar --runtime 4.6.0 --workers 2
anypoint cloudhub:vpc:firewall-rules:add main 10.0.0.0/8 tcp 8081 8082
anypoint cloudhub private-space tls-context create prod-space api --certificate api.crt --key api.key

# Manage an API with a policy
anypoint api-mgr:api:manage hello-api 1.0.0 --isFlex
anypoint api-mgr:policy:apply <api-id> rate-limiting --config '{"rateLimits":[{"maximumRequests":10,"timePeriodInMilliseconds":1000}]}'

anypoint exchange:asset:list salesforce --limit 20
anypoint api /cloudhub/api/v2/applications -H 'X-ANYPNT-ENV-ID: {env}'
```

## Layout

| path | contents |
|---|---|
| `anypoint/` | the library: HTTP client, auth and token cache, 401 retry, org and env resolution, region hosts, and typed calls per product area |
| `cmd/anypoint/main.go`, `cli.go` | the command tree and flag parsing (flags may come after positional arguments) |
| `cmd/anypoint/config.go` | CLI profiles and the environment variable overlay |
| `cmd/anypoint/output.go` | tables and JSON output |
| `cmd/anypoint/<area>.go` | one file per v4 plugin area, with its tests beside it |
| `internal/` | JSON path helpers, atomic file writes, a small YAML reader for policy definitions |

The bearer token is only sent to the configured Anypoint host. Pre-signed download links, such as Exchange file links on S3, are fetched without it.

## Development

```sh
make test       # unit tests with the race detector
make e2e        # the CLI against a real Mule CE runtime: MULE_HOME=/path/to/mule-standalone-4.x make e2e
make lint       # golangci-lint, pinned; fails on code `go fix` would change
make fix        # apply go fix
make build      # the CLI for this machine, into dist/
make snapshot   # every release platform, packaged by GoReleaser into dist/
```

The end-to-end test (`e2e/`) stands in for the control plane and the Runtime Manager agent: it serves the
hybrid API the CLI calls and applies each request to a real Mule CE runtime, by dropping archives into `apps/`
and removing anchor files, so deploy, redeploy, stop, start, restart, copy and delete are checked against what
the runtime actually deploys and runs. The other products have no local runtime to test against; their commands
are covered by unit tests against a recording fake of the API.

## Releases

Commits on `main` follow [Conventional Commits](https://www.conventionalcommits.org/).
[release-please](.github/workflows/release-please.yml) keeps a release pull request open that bumps
the version and writes `CHANGELOG.md`; merging it tags `vX.Y.Z` and runs
[the release workflow](.github/workflows/release.yml). That workflow tests and scans the tagged commit
(`govulncheck`), then [GoReleaser](.goreleaser.yaml) builds the CLI for linux, darwin and windows on amd64
and arm64 into reproducible archives, with a CycloneDX SBOM per binary and `SHA256SUMS`. Each archive
gets SLSA provenance and SBOM attestations, which are verified before anything is attached to the GitHub release.
Pushing a `v*` tag by hand runs the same workflow; a tag with a suffix such as `-rc.1` is published as a pre-release.
