# Terraform provider coverage

How the `anypoint` library compares with
[terraform-provider-anypoint](https://github.com/mulesoft/terraform-provider-anypoint) at
`9f3ffd8` (2026-11-03): resource and data source names are in `spec/terraform-provider-anypoint.json`.

Method: the 77 distinct endpoints the provider's `internal/client` calls were traced to the resources and
data sources that use them and matched against the library's paths; every mismatch was checked by hand.
This compares endpoints, not request fields: a covered resource can still differ in the fields modelled.

The provider has 101 resources and 120 data sources. 72 of each are typed wrappers, one per policy type
(`anypoint_api_policy_<type>`), over the one API Manager policy endpoint, which the library covers
generically (`ApplyPolicy` takes the configuration as JSON). The remaining 29 resources and 48 data
sources:

| | resources | data sources |
|---|---|---|
| covered | 18 | 32 |
| partial | 5 | 7 |
| missing | 6 | 9 |

## Covered

Organizations, environments, teams (members, roles), role groups (roles, users), users, available roles and
scopes, connected apps and their scopes, Exchange assets (pages, tags, custom fields, external instances,
search), API instances, SLA tiers, policies, upstreams, managed and self-managed (Flex) gateways, secret
groups with their keystores, truststores, certificates, certificate pinsets and shared secrets, and Secrets
Manager TLS contexts.

The self-managed gateway resource uses `/standalone/api/v1` (its registration-facade base is declared but
unused), as the library does.

## Partial

| provider | what is missing |
|---|---|
| `anypoint_agent_instance`, `anypoint_mcp_server`, `anypoint_mcp_bridge` (resources and data sources) | typed agent/MCP payloads; the endpoints (API Manager instances, Exchange assets) are reachable through the generic functions. Planned for a later release. |
| `anypoint_tls_context`, `anypoint_secret_group_tls_context(s)` | TLS contexts of a CloudHub 2 private space (`/runtimefabric/.../privatespaces/{id}/tlsContexts`) |

## Missing

| provider | API |
|---|---|
| `anypoint_private_space_config`, `anypoint_private_spaces` | `/runtimefabric/api/organizations/{org}/privatespaces[/{id}]` (also firewall rules and private network settings, which the provider sets through the private space) |
| `anypoint_privatespace_advanced_config` | same, advanced settings |
| `anypoint_private_space_association(s)` | `.../privatespaces/{id}/associations` |
| `anypoint_private_space_upgrade` | `.../privatespaces/{id}/upgrade`, `.../upgradestatus` |
| `anypoint_transit_gateway_connection(s)` | `.../privatespaces/{id}/transitgateways`, `.../transitgateways/{id}` |
| `anypoint_vpn_connection` | `.../privatespaces/{id}/connections[/{id}/vpns/{id}]` |
| `anypoint_mcp_tools` | none: the provider parses an Exchange asset's OpenAPI or RAML spec into MCP tools. Planned with MCP. |

anypoint-cli-v4 has no private-space commands, which is why these were not covered by the CLI work.
