# DNS-AID records for syntheticbrew.ai

DNS for AI Discovery (DNS-AID) per [draft-mozleywilliams-dnsop-dnsaid](https://datatracker.ietf.org/doc/draft-mozleywilliams-dnsop-dnsaid/) and [RFC 9460](https://www.rfc-editor.org/rfc/rfc9460) (SVCB records). These records live in Cloudflare DNS, not in this repo — apply them by hand in the dashboard.

We advertise only what actually exists: the public MCP endpoint (`mcp.syntheticbrew.ai`) and the HTTPS discovery surface at the apex (`/.well-known/api-catalog`, `/.well-known/agent-skills/index.json`, `/.well-known/mcp/server-card.json`, `/llms.txt`, `/auth.md`). There is **no A2A endpoint**, so no `_a2a._agents` record is published.

## Records to add (Cloudflare dashboard → syntheticbrew.ai → DNS → Add record, type SVCB)

| Name | Type | TTL | Priority | Target | Value |
|---|---|---|---|---|---|
| `_mcp._agents` | SVCB | 3600 | 1 | `mcp.syntheticbrew.ai.` | `alpn="mcp" port="443" mandatory="alpn,port"` |
| `_index._agents` | SVCB | 3600 | 1 | `syntheticbrew.ai.` | `alpn="h2,http/1.1" port="443"` |

Zone-file form:

```
_mcp._agents.syntheticbrew.ai.   3600 IN SVCB 1 mcp.syntheticbrew.ai. alpn="mcp" port="443" mandatory=alpn,port
_index._agents.syntheticbrew.ai. 3600 IN SVCB 1 syntheticbrew.ai. alpn="h2,http/1.1" port="443"
```

> The draft's alpn labels are pre-standard (the scanner's own example uses `alpn="a2a"` for an A2A service; `mcp` is the analogous label for MCP). If a future scan rejects `alpn="mcp"`, re-check the current draft revision before changing anything.

## DNSSEC

The DNS-AID check requires the zone to be signed so validating resolvers return authenticated data.

1. Cloudflare dashboard → syntheticbrew.ai → DNS → Settings → **Enable DNSSEC**.
2. Copy the DS record Cloudflare shows (key tag, algorithm 13, digest type 2, digest).
3. Add that DS record at the domain registrar (where syntheticbrew.ai is registered).
4. Wait until Cloudflare shows DNSSEC status **Success** (can take up to 24 h for registrar/registry propagation).

## Verify

```bash
dig _mcp._agents.syntheticbrew.ai SVCB +short
dig _index._agents.syntheticbrew.ai SVCB +short
dig +dnssec syntheticbrew.ai SOA          # expect the AD flag from a validating resolver
delv syntheticbrew.ai                     # expect "fully validated"
```

Then re-run the scanner (see `agent-readiness.md`) and check `checks.discoverability.dnsAid.status`.
