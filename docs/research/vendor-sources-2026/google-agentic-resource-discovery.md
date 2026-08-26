# Google: Agentic Resource Discovery

> **Source**: [Google Developers Blog](https://developers.googleblog.com/announcing-the-agentic-resource-discovery-specification/)<br>
> **Published**: 2026-06-17<br>
> **Evidence status**: `verified` official vendor publication; specification maturity and interoperability remain `to-verify`

## Relevant Content

Google states that Agent capabilities increasingly span teams, organizations, and platforms, while existing custom registries remain fragmented. The proposed Agentic Resource Discovery (ARD) specification aims to publish, discover, and verify capabilities independently of their framework, protocol, or provider.

The described model uses:

- organization-hosted catalogs under the organization's own domain;
- domain ownership as part of identity and trust establishment;
- registries that index catalogs rather than take ownership of the underlying resources;
- federated registry discovery;
- verifiable metadata followed by a direct connection through the resource's native protocol.

## What This Supports

The article directly supports cross-organization discovery and a separation between discovery metadata, trust establishment, and the native invocation protocol.

## What This Does Not Prove

- ARD maturity, adoption, security properties, and compatibility with current A2A Agent Cards have not been validated here.
- It does not prove that the Hub should implement its own registry.

## Project Implications

- Add ARD to the registry and discovery comparison before choosing a canonical Hub registry model.
- Prefer catalog ownership by the publishing organization and index ownership by registries.
- Investigate how ARD resources map to Agent Cards, protocol versions, health, revocation, and tenant policy.
