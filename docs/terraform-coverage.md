# Terraform provider coverage

How the `anypoint` library compares with
[terraform-provider-anypoint](https://github.com/mulesoft/terraform-provider-anypoint) at
`9f3ffd8` (2026-11-03): resource and data source names are in `spec/terraform-provider-anypoint.json`.

Method: the 77 distinct endpoints the provider's `internal/client` calls were traced to the resources and
data sources that use them and matched against the library's paths; every mismatch was checked by hand.
This compares endpoints, not request fields: a covered resource can still differ in the fields modelled.

The provider has 101 resources and 120 data sources. 72 of each are typed wrappers, one per policy type
(`anypoint_api_policy_<type>`), over the one API Manager policy endpoint, which the library covers
generically (`ApplyAPIPolicy` takes the configuration as JSON). The remaining 29 resources and 48 data
sources:

| | resources | data sources |
|---|---|---|
| covered | 26 | 41 |
| partial | 3 | 6 |
| missing | 0 | 1 |

## Covered

Organizations, environments, teams (members, roles), role groups (roles, users), users, available roles and
scopes, connected apps and their scopes, Exchange assets (pages, tags, custom fields, external instances,
search), API instances, SLA tiers, policies, upstreams, managed and self-managed (Flex) gateways, secret
groups with their keystores, truststores, certificates, certificate pinsets, shared secrets and TLS contexts,
and CloudHub 2.0 private spaces: the space, its network, firewall rules and ingress (advanced) settings,
environment associations, upgrades, transit gateways, VPN connections and TLS contexts (`anypoint/privatespaces.go`,
`anypoint cloudhub private-space ...`).

Where the provider found the private-space API to behave differently from its description, the library
does what it does: transit gateways are renamed through the organization-scoped PATCH, routes are set by
patching the attachment with its name, and deleting one also de-registers it organization-wide (detaching
alone leaves the AWS resource share bound); creating a transit gateway or TLS context returns no body, so the
new item is found by name; an empty upgrade status means none is scheduled.

The self-managed gateway resource uses `/standalone/api/v1` (its registration-facade base is declared but
unused), as the library does.

## Partial

| provider | what is missing |
|---|---|
| `anypoint_agent_instance`, `anypoint_mcp_server`, `anypoint_mcp_bridge` (resources and data sources) | typed agent/MCP payloads; the endpoints (API Manager instances, Exchange assets) are reachable through the generic functions. Planned for a later release. |

## Missing

| provider | API |
|---|---|
| `anypoint_mcp_tools` | none: the provider parses an Exchange asset's OpenAPI or RAML spec into MCP tools. Planned with MCP. |

anypoint-cli-v4 has no private-space commands; the `cloudhub private-space` commands follow the provider's resources.
