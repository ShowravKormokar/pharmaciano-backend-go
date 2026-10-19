# Pharmaciano:ERP --- Medicine Module Contract

**File:** `MEDICINE_CONTRACT.md`\
**Module:** `internal/modules/medicine`\
**Status:** Architecture contract; approve schema decisions before
implementation.\
**Architecture:** Go modular monolith, Gin, PostgreSQL, Redis only where
justified, OpenAPI 3.1 contract-first.

**Basis:** Astra `answer_in_markdown.md` (Medicine module), MediCore ERP
plan, the supplied Medicine model sketch,
`MASTERDATA_IMPLEMENTATION_CONTRACT.md`, and the supplied
`000005_master_data.up.sql`.

> This is a target contract, not proof that every proposed table,
> worker, outbox, or middleware already exists. Inspect the repository
> and current migrations before coding. Reuse existing infrastructure
> and do not invent parallel systems.

## 1. Executive decisions

1.  **One central catalog per organization, shared by every branch and
    warehouse in that organization.** Branches must not create separate
    copies of the same medicine. Inventory batches, stock, expiry,
    purchase facts, cost, branch price policy, and reorder policy remain
    operational records owned by their respective modules.
2.  **"Central" does not mean shared across unrelated organizations.**
    Astra describes an organization-central catalog and tenant-scoped
    medicine identity/SKUs. Keep tenant isolation. If a shared
    international/national reference formulary is later needed, design
    it as a separate governed reference layer; do not expose one
    tenant's commercial catalog to another.
3.  Medicine is not one flat row. Separate formulation identity,
    composition, sellable/stockable variants, variant-specific barcodes,
    regulatory profiles, and immutable revisions.
4.  A variant is a package identity. "Napa 500 mg, 10 tablets" and "Napa
    500 mg, 100 tablets" can share the formulation but need distinct
    variant/SKU identities.
5.  Do **not** copy the supplied `model.go` sketch unchanged. It mixes
    identity, package conversion, price, cost, and reorder policy.
    `MRP`/`DefaultCost` do not belong as authoritative Medicine fields;
    price history belongs to Pricing, cost to Purchase/Inventory
    valuation, and stock thresholds to Inventory policy.
6.  MasterData remains the source of existing dictionaries. Reuse
    `product_categories`, `dosage_forms`, `routes`, `package_types`,
    `unit_types`, `storage_conditions`, `tax_rates`,
    `generic_medicines`, and existing `brands`/`manufacturers`. Do not
    duplicate these tables/models.
7.  The supplied `000005_master_data.up.sql` does **not** create
    Medicine, composition, variant, barcode, revision, or import-job
    tables. Design a new forward migration after schema decisions; never
    edit a migration that may already have been applied.
8.  Under the supplied policy, only `SUPER_ADMIN` may write the central
    catalog. Other authorized branches may read it through
    authenticated, permission-gated APIs.
9.  POS lookup prioritizes exact SKU/barcode. Fuzzy search must not
    silently resolve ambiguity or make clinical substitution decisions.
10. Protect historical meaning. Once a medicine/variant is referenced by
    stock, purchases, or sales, do not silently rewrite formulation,
    strength, unit conversion, barcode ownership, or regulatory history.
11. PostgreSQL is the source of truth. No Redis cache, search engine,
    event bus, or Medicine-only queue by default. Use only verified
    shared infrastructure and measured need.
12. `GET /medicines/{id}/price-history` must delegate to the approved
    Pricing interface if one exists. Do not create a competing Medicine
    price-history table.

## 2. Purpose and boundaries

### Medicine owns

-   Product/formulation identity and catalog lifecycle.
-   MasterData and Brand/Manufacturer references.
-   One or more active ingredients with normalized strength/composition.
-   Package variants, SKUs, exact unit conversion, and barcode
    identifiers.
-   Catalog regulatory facts
    (prescription/controlled/refrigeration/import provenance) with
    appropriate provenance/effective dates.
-   Immutable revisions and business audit evidence for sensitive
    changes.
-   Bounded catalog search, exact POS lookup, and authorized
    import/export job requests.

### Medicine does not own

-   Branch/warehouse inventory, batch number, expiry, quantity, stock
    movement, FEFO.
-   Purchase orders, receiving, supplier balances, acquisition cost,
    inventory valuation.
-   Branch selling price, discounts, price books, authoritative price
    history.
-   Reorder thresholds or replenishment policy.
-   Supplier/manufacturer purchasing relationships, prescription
    records, dispensing workflow, controlled-drug registers, or clinical
    decision support.
-   External national drug database sync without a separate approved
    integration contract.
-   A global shared catalogue across unrelated tenants without a
    deliberate governance/data-isolation design.

## 3. Scope model

``` text
Organization
  ├── Central medicine catalog (shared across its branches)
  │     ├── Medicine/formulation
  │     ├── Composition (one or more ingredients)
  │     ├── Variants (package + SKU)
  │     │     └── Barcodes (variant-specific)
  │     ├── Regulatory profile(s) and immutable revisions
  │     └── MasterData / Brand / Manufacturer references
  ├── Branch A: inventory batches, expiry, stock movements, cost layers
  └── Branch B: inventory batches, expiry, stock movements, cost layers
```

Example: both branches reference the same medicine and variant IDs.
Branch A may hold 240 tablets across two batches while Branch B has zero
stock. Catalog identity is shared; stock, expiry, and cost are not.

**Tenant boundary:** never accept `organization_id`, `branch_id`, or
`warehouse_id` from a Medicine DTO as authority. Resolve trusted scope
from authenticated request context. Astra's current design is
organization-central, not cross-tenant global. "All branches can read"
does not mean public or unauthenticated.

## 4. Dependencies and ownership

  -----------------------------------------------------------------------
  Dependency                          Rule
  ----------------------------------- -----------------------------------
  `common`, `platform/db`, UUID       Reuse actual shared implementations
  utilities, `pkg/pagination`         and conventions

  Auth/session middleware             Do not reimplement authentication

  Native RBAC                         Use existing enforcer/route policy;
                                      do not add a parallel authorization
                                      system

  MasterData                          References only; no duplicate
                                      dictionaries

  Brand/Manufacturer                  Reuse existing ownership/schema and
                                      validate references

  Inventory                           Owns batch, expiry, stock, FEFO and
                                      replenishment policy; references
                                      variant

  Purchase                            Owns purchase/receiving facts and
                                      cost; references variant

  POS/Sales                           Uses exact lookup but independently
                                      validates stock, price and
                                      regulatory requirements

  Pricing, if implemented             Owns price books/history; Medicine
                                      endpoint is only a compatibility
                                      adapter

  Existing job/storage framework, if  Reuse for import/export; verify
  present                             before claiming async support
  -----------------------------------------------------------------------

The supplied migration shows `brands.manufacturer_id` is `NOT NULL` and
references `manufacturers(id)` with `ON DELETE RESTRICT`. It also shows
soft-delete timestamps and partial unique indexes on code. It does not
show `organization_id` on these reference tables. Check later migrations
and deployed schema before assuming their tenant scope.
`generic_medicines.is_controlled` is not by itself the final legal
status for every product/jurisdiction. `unit_types` has a
base-unit/conversion factor, but its dimensional semantics must be
verified before using it for medicine calculations.

## 5. Domain model

  ----------------------------------------------------------------------------------------------------
  Entity                        Responsibility                          Lifecycle
  ----------------------------- --------------------------------------- ------------------------------
  `Medicine`                    Stable formulation/product identity and Active/inactive/soft-deleted
                                catalog status                          

  `MedicineComposition`         One active ingredient and normalized    Versioned; material changes
                                strength component                      create a new revision or
                                                                        identity

  `MedicineVariant`             Sellable/stockable package, SKU, exact  Deactivate; semantic package
                                conversion                              change creates a new variant

  `MedicineBarcode`             Identifier for a specific variant       Deactivate; never silently
                                                                        reassign

  `MedicineRegulatoryProfile`   Jurisdiction/effective-date-sensitive   Effective-dated/versioned
                                restrictions, if adopted                

  `MedicineRevision`            Immutable catalog snapshot, actor and   Append-only
                                reason                                  

  `MedicineImportJob` /         Durable async job state and outcome     Retained per policy
  `MedicineExportJob`                                                   
  ----------------------------------------------------------------------------------------------------

### 5.1 Composition and strength

-   Support combination medicines with multiple ingredient rows.
-   Store numeric values and units, not only display text such as
    `"500 mg"`.
-   For concentrations/ratios, model numerator, denominator, and units
    explicitly; paired denominator fields must be validated.
-   Use decimal-safe PostgreSQL `NUMERIC` semantics for strength and
    package conversions, not binary floating point.
-   Never parse display strings during checkout or stock conversion.
-   Confirm `unit_types` supports mass/volume/count dimensions. If not,
    add a dedicated measurement-unit model with explicit dimensions
    rather than overloading it.
-   Reject non-positive values and incompatible dimensions; mg cannot be
    converted to mL without concentration/formulation semantics.

### 5.2 Variant/package conversion

Each variant should define medicine ID, SKU, package label/type, base
unit, exact quantity per package, whether it can be broken for partial
sale, and active state. Avoid assuming every package is an integer
count; some products require fractional quantities or dimensional
conversions.

**Critical invariant:** after a variant is referenced by purchase,
inventory movement, or sale, do not mutate its base unit/conversion
factor in place. Create a new variant and preserve historical conversion
snapshots. Label-only corrections may be allowed with audit.

### 5.3 Barcode

-   Attach barcodes to variants, not only the medicine.
-   Support multiple identifiers per variant only where the business
    permits.
-   Normalize and enforce exact uniqueness at the approved scope in
    PostgreSQL.
-   Validate symbology without assuming every code is EAN-13.
-   Never silently transfer a barcode between variants.
-   If more than one active variant matches, treat it as an
    integrity/ambiguity error and block automatic POS resolution.

### 5.4 Regulatory profile

-   Prescription and controlled status may vary by jurisdiction and
    time. Prefer effective-dated profiles for multi-jurisdiction
    support.
-   If first release is explicitly Bangladesh-only, flat flags can be a
    documented simplification, but must be audited and versioned; do not
    imply they are globally correct.
-   Regulatory changes require elevated authorization, a reason, audit
    evidence and revision history.
-   Unknown/conflicting status must not be interpreted as unrestricted
    sale. The sale/dispensing workflow must fail closed where a safety
    decision is required.
-   Catalog flags do not replace prescription/dispensing/controlled-drug
    workflows.
-   Astra suggests importer identity may reference a supplier, while the
    supplied sketch uses `ImporterName`. These are not equivalent.
    Decide whether the domain needs a verified importer entity, supplier
    link, license/evidence reference, or a combination before migration.
    Never assume supplier equals manufacturer.

## 6. Proposed persistence design

This is a logical target, not a claim these tables exist. Verify the
actual migration conventions, shared model, transaction helpers and
deployed schema first.

  -----------------------------------------------------------------------
  Table                               Responsibility and constraints
  ----------------------------------- -----------------------------------
  `medicines`                         Stable identity, approved
                                      references, active/archive state;
                                      tenant-scoped uniqueness where
                                      required; optional version for
                                      optimistic locking

  `medicine_compositions`             FK to medicine and
                                      ingredient/generic identity;
                                      positive strength numerator; paired
                                      denominator fields/units; prevent
                                      duplicate composition rows

  `medicine_variants`                 FK to medicine; SKU uniqueness at
                                      agreed scope; package/base-unit
                                      references; conversion factor \> 0;
                                      protect conversion after
                                      operational use

  `medicine_barcodes`                 FK to variant; normalized exact
                                      barcode uniqueness; indexed lookup;
                                      symbology and lifecycle

  `medicine_regulatory_profiles`      Optional effective-dated
                                      jurisdiction rules; valid date
                                      ranges and no overlapping profiles
                                      under the chosen policy

  `medicine_revisions`                Immutable snapshot and
                                      reason/actor; unique
                                      medicine/revision; indexed history

  `medicine_import_jobs`              Actor, file reference, checksum,
                                      mode, state, counters and
                                      timestamps; no public filesystem
                                      paths

  `medicine_import_row_errors`        Bounded per-row diagnostics; safe
  (optional)                          codes, no secret/raw-file leakage

  `medicine_export_jobs`              Actor, filters, state, artifact
                                      reference and expiry; authorize
                                      every status/download request
  -----------------------------------------------------------------------

### Schema decisions that block migration

1.  **SKU ownership:** recommendation is variant-level; medicine may
    have a separate catalog code only if needed.
2.  **Category cardinality:** one category FK or many-to-many mapping;
    choose based on actual business requirement.
3.  **Ingredients:** determine whether `generic_medicines` is the
    ingredient identity for the first release; composition still needs
    multiple rows for combinations.
4.  **Units:** verify mass/volume/count dimensions and exact
    conversions.
5.  **Regulatory scope:** Bangladesh-only flat flags vs effective-dated
    jurisdiction profiles.
6.  **Catalog scope:** organization-central (recommended/current Astra
    design) vs a separate governed global reference dataset.
7.  **Tax:** tax classification/rules and transaction snapshots must be
    owned by the approved tax/sales contract.
8.  **Infrastructure:** verify outbox, worker, object-storage and
    business-audit implementations before depending on them.

Do not add fields merely because they appear in a draft. Each field
needs an owner, invariant, validation rule, and consuming workflow.

## 7. Business invariants

  -----------------------------------------------------------------------
  Concern                             Rule
  ----------------------------------- -----------------------------------
  Central identity                    One organization catalog identity
                                      reused across its branches

  Tenant isolation                    No cross-organization access unless
                                      explicitly designed as global
                                      reference data

  References                          Reject missing/deleted/unusable
                                      references; database FKs are final
                                      integrity guard

  Composition                         Combination products use structured
                                      ingredient rows

  Strength                            Numeric value + unit; no parsing
                                      free text at runtime

  Variant identity                    Package configurations have
                                      distinct variant/SKU identities

  Conversion                          Exact, positive, dimensionally
                                      valid; no float-based stock
                                      arithmetic

  Barcode                             Variant-specific, normalized,
                                      unique and exact

  History                             Never reinterpret old stock/sales
                                      by mutating formulation/conversion

  Regulatory changes                  Elevated permission, reason, audit
                                      and revision

  Deletion                            Soft-delete/archive; retain
                                      historical references

  Pricing/cost                        Not Medicine-owned

  Replenishment                       Inventory/branch/warehouse policy

  Duplicate products                  Similar names may warn; never
                                      auto-merge by fuzzy match

  Availability                        Catalog lookup does not prove stock
                                      availability

  Substitution                        Search similarity is not a clinical
                                      substitution recommendation
  -----------------------------------------------------------------------

## 8. API contract

The route inventory below preserves the supplied draft. **Verify rate
names against the actual limiter registry.** The MasterData review
identified a mismatch between draft names
(`rate_auth_read`/`rate_auth_write`) and configured names
(`auth_read`/`auth_write`). Never use an unregistered policy key.

  ----------------------------------------------------------------------------------------------------------------------
  Method & path                                           Rate (verify)       Permission           Behavior
  ------------------------------------------------------- ------------------- -------------------- ---------------------
  `POST /api/v1/medicines`                                `rate_auth_write`   `medicines:create`   Create aggregate;
                                                                                                   composition/initial
                                                                                                   variants/revision
                                                                                                   transactionally

  `GET /api/v1/medicines`                                 `rate_auth_read`    `medicines:view`     Bounded paginated
                                                                                                   list

  `GET /api/v1/medicines/{id}`                            `rate_auth_read`    `medicines:view`     Detail with joined
                                                                                                   reference labels

  `PATCH /api/v1/medicines/{id}`                          `rate_auth_write`   `medicines:update`   Partial update of
                                                                                                   allow-listed fields

  `PUT /api/v1/medicines/{id}`                            `rate_auth_write`   `medicines:update`   Full replacement only
                                                                                                   if optional/null
                                                                                                   semantics are fully
                                                                                                   defined; otherwise
                                                                                                   defer

  `DELETE /api/v1/medicines/{id}`                         `rate_auth_write`   `medicines:delete`   Soft-delete/archive
                                                                                                   with reference checks

  `GET /api/v1/medicines/search?q=`                       `rate_search`       `medicines:view`     Bounded text search;
                                                                                                   not exact lookup

  `POST /api/v1/medicines/lookup`                         `rate_auth_read`    `medicines:view`     Exactly one barcode
                                                                                                   or SKU

  `GET /api/v1/medicines/{id}/variants`                   `rate_auth_read`    `medicines:view`     List variants

  `POST /api/v1/medicines/{id}/variants`                  `rate_auth_write`   `medicines:update`   Add variant

  `PATCH /api/v1/medicines/{id}/variants/{variant_id}`    `rate_auth_write`   `medicines:update`   Update permitted
                                                                                                   fields; protect
                                                                                                   conversion

  `DELETE /api/v1/medicines/{id}/variants/{variant_id}`   `rate_auth_write`   `medicines:update`   Deactivate/archive

  `GET /api/v1/medicines/{id}/barcodes`                   `rate_auth_read`    `medicines:view`     List barcodes

  `POST /api/v1/medicines/{id}/barcodes`                  `rate_auth_write`   `medicines:update`   Add variant barcode

  `DELETE /api/v1/medicines/{id}/barcodes/{bc_id}`        `rate_auth_write`   `medicines:update`   Deactivate
                                                                                                   assignment; preserve
                                                                                                   history

  `GET /api/v1/medicines/{id}/price-history`              `rate_auth_read`    `medicines:view`     Pricing adapter only;
                                                                                                   block if
                                                                                                   owner/interface is
                                                                                                   absent

  `POST /api/v1/medicines/import`                         `rate_export` in    `medicines:import`   Validate upload and
                                                          draft                                    enqueue staged
                                                                                                   import; verify policy
                                                                                                   or define a dedicated
                                                                                                   configured one

  `GET /api/v1/medicines/export?format=csv\|xlsx`         `rate_export`       `medicines:export`   Prefer async job for
                                                                                                   nontrivial exports
  ----------------------------------------------------------------------------------------------------------------------

### 8.1 Authorization and middleware

-   Under the supplied policy, writes require `SUPER_ADMIN` **and** the
    specific permission. Use existing native RBAC and route protection,
    not ad hoc role checks scattered in handlers.
-   All reads are authenticated and permission-gated; "all branches
    read" is not public access.
-   Use existing request ID/recovery, auth/session, trusted scope
    resolution, RBAC, rate-limit, idempotency, audit, and observability
    middleware in the project's established order. Inspect actual route
    composition; do not blindly duplicate middleware.
-   UUIDs use shared parsing utilities. Responses/errors use shared
    envelope and error contract.
-   Resolve organization/user IDs only from trusted context. Do not
    accept tenant IDs in request bodies as authority.
-   Every route, job status and download must enforce scope and
    permission.
-   Verify exact configured rate-policy names. Draft labels are not
    proof of runtime configuration.

### 8.2 Filters and lookup

List filters: `category_id`, `brand_id`, `generic_id`, `is_active`,
`requires_prescription`, `is_controlled`, `q`, plus shared pagination.
Validate UUIDs/booleans, query length, page size, sort key and
direction. Use deterministic ordering with a unique tie-breaker.
Distinguish omitted boolean from `false`; never accept raw SQL
sort/filter expressions.

Lookup request must contain exactly one of `barcode` or `sku`. Zero
matches returns not found; multiple active matches returns an
integrity/ambiguity error. Inactive/deleted variants are not eligible
for ordinary POS lookup. Return catalog/variant metadata, not stock
availability or final price. Sale workflow separately revalidates branch
stock, price and safety requirements.

### 8.3 Import/export

-   Import requires `medicines:import`; export requires
    `medicines:export`.
-   Validate file size, content type, format, row/column/cell limits and
    parser behavior. Do not trust extension alone; reject unsupported
    macros/formats.
-   Store files using approved object storage with generated keys; never
    accept user-controlled paths.
-   Create durable job records with checksum and idempotency;
    parse/validate in bounded batches.
-   Default to validating the file and reporting errors before applying
    changes, unless an explicit partial-import policy is approved.
-   Formula-escape CSV/XLSX exports; authorize every status/download
    request; use short-lived signed URLs only if shared storage supports
    them.
-   Keep job state in PostgreSQL. Queue messages are not the source of
    truth; retries must be safe and workers reconcilable.
-   The supplied draft uses `rate_export` for import. Verify
    intent/configuration rather than silently trusting this naming.

## 9. Service/repository architecture

Use the repository's actual layout and patterns. A reasonable target, if
consistent with adjacent modules:

``` text
internal/modules/medicine/
  model.go
  dto.go
  mapper.go
  repository.go
  repository_postgres.go
  service.go
  handler.go
  routes.go
  *_test.go
```

-   **Model:** maps approved SQL columns; no HTTP logic, SQL business
    rules, or authorization.
-   **DTO:** separate create, patch, replace, list query, lookup,
    medicine response, variant and barcode DTOs. Never decode request
    JSON directly into a persistence model.
-   **Mapper:** pure model/DTO conversion; no DB queries or business
    policy.
-   **Service:** authorization-sensitive business rules, reference
    validation, lifecycle, composition/unit rules, revisions and
    transactions.
-   **Repository:** parameterized SQL, explicit scope, constraints and
    transaction-aware writes.
-   **Handler:** HTTP ↔ DTO, validation, service invocation, shared
    response/error mapping only.

Use `common.BaseModel` only if its fields match the approved schema. Do
not embed `AuditColumns` unless `created_by`/`updated_by` columns exist
or a migration intentionally adds them. Match actual DB transaction
helpers; do not invent calls such as `db.FromCtx(ctx)` unless they
exist.

Illustrative repository operations (adapt signatures to actual
conventions): `CreateMedicineAggregate`, `GetMedicine`, `ListMedicines`,
`SearchMedicines`, `LookupVariantBySKU`, `LookupVariantByBarcode`,
`ListVariants`, `CreateVariant`, `UpdateVariant`, `CreateBarcode`,
`DeactivateBarcode`, `AppendRevision`, `ArchiveMedicine`.

### Performance

-   Parameterized SQL only.
-   B-tree/unique indexes for exact SKU/barcode lookups.
-   Batch-load reference labels to avoid N+1.
-   Select only needed list columns; do not join
    inventory/purchases/sales into general catalog list/detail queries.
-   Use trigram indexes only where extension support and measured
    workload justify them.
-   Bound import batches and avoid one giant transaction for large
    files.
-   Measure PostgreSQL query plans before adding Redis or a dedicated
    search engine.

## 10. Transactions, idempotency and history

-   Create medicine + composition + optional initial variants + first
    revision atomically.
-   Database constraints are the final defense against concurrent
    duplicate SKU/barcode requests; map unique violations to stable
    conflict errors.
-   Use optimistic concurrency if supported by project conventions.
-   Composition/regulatory mutations append a revision in the same
    transaction.
-   Use the existing idempotency implementation for create/import/export
    submissions if available. Database uniqueness and durable
    idempotency results are stronger than a Redis lock alone.
-   If a transactional outbox exists, write outbox events in the same
    transaction. If absent, do not claim in-memory post-commit events
    are durable; add a shared outbox contract only when approved.
-   Never keep a DB transaction open while parsing files or generating
    an export.
-   `DELETE` means soft-delete/archive, not physical delete. If
    referenced by operational records, normally deactivate and preserve
    identity. Do not cascade-delete history.
-   Once operationally referenced, protect semantic
    formulation/conversion fields. Descriptive corrections can be
    allowed under audit if they do not change product meaning.
-   Historical invoices and inventory movements must remain renderable
    using their transaction snapshots/references.

## 11. Security and pharmacy safety

  -----------------------------------------------------------------------
  Risk                                Control
  ----------------------------------- -----------------------------------
  Cross-tenant IDOR                   Trusted scope in every repository
                                      query; authorization on every
                                      request; isolation tests

  Privilege escalation                Existing RBAC plus SUPER_ADMIN
                                      write policy; no client-supplied
                                      actor/tenant

  Mass assignment                     Explicit DTO allow-lists

  Unsafe regulatory change            Elevated permission, reason,
                                      immutable revision/audit, effective
                                      dates where needed

  Barcode collision/hijack            DB uniqueness, exact lookup,
                                      audited deactivation/reassignment

  Product misidentification           Structured composition/strength and
                                      variant-specific barcode; ambiguity
                                      fails closed

  Historical corruption               Immutable transaction snapshots;
                                      protect semantic fields after use

  Malicious upload                    Size/type/parser limits, row/cell
                                      limits, bounded error output

  Spreadsheet formula injection       Escape formula-triggering values in
                                      CSV/XLSX

  SQL injection                       Parameterized queries; allow-listed
                                      sort/filter

  Resource exhaustion                 Rate limits, request/file limits,
                                      bounded pagination/search, async
                                      jobs

  Data leakage                        No raw files/secrets in logs;
                                      scoped job status and downloads; no
                                      cross-tenant existence leakage

  Stale cache                         No cache initially; if added,
                                      version/invalidation design and
                                      authoritative revalidation

  Unknown safety state                Fail closed in safety-critical
                                      sale/dispensing path

  Unauthorized export                 Specific permission, scoped worker
                                      queries, audit, short-lived signed
                                      URL
  -----------------------------------------------------------------------

This contract does not replace applicable pharmacy law or a
prescription/dispensing workflow. Catalog flags alone do not authorize a
sale.

## 12. Redis, events, observability

**Redis:** no Medicine cache by default. Measure PostgreSQL first. If
later justified, cache read projections only, version keys by
organization/catalog revision, define invalidation, fall back to
PostgreSQL, and always revalidate safety-critical status during the
authoritative sale/dispensing operation.

**Events:** possible events if a shared outbox exists:
`MedicineCreated`, `MedicineCatalogChanged`, `MedicineDeactivated`,
`MedicineRegulatoryProfileChanged`, `MedicineVariantChanged`,
`MedicineBarcodeChanged`. Include identifiers/revision/correlation
metadata; avoid full payloads. Consumers must be idempotent. Never
publish before commit or rely on an in-memory goroutine for durability.

**Observability:** use existing logging, metrics, tracing, request IDs
and audit. Measure list/detail/lookup/search latency, ambiguous barcode
count, duplicate conflicts, regulatory changes, import/export
duration/failure/row counts, DB query duration and pool saturation. Do
not use SKU, barcode, medicine name, organization ID or user ID as
unbounded metric labels. Never log tokens, credentials, full files, raw
imported rows or private storage URLs.

Business audit should capture creation/archive/reactivation, composition
changes, SKU/barcode assignment, conversion protection, regulatory
changes (actor/reason/effective date/revision), import/export
initiation/results/download authorization. Allow-list and redact
before/after values. Request-level middleware audit alone does not prove
a business transaction committed.

## 13. Errors and OpenAPI

Use the project's shared error envelope and stable codes. Expected
cases: validation error, invalid reference, duplicate SKU/barcode
conflict, ambiguous barcode integrity error, scoped not found, protected
conversion conflict, stale-version conflict, archive blocked by
references, upload size/type error, async job accepted, safe job
failure, rate limit, auth/forbidden.

-   OpenAPI 3.1 contract-first; update the existing split YAML layout.
-   Use current Redocly, `oapi-codegen` strict Gin, `kin-openapi`,
    Scalar and lint/drift/breaking checks where actually configured.
-   Define all request/response/error/job schemas, PATCH null/omission
    semantics, PUT replacement semantics, permissions, rate policies,
    pagination and idempotency.
-   No Swagger annotations or hand-editing generated files if project
    toolchain generates them.
-   For async import/export, return a job resource/`202 Accepted` and
    define status/result endpoints according to existing versioning
    conventions; do not pretend a large job completed synchronously.
-   Keep supplied routes unless a reviewed API change is approved.

## 14. Testing strategy

**Unit:** DTO validation, optional/null PATCH semantics, composition and
unit compatibility, positive conversion, regulatory policy/reason,
formula-safe export.

**Repository/integration:** unique SKU/barcode under concurrent
requests; aggregate rollback; FK validation; soft-delete/history
behavior; optimistic conflicts; deterministic pagination; exact lookup;
tenant isolation; historical snapshots.

**Handler/middleware:** authentication and permission on every route;
non-SUPER_ADMIN write denied; authorized branches can read without
duplicate catalog records; correct configured rate key; idempotency;
cross-tenant job/download denied.

**POS/safety:** exact lookup returns the right variant;
missing/ambiguous barcode fails closed; inactive variant excluded;
lookup does not imply stock or price; sale revalidates authoritative
stock/price/regulatory state; pack conversion is exact.

**Import/export:** malformed/oversized/replayed/adversarial files;
formula injection; retries; worker interruption; expiry of signed links;
cross-tenant denial; bounded runtime/memory.

Run actual repository gates (`gofmt`, `go test ./...`, `go vet ./...`,
migration checks, OpenAPI lint/generate/drift and integration tests
where configured). Never claim a gate passed unless it ran.

## 15. Failure handling

  -----------------------------------------------------------------------
  Failure                             Required behavior
  ----------------------------------- -----------------------------------
  PostgreSQL unavailable              Shared service-unavailable error;
                                      no unsafe stale authoritative
                                      status

  Redis unavailable, if added         Fallback to PostgreSQL for eligible
                                      reads

  Concurrent duplicate                DB constraint decides; stable
                                      conflict

  Import worker crash                 Durable job remains reconcilable
                                      and safely retryable

  Invalid import rows                 Preserve diagnostics; apply only
                                      explicitly approved import mode

  Outbox delivery failure             Retry from durable outbox if
                                      implemented

  Reference deactivated after         Authoritative business operation
  selection                           revalidates before commit

  Unknown/conflicting safety state    Block safety-critical operation and
                                      record diagnostic

  Export storage failure              Mark job failed/retryable; never
                                      return a broken URL

  Change conflicts with history       Reject semantic mutation; create
                                      new identity/version

  Cross-tenant ID/job                 Scoped not-found/forbidden per
                                      shared policy, no existence leak
  -----------------------------------------------------------------------

## 16. Implementation sequence

  -----------------------------------------------------------------------
  Phase                   Work                    Exit criteria
  ----------------------- ----------------------- -----------------------
  0\. Discovery           Inspect migrations,     No guessed
                          BaseModel, DB           schema/infrastructure
                          transaction patterns,   
                          MasterData/Brand        
                          models, RBAC,           
                          middleware, limiter     
                          registry, audit,        
                          job/storage and OpenAPI 

  1\. Schema contract     Finalize medicine,      Scope, constraints,
                          composition, variant,   indexes and delete
                          barcode, revision and   policy reviewed
                          approved support tables 

  2\. Model/DTO           `model.go`, `dto.go`,   Exact SQL mapping; mass
                          `mapper.go`             assignment prevented

  3\. Repository          Parameterized reads,    Scope and concurrency
                          exact lookup, aggregate tests
                          transactions            

  4\. Service             Rules, references,      Unit/transaction tests
                          lifecycle, revisions    

  5\. Handler/routes      Shared middleware,      RBAC/rate keys verified
                          response/error          
                          conventions             

  6\. OpenAPI             Schemas/routes/job      Lint/generate/drift
                          resources               pass

  7\. Variants/barcodes   Conversion and exact    Uniqueness and
                          POS lookup              conversion tests

  8\. Import/export       Staging, jobs, storage  Retry/security/scope
                          and access control      tests

  9\. Hardening           Audit, metrics, query   No critical
                          plans and security      data-integrity issue
                          review                  
  -----------------------------------------------------------------------

### Definition of done

-   [ ] One catalog identity is shared across branches within an
    organization.
-   [ ] Tenant/global ownership is explicit and tested.
-   [ ] Medicine, composition, variant, barcode and revision data are
    transactionally consistent.
-   [ ] SKU/barcode exact lookup is indexed and unambiguous.
-   [ ] Formulation and conversion cannot be silently rewritten after
    use.
-   [ ] Regulatory-sensitive edits are privileged, justified, audited
    and versioned.
-   [ ] No Medicine-owned stock, acquisition cost, price policy or
    reorder thresholds.
-   [ ] Routes use shared auth/RBAC/rate/response/error/observability.
-   [ ] Import/export is durable, scoped, retry-safe and protected.
-   [ ] OpenAPI and tests pass in the actual repository.

## 17. Decisions for the supplied model sketch

  -----------------------------------------------------------------------
  Draft field/concept                 Decision
  ----------------------------------- -----------------------------------
  `SKU` on `Medicine`                 Prefer SKU on `MedicineVariant`;
                                      medicine-level catalog code only if
                                      separately needed

  Single `Barcode`                    Replace as authoritative identity
                                      with variant-linked
                                      `MedicineBarcode` rows

  `GenericMedicineID`                 Insufficient alone for
                                      combinations; composition rows
                                      reference ingredient/generic
                                      identity

  Brand/category/dosage               Keep only if they match approved
  form/route/storage condition IDs    schema and identity semantics;
                                      choose single vs multi-category
                                      explicitly

  `ManufacturerID`                    Avoid contradictory duplicate when
                                      Brand already references
                                      Manufacturer; keep only if separate
                                      relationship is meaningful

  `Strength string`                   Structured numeric amount and unit;
                                      display string is derived

  `PackSize string`, `UnitsPerPack`,  Explicit variant/package and exact
  `UnitsPerStrip`                     conversion model

  `RequiresPrescription`,             Effective-dated regulatory profile
  `IsControlled`                      where needed; flat flags only as
                                      documented single-jurisdiction
                                      simplification

  `IsRefrigerated`                    Prefer `storage_conditions` if it
                                      expresses the rule; avoid
                                      conflicting sources of truth

  Import country/name/license         Keep only if in scope; decide
                                      verified importer entity vs
                                      supplier reference and evidence
                                      ownership

  `MRP`, `DefaultCost`                Remove as authoritative Medicine
                                      fields; pricing and
                                      purchase/inventory valuation own
                                      these facts

  `TaxRateID`                         Reference only if required; tax
                                      rule belongs to approved tax/sales
                                      contract

  `HSNCode`                           Include only if required by
                                      reporting/tax rules; define
                                      jurisdiction/format

  `ShelfLifeMonths`                   Not a substitute for batch expiry;
                                      retain only as a documented default
                                      consumed by a workflow

  `MinStockThreshold`,                Move to inventory/branch/warehouse
  `MaxStockThreshold`                 replenishment policy

  `ImageURL`                          Optional media reference only if
                                      storage/security rules exist;
                                      validate unsafe URLs

  `MedicineVariant.MRP`               Do not duplicate price ownership
                                      unless pricing contract explicitly
                                      allows a catalog list price

  `MedicinePriceHistory`              Do not implement in Medicine;
                                      delegate to Pricing

  `BaseModel`                         Embed only if fields match actual
                                      SQL; `AuditColumns` requires
                                      matching audit columns/migration
  -----------------------------------------------------------------------

## 18. Source alignment and unresolved facts

**Adopted from Astra:** organization-central catalog, multi-ingredient
composition, package variants and variant-specific barcodes,
inventory/purchase/sale ownership boundaries, exact POS lookup, staged
import, immutable revisions, no default Redis, and
security/observability gates.

**Adopted from MasterData:** existing dictionaries remain authoritative;
no duplicate Brand/Manufacturer/GenericMedicine tables; use existing
native RBAC, middleware, response/error, pagination, DB and OpenAPI
conventions; verify limiter keys against actual configuration.

**Not proven by the supplied migration:**
Medicine/composition/variant/barcode/regulatory/revision/import/export
tables exist. Add forward migrations only after inspection.

**Must be verified in repository:** exact method signatures, limiter
keys, outbox/worker/storage/business-audit availability, response/error
DTO names, unit semantics, and actual deployed migration state.

### References

-   `answer_in_markdown.md` --- Astra module plan, especially
    `MODULE: medicine` and cross-module architecture.
-   `MediCore-ERP-Backend-Plan.pdf` --- central catalog, branch-specific
    inventory, FEFO, purchase and POS workflows.
-   `MASTERDATA_IMPLEMENTATION_CONTRACT.md` --- MasterData ownership and
    project integration rules.
-   `migrations/000005_master_data.up.sql` --- supplied reference
    schema.
-   Current repository source, migrations, shared middleware, native
    RBAC, `common.BaseModel`, `pkg/pagination`, and split OpenAPI
    contract --- implementation authority.
