# SETTINGS_CONTRACT.md

**Project:** Pharmaciano ERP --- Go Backend\
**Module:** `settings`\
**Status:** Phase 1 implementation in code; PostgreSQL migration/integration
validation pending\
**API Version:** `/api/v1`\
**Architecture:** Modular Monolith · Three-Tier · Contract-First OpenAPI
3.1\
**Primary Storage:** PostgreSQL\
**Optional Infrastructure:** Existing audit infrastructure; Redis/outbox
only if already available and justified

------------------------------------------------------------------------

## 1. Purpose

The Settings module manages approved application and business
configuration. It is not an unrestricted key-value store.

The initial implementation must provide:

-   A controlled registry of permitted setting keys and value types.
-   System-wide defaults/overrides and organization-specific overrides.
-   Server-side validation of values and ownership.
-   Deterministic resolution of effective settings.
-   Optimistic concurrency for updates.
-   Atomic bulk updates.
-   Safe handling of sensitive configuration.
-   Configuration revision history integrated with the existing audit
    architecture.
-   Compatibility with the existing authentication, tenant, RBAC,
    rate-limit, error, response, database, and OpenAPI infrastructure.

The module must not become the owner of configuration or business data
that belongs to MasterData, Medicine, Inventory, Ledger, Purchase, Sale,
or other domain modules.

The Phase 1 model, DTO, migration, repository, service, handlers, routes,
seed integration, and OpenAPI paths are implemented. The migration has
not yet been exercised against a PostgreSQL instance in this workspace,
so deployment readiness still requires database integration testing.
Existing legacy seed keys are retained as deprecated definitions until
their owning modules are verified to consume database-backed values;
this API must not imply that changing a stored value changes runtime
behavior when no such consumer exists.

## 2. Scope and delivery phases

To avoid overengineering, the implementation is divided into explicit
phases.

### Phase 1 --- Initial implementation

Implement only what is needed for the four approved settings endpoints:

1.  Controlled setting definitions.
2.  System-level setting values.
3.  Organization-level setting overrides.
4.  Effective-value resolution between system and organization scope.
5.  Typed validation.
6.  Versioned updates and stale-version conflict handling.
7.  Atomic bulk updates.
8.  Revision records.
9.  Sensitive-value redaction and secret-reference-only rules.
10. Existing middleware, RBAC, audit, error, and OpenAPI integration.

### Deferred --- Do not implement in Phase 1 without a separately approved requirement

-   Branch-level setting overrides.
-   User preference storage and endpoints.
-   Feature-flag definitions, values, and endpoints.
-   Scheduled configuration activation.
-   Redis caching.
-   Transactional outbox events.
-   A new scheduler or background worker.
-   Generic scope-ID persistence abstractions.

These may be added in later phases when the API contract and a real use
case require them. Do not create empty files, tables, models, or routes
merely to anticipate future features.

## 3. Public API contract

The four Phase 1 endpoint paths are registered in the router and
documented in `docs/api/API.md` and the split OpenAPI contract. The model
does **not** require additional endpoints.

  ----------------------------------------------------------------------------------------
  Method and path                Permission          Rate policy         Purpose
  ------------------------------ ------------------- ------------------- -----------------
  `GET /api/v1/settings`         `settings:view`     `rate_auth_read`    List settings
                                                                         available to the
                                                                         caller; sensitive
                                                                         metadata/value
                                                                         disclosure is
                                                                         restricted

  `GET /api/v1/settings/{key}`   `settings:view`     `rate_auth_read`    Resolve one
                                                                         setting for the
                                                                         caller's
                                                                         authorized scope

  `PUT /api/v1/settings/{key}`   `settings:update`   `rate_auth_write`   Create or update
                                                                         an allowed system
                                                                         or organization
                                                                         value

  `POST /api/v1/settings/bulk`   `settings:update`   `rate_auth_write`   Atomically
                                                                         create/update a
                                                                         bounded list of
                                                                         settings  
  ----------------------------------------------------------------------------------------

### 3.1 Scope selection

The settings value scope is `system` or `organization`. For writes, the
request must explicitly select one of those scopes; for reads, the query
scope may be `effective` (the default), `system`, or `organization`.
This selector never carries an organization, branch, or user identity.
The organization identity is always taken from the verified
authentication/tenant context.

-   A platform-authorized `SUPER_ADMIN` may perform system-level
    operations where explicitly permitted.
-   Organization-scoped operations use the organization established by
    existing tenant middleware and verified authorization context.
-   A tenant caller must never access or mutate another organization's
    settings.
-   Requests must not be allowed to bypass tenant context by sending an
    arbitrary `organization_id`; request DTOs must not contain one.
-   System-scope reads and writes, and sensitive-value disclosure, require
    the project's privileged platform authorization policy. Enforce that
    policy through the existing authorization layer, not ad hoc role
    checks in handlers.
-   Branch and user scopes are deferred and must not be simulated
    through an arbitrary `scope_id`.

### 3.2 Sensitive reads

`include_sensitive=true` is retained as the documented query option and
requires the approved privileged policy, expected to be `SUPER_ADMIN`.

This parameter never authorizes returning actual secrets. Secret
material must remain in the established secret-management system.
Settings may store only an approved secret reference where required.
Without this parameter, sensitive values are redacted in every response;
revision history always omits/redacts them. Sensitive status is
definition-owned, not caller-controlled.

### 3.3 Bulk request

The bulk endpoint accepts an object with one `scope` and a bounded
`items` array. Each entry contains only client-writable fields, such as:

-   `key`
-   `value`
-   required `expected_version` for optimistic concurrency
-   `reason` when required for a sensitive/high-risk change

Clients cannot submit or overwrite server-owned metadata such as actor
IDs, timestamps, revision numbers, or ownership identifiers.

The JSON shape and maximum number of entries are defined in the DTOs and
OpenAPI schema. Duplicate keys in one request are rejected.

The Phase 1 DTO shape is `{"scope":"organization","items":[...]}`;
`scope` is `system` or `organization`, `items` contains 1–100 entries,
and each entry has a key, JSON value, required `expected_version`, and
optional reason. A single update has the same fields except `items`.
The four endpoint paths remain unchanged.

## 4. Phase 1 domain model

Use a deliberately small model set.

### 4.1 `SettingDefinition`

An authoritative registry entry maintained by reviewed code/seed
changes, not created arbitrarily by public clients.

Required fields:

-   `key`: stable unique identifier.
-   `value_type`: `string`, `integer`, `boolean`, `float`, or `json`.
-   `schema_version`: positive version for the setting's schema.
-   `default_value`: optional validated JSON value.
-   `allowed_scopes`: Phase 1 permits `system` and/or `organization`.
-   `description`: optional administrative documentation.
-   `is_sensitive`: disclosure classification controlled by the
    definition.
-   `secret_reference_only`: true when only an approved secret reference
    may be stored.
-   `validator_key`: optional key for an application-registered
    validator.
-   `status`: active or deprecated.

Definitions are server-controlled. Unknown keys and
unsupported/deprecated mutations must be rejected according to the
lifecycle policy.

### 4.2 `SystemSetting`

Persists an explicit system-level override.

Required logical fields:

-   Base fields from the existing `BaseModel`.
-   `key`
-   `value`
-   `version`
-   `updated_by`

The key must reference a valid definition that permits system scope.
There must be at most one system value per key.

If no explicit value exists, the resolver may use a validated definition
default only where the definition's policy permits it.

### 4.3 `OrganizationSetting`

Persists an organization-specific override.

Required logical fields:

-   Base fields from the existing `BaseModel`.
-   `organization_id`
-   `key`
-   `value`
-   `version`
-   `updated_by`

The key must reference a valid definition that permits organization
scope. There must be at most one value for each `(organization_id, key)`
pair.

Organization ownership must be validated by existing tenant context and
database constraints where appropriate.

### 4.4 `SettingRevision`

Stores append-only configuration history for Phase 1 mutations.

Required logical fields:

-   Setting key.
-   Scope (`system` or `organization`).
-   Organization identity for organization scope; absent for system
    scope.
-   Revision/version number.
-   Definition schema version used to validate the value.
-   Actor ID.
-   Change reason where required.
-   Change timestamp.
-   Correlation/request ID when available.
-   Previous/new values only when safe to retain.
-   Redaction marker when values are omitted or redacted.

Revision rows are inserted in the same transaction as the setting
change. Ordinary application operations must not edit or delete existing
revision records. Sensitive values and actual secrets must never be
written to revision history.

## 5. Deferred models

Do not include these in the Phase 1 `model.go` unless the contract is
separately amended:

-   `BranchSetting`
-   `UserPreference`
-   `FeatureDefinition`
-   `FeatureFlag`
-   Generic scope structures designed to cover every future scope

When branch settings, user preferences, or feature flags are approved,
update the model, migration plan, DTOs, OpenAPI, permissions, tests, and
contract together.

Feature flags are a distinct capability. They control feature
availability, never authorization. They are not required by the four
current settings endpoints and should not be implemented solely because
an earlier draft included a `FeatureFlag` struct.

## 6. Value representation and validation

The initial supported value types are:

-   `string`
-   `integer`
-   `boolean`
-   `float`
-   `json`

Use the repository's established PostgreSQL JSON/JSONB and driver
conventions. `json.RawMessage` may be used in the Go model if it is
compatible with the actual driver and repository helpers.

Before persistence, the service must:

-   Resolve the registered definition.
-   Reject unknown keys.
-   Confirm the target scope is allowed.
-   Validate the JSON representation and expected primitive type.
-   Reject malformed JSON, unsupported schema versions, NaN, infinity,
    and invalid numeric ranges.
-   Apply the registered validator where configured.
-   Enforce payload size and any registered JSON schema constraints.
-   Reject actual secrets when a setting is marked
    `secret_reference_only`.

Do not invent default values or business rules. Defaults must be
explicitly defined and validated.

## 7. Effective-value resolution

For Phase 1, the resolver supports only system and organization scope.

For an organization caller:

1.  Load the approved definition.
2.  Validate the authenticated organization context.
3.  If organization scope is permitted and an organization override
    exists, use it.
4.  Otherwise, if system scope is permitted and an explicit system value
    exists, use it.
5.  Otherwise, use the validated definition default only when
    `default_value` is present.
6.  If no valid value exists for a required setting, return the
    established domain error; do not invent a default.

A system administrator's access to system values must follow the
explicit administrative policy. Resolution results should carry the
effective value, type, source scope, version, and schema version
internally and expose only fields approved by the response DTO.

Security policy, RBAC permissions, authentication rules, mandatory
auditing, rate limits, and security controls must never be weakened
through ordinary settings.

## 8. Persistence and database constraints

Logical Phase 1 tables:

  -----------------------------------------------------------------------------------
  Table                           Responsibility          Required integrity
  ------------------------------- ----------------------- ---------------------------
  `setting_definitions`           Controlled setting      Unique key, valid type,
                                  registry                positive schema version,
                                                          allowed scopes

  `system_setting_values`         Explicit system         Unique key; valid
                                  overrides               definition

  `organization_setting_values`   Organization overrides  Unique
                                                          `(organization_id, key)`;
                                                          valid organization and
                                                          definition

  `setting_revisions`             Append-only history     Scope-aware identity and
                                                          useful history indexes
  -----------------------------------------------------------------------------------

These are logical requirements, not permission to replace existing
schema conventions blindly. Inspect current migrations, `BaseModel`,
UUID types, timestamps, soft-delete rules, and foreign-key patterns
first.

Database requirements:

-   Unique constraints for setting keys and scope ownership.
-   Foreign keys for definitions, organizations, and actors where
    appropriate.
-   Positive schema versions and value versions (`version >= 1`).
-   Database-enforced append-only revisions and scope identity
    consistency; application comments alone do not make a table
    immutable.
-   Indexes for effective-value lookups and revision history.
-   Parameterized SQL and explicit column lists.
-   No table names or SQL identifiers derived from client input.
-   Revision writes and setting writes in the same transaction.
-   A migration strategy compatible with the existing migration
    framework.

Do not introduce a second migration system or duplicate infrastructure.

The repository already has a legacy `system_settings` table. The new
forward migration copies its live rows into the new schema and retains
the legacy table for rollback. `cmd/seed` now writes definitions and
system values to the new schema. The legacy table
stores text values, combines global and organization rows, uses
`UNIQUE (organization_id, key)` (which does not prevent duplicate
system rows when `organization_id` is NULL), and has no revision/version
or definition registry. The new tables must not be created alongside it
without a data migration. The forward migration normalizes and transfers
existing global/organization rows, fails on duplicate global keys or
conflicting value types rather than silently choosing a row, validates
legacy values, and retains the legacy table. The down migration copies
current live values back to that table before dropping the replacement
tables. Revision history has no representation in the legacy schema, so
it must be backed up before rollback. Do not drop the legacy table until
the replacement seeder and runtime are deployed and verified.

The current `system_settings.yaml` uses `int`, `bool`, and `duration`
aliases and stores every value as a YAML string. The new registry uses
`integer`, `boolean`, and JSON-typed values; migration/seeding must parse
the legacy representation and serialize a valid JSON scalar. `duration`
has no Phase 1 type and must become a validated string, not a new
implicit runtime type. The settings seeder must be made idempotent and
transaction-safe against unique conflicts.

Legacy `is_sensitive` moves to the corresponding `SettingDefinition`;
it is not mutable value-row metadata. Migrated legacy keys are initially
marked deprecated and are not exposed or mutable until reviewed. Invalid
legacy keys, types/values, duplicate global keys, and conflicting
definitions stop the migration with an actionable error. Legacy
`feature.*` entries do not implicitly become settings or feature-flag
definitions.

Seeded definitions default to `deprecated`. A seed entry may set
`status: active` only after its owning module reads and validates the
database-backed value; the setting seed is authoritative for that status.

`internal/platform/config` loads process configuration from YAML and
environment variables during startup. It is the authoritative source
for infrastructure and security controls such as database, Redis, JWT,
password, rate-limit, and server settings. Database-backed settings do
not override those startup values, and must not claim to dynamically
change them. Only explicitly registered business/application settings that their
owning modules actually read at runtime may be mutable here. The current
legacy seed catalog has not been verified against runtime consumers, so
its definitions remain deprecated. Review and wire each owning module
before activating a setting; configuration stored by this API does not
automatically alter application behavior.

## 9. Authorization and middleware

Reuse the existing authentication, session validation, tenant context,
RBAC, rate limiting, audit, response, and error infrastructure.

Required canonical permissions:

-   `settings:view`
-   `settings:update`

These match the repository's existing `module:action` permission naming.
`settings:view` and `settings:update` do not by themselves authorize
system-scope operations or sensitive disclosure: those require the
existing privileged platform policy. The settings service must
distinguish system and organization authorization even though the two
existing endpoint permissions are shared.

Use the repository's actual canonical permission format consistently. If
existing seeds or middleware use a different separator, reconcile it
before contract freeze rather than silently creating aliases.

Authorization requirements:

-   System-wide mutations require the explicitly approved platform
    administrator policy.
-   Organization mutations require permission for the active
    organization.
-   Tenant isolation is enforced for both reads and writes.
-   Sensitive reads require the approved privileged policy.
-   The handler does not implement role policy through scattered
    hardcoded role checks.
-   Setting values cannot grant permissions or bypass RBAC.
-   Do not create a parallel authentication, audit, rate-limit, or error
    framework.

Rate policies:

-   Reads: `rate_auth_read`
-   Writes: `rate_auth_write`

Confirm middleware registration and ordering against the existing
router.

## 10. Service, repository, and handler boundaries

Dependency direction:

`Handler → Settings Service → Settings Repository → PostgreSQL`

### Handler

Responsible only for HTTP parsing, DTO construction, service invocation,
and response mapping.

### Service

Responsible for:

-   Definition lookup and lifecycle validation.
-   Value/type/schema validation.
-   Scope and authorization-context validation through existing
    mechanisms.
-   Effective-value resolution.
-   Optimistic concurrency.
-   Atomic bulk-update orchestration.
-   Revision creation.
-   Transaction boundaries.
-   Safe disclosure/redaction policy.
-   Post-commit cache invalidation/events only if later enabled.

### Repository

Responsible for explicit database operations, parameterized queries,
transaction-aware persistence, and retrieving/writing values and
revisions. It must not implement HTTP behavior or make RBAC decisions.

### Definition registry

Definitions are provisioned through reviewed code/seed changes. Do not
expose public definition-creation endpoints in Phase 1.

## 11. Transactions and concurrency

### Optimistic concurrency

Each mutable value has a monotonically increasing `version`.

-   A missing value has version `0`; an inserted row starts at version
    `1`.
-   `expected_version` is mandatory on every update and every bulk item.
    A value of `0` is valid only when no row exists; otherwise it must
    match the current positive version.
-   Successful updates advance the version.
-   If the expected version does not match, return the project's
    standard conflict error.
-   Do not silently overwrite a stale update.

The DTO validates JSON syntax and payload bounds only. Definition lookup,
primitive type validation, schema/validator execution, numeric bounds,
secret-reference validation, and default validation belong to the
service and must happen before persistence.

### Atomic bulk updates

The entire bulk operation runs in one database transaction:

1.  Validate request size and duplicate keys.
2.  Load all definitions and current values.
3.  Validate every entry's scope and authorization.
4.  Validate every value.
5.  Check expected versions.
6.  Persist all values and corresponding revisions.
7.  Commit only if every entry succeeds.

Any failure rolls back all value and revision changes.

## 12. Sensitive configuration and audit

`is_sensitive` is definition-owned classification, not a client-writable
property.

-   Never store actual credentials, API keys, private keys, tokens, or
    database secrets.
-   Use approved secret references where necessary.
-   Never include secret material in API responses, revisions, logs,
    errors, caches, or events.
-   Restrict sensitive configuration reads and reference updates.
-   Reuse the existing technical/security audit infrastructure.
-   Configuration revisions complement audit logging; they do not
    replace it.

## 13. Caching and events

Phase 1 does not require Redis caching or event publication.

If a demonstrated requirement later justifies either capability:

-   PostgreSQL remains authoritative.
-   Cache invalidation occurs after commit.
-   Cache keys include all applicable scope dimensions.
-   Tenant-specific values cannot leak across organizations.
-   Redis failures fall back to PostgreSQL where safe.
-   Events are published only after commit, preferably through existing
    transactional outbox infrastructure.
-   Event payloads contain metadata and versions, never secret values.

Do not introduce a scheduler, outbox, or cache solely for architectural
appearance.

## 14. API and error behavior

Follow existing response envelopes and error types.

Expected HTTP semantics:

-   `400` --- malformed or invalid input.
-   `401` --- missing/invalid authentication.
-   `403` --- insufficient permission or prohibited scope.
-   `404` --- unknown or intentionally undisclosed setting.
-   `409` --- stale version or conflicting mutation.
-   `429` --- rate limit exceeded.
-   `500` --- unexpected internal error.
-   `503` --- required configuration/security infrastructure
    unavailable.

The OpenAPI 3.1 contract is authoritative for request and response
shapes. Do not change endpoint paths or introduce new endpoints without
updating OpenAPI and tests.

## 15. Testing requirements

### Unit tests

-   Unknown key and inactive/deprecated definition behavior.
-   Type validation and malformed JSON.
-   Default-value validation and resolution.
-   System/organization precedence.
-   Scope restrictions.
-   Explicit `false`, `0`, and empty values where allowed.
-   Sensitive-value redaction.
-   Optimistic concurrency conflicts.
-   Bulk duplicate-key rejection.
-   Error mapping.

### Database integration tests

-   Unique constraints.
-   Organization isolation.
-   Foreign-key enforcement.
-   Version advancement.
-   Revision immutability through application operations.
-   Atomic rollback when one bulk entry fails.
-   Transaction-aware repository behavior.
-   Migration and seed correctness.

### API/security tests

-   Anonymous access rejection.
-   Permission enforcement.
-   Cross-organization access rejection.
-   System mutation privilege restrictions.
-   Sensitive-read restrictions.
-   Correct middleware/rate-limit/audit registration.
-   Request/response DTO and OpenAPI consistency.
-   Existing route compatibility.

Do not claim database integration coverage when only mocks were used.

## 16. Recommended Phase 1 file structure

Follow the existing flat module convention:

-   `model.go` --- Phase 1 persistence/domain models.
-   `dto.go` --- request, response, and query DTOs.
-   `definition.go` --- typed definitions and validator registration, if
    needed.
-   `resolver.go` --- system/organization effective-value resolution.
-   `repository.go` --- explicit scope-specific persistence.
-   `service.go` --- validation, transactions, concurrency, and
    orchestration.
-   `handler.go` --- HTTP handling.
-   `routes.go` --- route registration using existing middleware.
-   `seed.go` --- approved definitions and permission seeds, if
    consistent with repository conventions.
-   `settings_test.go` --- focused tests, plus integration tests using
    existing project conventions.

Do not create empty files or new packages without implemented behavior.
Reuse existing migrations, seed, router, audit, and OpenAPI locations.

## 17. Implementation sequence

1.  Inspect existing `BaseModel`, JSON/DB types, migrations, repository
    patterns, router, RBAC, audit, and error conventions.
2.  Freeze the approved runtime keys, types, validators, scope policy,
    and permission naming after auditing seed/config consumers.
3.  Update OpenAPI request/response schemas for the existing four
    endpoints.
4.  Implement the four Phase 1 models: `SettingDefinition`,
    `SystemSetting`, `OrganizationSetting`, and `SettingRevision`.
5.  Implement migrations and database constraints.
6.  Implement DTOs and validation.
7.  Implement repository operations.
8.  Implement definition registry and resolver.
9.  Implement service transactions, concurrency, revisions, and atomic
    bulk updates.
10. Implement handlers and route registration.
11. Integrate existing middleware and seeds.
12. Add tests and run the full build/test/lint suite.
13. Review security, tenant isolation, migration behavior, and OpenAPI
    consistency.

Do not implement future branch/user/feature-flag capabilities as part of
this phase.

## 18. Definition of done

Phase 1 is complete when:

-   Only registered definitions can be mutated.
-   Values are validated against their declared type and rules.
-   System and organization scopes are enforced.
-   Effective-value resolution is deterministic and tested.
-   Tenant isolation is enforced.
-   Sensitive values and secrets are protected.
-   Updates use the agreed concurrency policy.
-   Bulk updates are atomic.
-   Revision history is written transactionally and is append-only
    through the application interface.
-   Existing middleware and RBAC are integrated.
-   Migrations and seeds work from a clean database.
-   OpenAPI matches actual routes and DTOs.
-   Tests cover the required functional, database, and security
    behavior.
-   No unrelated completed modules are rewritten.

## 19. Decisions to freeze before implementation

The following contract decisions are frozen:

1.  Permissions use the existing `settings:view` and `settings:update`
    names (`module:action`); system scope and sensitive disclosure also
    require the existing privileged platform authorization policy.
2.  Registered keys, value types, defaults, and validators must be
    reviewed against runtime consumers before the registry is seeded.
    The legacy YAML catalog is not proof that each key is a live, safe
    setting.
3.  Definitions explicitly allow `system`, `organization`, or both.
4.  Writes require a `scope` selector; reads use `scope=effective` by
    default, with explicit `system`/`organization` reads authorized by
    policy. Organization identity always comes from trusted context.
5.  System operations and sensitive-value disclosure require the
    project's privileged platform authorization policy.
6.  `include_sensitive=true` is available only under that policy and
    never reveals actual secret material; revisions always redact.
7.  Inserted values start at version 1; `expected_version` is mandatory
    and uses 0 for create-only, otherwise the current positive version.
8.  Bulk JSON is `{"scope":"...","items":[...]}`, with 1–100 unique
    keys, per-value limit 64 KiB, and aggregate value limit 1 MiB.
9.  Revision rows are append-only, transactionally inserted, and retain
    no sensitive values; non-sensitive revision retention must be set
    with the migration/operations policy before deployment.
10. `json.RawMessage` is the in-memory JSON representation. Confirm pgx
    JSONB encoding/decoding with integration tests when implementing
    the repository.

Branch settings, user preferences, feature flags, Redis caching, and
outbox events require a separate approved change before implementation.

------------------------------------------------------------------------

## Final instruction

Treat this contract as the Phase 1 implementation boundary. Preserve the
existing Pharmaciano architecture and the four current settings
endpoints. Keep the model as small as possible while meeting the
approved requirements. Add a model, table, permission, or endpoint only
when a defined requirement needs it, and update the model, migrations,
DTOs, OpenAPI, tests, and contract together.

**Target:** a secure, deterministic, versioned system/organization
configuration module---not an unrestricted key-value CRUD service and
not a premature implementation of every possible configuration feature.
