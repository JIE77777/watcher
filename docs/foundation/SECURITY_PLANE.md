# Security Plane

> Status: foundation boundary
> Scope: Android System, service/relay edge posture, public deployment readiness

Security is not module business.

Watcher keeps security posture in a separate plane so modules cannot quietly own
transport, auth, public exposure, or secret handling.

## Owns

The security plane owns:

- owner auth posture
- device registration posture
- relay URL transport posture
- HTTPS / cleartext warning
- public host exposure warning
- allowed host / trusted proxy / HSTS checklist
- update-channel trust posture
- debug report redaction rules

## Does Not Own

The security plane does not own:

- module resources
- module operations
- agent session state
- worker runtime business details
- feed, dataset, conversation, artifact, or pending-input presentation

Those stay in module or runtime surfaces.

## Android Boundary

Android `System` may link to Security, but Security is a separate screen.

The Security screen may show:

- relay URL scheme and host class
- whether owner token is stored locally
- whether this device has a device token
- whether HTTPS is currently used
- whether a self-signed relay certificate fingerprint is trusted locally
- whether the host appears public
- next required edge-hardening steps

The Security screen must not show raw owner token or raw device token.

## Contract

`GET /api/v2/security/posture` exposes the minimal security posture for the
current service or relay. It is a small readiness signal, not a full audit
system.

## Current Deployment Stages

- `local_dev`: loopback or emulator HTTP is acceptable.
- `lan_private`: private LAN HTTP is acceptable for controlled testing only.
- `public_cleartext`: public host over HTTP is not acceptable for release.
- `public_https`: minimum public posture when combined with host allowlist,
  trusted proxy, rate limit, secure cookies, and HSTS.

## Token Model

Watcher keeps token handling simple for a personal deployment:

- `owner_token`: deployment owner secret. Android uses it for first device
  registration or recovery after clearing registration.
- `device_token`: per-device relay secret. Android uses it for normal module,
  shell, event, update, and push requests after registration.
- `service.owner_token`: relay-to-service secret. It stays inside server
  configuration and is not a mobile or user-facing token.

Module screens should not branch on token type. They call authenticated relay
APIs and let the security plane decide which credential is used.

## Built-In TLS

The relay may terminate HTTPS itself with `security.tls.enabled=true`. If no
certificate files are configured and `auto_self_signed=true`, relay generates a
self-signed certificate and stores its SHA-256 fingerprint.

Android can pin that fingerprint from Settings. This is intended for personal
deployments without a domain, including Tailscale or a public IP on a non-443
port. It is not a replacement for a managed edge when exposing a multi-user
service.

## Public Release Gate

Before public/open-source release guidance says a deployment is safe for public
internet exposure, this must be true:

- service and relay are behind a reverse proxy or equivalent edge
- public traffic enters through HTTPS
- direct service/relay ports are firewalled from the public internet
- `allowed_hosts` contains only expected hostnames
- `trusted_proxies` contains only owned proxy ranges
- `secure_cookies=true` for dashboard sessions
- HSTS is enabled after HTTPS is stable
- owner token and session secret are high entropy and never logged
