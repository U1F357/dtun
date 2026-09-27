# Phase 0 — 2026-09-27

- Beijing system Go: 1.22.9; project toolchain: official Go 1.27.1 (download verified against go.dev SHA256). Hong Kong has no Go installed; deploy a static Linux/amd64 binary.
- Latest stable Pion module verified via proxy.golang.org: **github.com/pion/dtls/v3 v3.1.10**, tagged 2026-09-26, commit 2954d47efeeb539d0ac264572ffd96bbdf01bb93. Requires Go >=1.24.
- Stable v3 implements DTLS 1.2. Main advertises DTLS 1.3 but remains frozen for tagging and accepts breaking changes. Choose stable v3, not main.
- Use ECDSA P-256 self-signed certificates, mutual SHA256 SPKI pinning, RequireAnyClientCert + mandatory VerifyPeerCertificate, and TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256. Certificate validity is checked too. No PSK or traffic-secret logging.
- Config API remains available but deprecated in favor of functional options. Keep dependency usage inside transport package for future migration.
- Source verification: Config.MTU documents handshake fragmentation; Conn.Write creates a single application record and compactRawPackets returns a single record unmodified. Thus MTU does NOT cap application writes. Fragment application packets into <=1100 bytes plus 16-byte header; wrap actual UDP WriteTo with a strict 1200-byte limit. Set handshake MTU=1100 for headroom.
- ReplayProtectionWindow defaults to 64; explicitly use 1024 for reordering. DTLS handles record replay and nonce/key management. Application packet IDs are independent.
- HelloVerify cookie remains enabled. A fixed allowed peer IP and a single session bound server state; failed handshakes have a 10-second deadline. This is not a general Internet-facing multi-peer server.
- DTLS 1.2 does not offer the requested DTLS 1.3 key-update mechanism. Re-establish sessions after one hour; no custom rekey. Expect a brief interruption.
- Known-issue limitation: GitHub issues API returned HTTP 403; no claim of exhaustive 1.3 issue review. Main's own tagging warning is enough to exclude it from this MVP.
- User explicitly excludes HY2, so no HY2 implementation/comparison in this iteration. IPv6 Internet exit is not claimed.

Sources: https://github.com/pion/dtls ; https://proxy.golang.org/github.com/pion/dtls/v3/@latest ; https://github.com/pion/dtls/blob/v3.1.10/config.go ; https://github.com/pion/dtls/blob/v3.1.10/conn.go
