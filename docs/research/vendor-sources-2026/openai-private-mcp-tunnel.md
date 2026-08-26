# OpenAI: Private MCP Servers Without Public Exposure

> **Source**: [OpenAI Developers](https://developers.openai.com/blog/connect-private-mcp-servers-to-openai-products/)<br>
> **Published**: 2026-06-26<br>
> **Evidence status**: `verified` official vendor engineering publication; project implications are `draft`

## Relevant Content

OpenAI describes the problem of connecting hosted products to MCP servers inside enterprise networks, service meshes, laptops, and other environments that reject inbound public traffic. Its Secure MCP Tunnel keeps the server behind customer controls while preserving the standard MCP request, response, streaming, notification, and authentication model.

The article emphasizes:

- outbound-only connectivity from the customer environment;
- explicit destinations and narrow connectivity rather than general network peering;
- customer-inspectable software inside the customer boundary;
- organization, workspace, and tunnel identity on the hosted side;
- enterprise OAuth, private certificate authorities, proxies, and mTLS;
- the operational and procurement consequences of adding a connectivity vendor.

## What This Supports

The article supports treating network, identity, trust, procurement, and operating boundaries as first-class integration concerns rather than erasing them behind a tool call.

## What This Does Not Prove

- It addresses hosted-product-to-tool connectivity through MCP, not Agent-to-Agent federation.
- OpenAI does not endorse A2A in this source.

## Project Implications

- Model customer-controlled outbound connectivity as a possible deployment profile.
- Keep protocol identity separate from network reachability and tenant authorization.
- Avoid turning a narrow Agent connection into broad network access.
