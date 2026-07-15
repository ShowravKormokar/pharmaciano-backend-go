# ADR 0003 — Password Hashing: Argon2id (not bcrypt)

- **Status:** Accepted
- **Date:** 2026-07-15
- **Deciders:** Backend Lead, Security
- **Related:** ADR-0008 (JWT rotation and reuse detection)

---

## Context

We need a password-hashing scheme for Pharmaciano ERP users. The system stores
identifiers for pharmacists, cashiers, accountants, managers and admins.
Password data will be at rest in Postgres, encrypted only at the disk layer
(TDE-equivalent) in production, so the hash must be robust to database
compromise.

Constraints:

- OWASP ASVS L2 baseline.
- ~500–5 000 users per deployment; login events are frequent but not
  extreme (peak ≈ 20 logins/second per API replica).
- Hardware target: 4 vCPU / 4 GB RAM per node.
- No hardware security module (HSM) available.

Candidates:

1. **bcrypt** — mature, widely deployed, work-factor tunable.
2. **scrypt** — memory-hard, older.
3. **Argon2id** — memory-hard and side-channel-resistant, winner of the 2015
   PHC competition, the current OWASP recommendation.
4. **PBKDF2** — FIPS-compliant, but not memory-hard; obsolete for new work.

## Decision

We use **Argon2id** via `golang.org/x/crypto/argon2` with the following
parameters as the **initial baseline**:

| Parameter | Value | Meaning |
|---|---|---|
| `memory` | **64 MiB** (`64 * 1024 KiB`) | RAM per hash |
| `time` | **3** | iterations |
| `parallelism` | **2** | lanes |
| `salt length` | **16 bytes** | random per user |
| `key length` | **32 bytes** | derived key size |
| `pepper` | server-side, from env, applied via HMAC-SHA-256 before hashing | defence against DB-only compromise |

Encoded storage format:  
```
argon2idv=19m=65536,t=3,p=2m=65536,t=3,p=2
m=65536,t=3,p=2<b64salt>$<b64hash>
```

This is the standard PHC format so we can migrate params or algorithms later
without breaking existing hashes.

## Rationale

- **Memory-hardness** defeats commodity GPU/ASIC cracking rigs that trivially
  break high-work-factor bcrypt hashes.
- **Argon2id** blends Argon2i (side-channel resistant) and Argon2d (data-
  dependent, GPU-hard). It is the recommended variant by the PHC and OWASP.
- Parameters are budgeted to give **≈ 150–250 ms per hash on the 4 vCPU
  target**, which is at the OWASP-recommended threshold for a login flow.

Bcrypt was rejected because:

- Its 72-byte password truncation is a footgun.
- It is not memory-hard, so GPU attacks scale linearly.
- The `bcrypt` work factor cap of 31 offers less future-proofing than
  Argon2id's independent m/t/p tuning.

## Consequences

### Positive

- Modern, well-reviewed algorithm with defence-in-depth (memory + iterations
  + parallelism).
- Independent tuning of memory, time and parallelism as hardware evolves.
- Encoded format makes parameter migration trivial.

### Negative

- ~200 ms per hash means login and password-change endpoints have a real CPU
  cost. Mitigations:
  - Rate limit per-email (5 / 15 min) and per-IP (20 / 15 min).
  - Argon2 runs off the request goroutine — accepted as the login cost.
  - Add API instance capacity if login throughput ever exceeds the budget.
- Argon2 memory (~64 MiB × concurrency) must be budgeted. At `p=2` and 50
  concurrent logins the peak is ~3.2 GiB, which fits our node budget with
  headroom.

## Migration Path

1. New users: Argon2id from creation.
2. Existing users (if we ever import legacy bcrypt hashes): store the algorithm
   marker in the encoded string, then on successful login **re-hash with
   Argon2id transparently**. No forced password reset.

## Guardrails

- Passwords are **never** logged, even at TRACE. The Zap logger has a
  redact list that includes `password`, `password_hash`, and `token`.
- The pepper is loaded from `FIELD_ENCRYPTION_KEY`-style env var and never
  written to disk.
- Parameters live in `config/config.yaml` under `password.argon2.*` so we can
  raise them without a code change.
- Weekly cronjob measures the median hash time; if it drops below 100 ms we
  raise the memory factor.

## References

- OWASP ASVS v4, section V2.4 (Credential Storage).
- OWASP Password Storage Cheat Sheet (2024 edition).
- Password Hashing Competition, https://password-hashing.net/.
- Argon2 RFC 9106.