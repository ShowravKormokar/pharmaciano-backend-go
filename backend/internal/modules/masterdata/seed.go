package masterdata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"backend/internal/platform/db"
)

// SeedAdvisoryLock is the transaction-scoped advisory lock key the masterdata
// seeder takes so two app instances booting at once serialise instead of racing
// on the same catalogue rows. Any stable constant works; it must not collide
// with other advisory-lock users (rbac uses its own).
const SeedAdvisoryLock int64 = 0x4d_4441_5441 // "MDATA"

// Seeder plants the fixed reference catalogues every tenant needs before it can
// run day-to-day operations: routes of administration, dosage forms, package
// and unit types, storage conditions, product categories and tax rates.
//
// It is idempotent by `code` — every table carries a partial UNIQUE index on
// `code` WHERE deleted_at IS NULL, which the upserts target directly — so it is
// safe to run on every boot. Values are refreshed from the YAML so the checked-in
// files stay the single source of truth for the seeded catalogue.
//
// Self-referencing rows (a category's parent, a unit's base unit) are inserted
// in two passes: parents first with NULL, then a second UPDATE resolves the
// references by code. That keeps the YAML free to declare a child before its
// parent.
type Seeder struct {
	db      *db.DB
	log     *zap.Logger
	seedDir string
}

// NewSeeder builds a seeder over the shared database handle. An empty seedDir
// makes it resolve the conventional "seed" directory relative to the working
// directory; cmd/seed passes an explicit path.
func NewSeeder(database *db.DB, log *zap.Logger, seedDir string) *Seeder {
	if log == nil {
		log = zap.NewNop()
	}
	return &Seeder{db: database, log: log, seedDir: seedDir}
}

// Seed applies the whole masterdata catalogue in a single transaction guarded by
// an advisory lock. Either every catalogue lands or none of them do.
func (s *Seeder) Seed(ctx context.Context) error {
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.db.FromCtx(ctx).Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, SeedAdvisoryLock); err != nil {
			return fmt.Errorf("acquire masterdata seed lock: %w", err)
		}

		q := s.db.FromCtx(ctx)

		// Order matters: a catalogue may reference an earlier one by code.
		routeIDs, err := s.seedRoutes(ctx, q)
		if err != nil {
			return err
		}
		dosageForms, err := s.seedDosageForms(ctx, q, routeIDs)
		if err != nil {
			return err
		}
		units, err := s.seedUnitTypes(ctx, q)
		if err != nil {
			return err
		}
		packages, err := s.seedPackageTypes(ctx, q)
		if err != nil {
			return err
		}
		storage, err := s.seedStorageConditions(ctx, q)
		if err != nil {
			return err
		}
		categories, err := s.seedProductCategories(ctx, q)
		if err != nil {
			return err
		}
		taxes, err := s.seedTaxRates(ctx, q)
		if err != nil {
			return err
		}

		s.log.Info("masterdata catalogue seeded",
			zap.Int("routes", len(routeIDs)),
			zap.Int("dosage_forms", dosageForms),
			zap.Int("unit_types", units),
			zap.Int("package_types", packages),
			zap.Int("storage_conditions", storage),
			zap.Int("product_categories", categories),
			zap.Int("tax_rates", taxes),
		)
		return nil
	})
}

// --- YAML shapes -------------------------------------------------------------
//
// Only the columns the schema actually stores are declared. The YAML files carry
// a few extra advisory fields (humidity_max_percent, protect_from_light,
// is_base, ...); yaml.v3 ignores unknown keys, so those stay documentation-only
// until the schema grows columns for them.

type routesFile struct {
	Routes []struct {
		Code        string `yaml:"code"`
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		IsActive    *bool  `yaml:"is_active"`
	} `yaml:"routes"`
}

type dosageFormsFile struct {
	DosageForms []struct {
		Code        string `yaml:"code"`
		Name        string `yaml:"name"`
		Route       string `yaml:"route"`
		Description string `yaml:"description"`
		IsActive    *bool  `yaml:"is_active"`
	} `yaml:"dosage_forms"`
}

type packageTypesFile struct {
	PackageTypes []struct {
		Code        string `yaml:"code"`
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		IsActive    *bool  `yaml:"is_active"`
	} `yaml:"package_types"`
}

type unitTypesFile struct {
	UnitTypes []struct {
		Code             string   `yaml:"code"`
		Name             string   `yaml:"name"`
		Symbol           string   `yaml:"symbol"`
		BaseUnit         string   `yaml:"base_unit"`
		ConversionFactor *float64 `yaml:"conversion_factor"`
		IsActive         *bool    `yaml:"is_active"`
	} `yaml:"unit_types"`
}

type storageConditionsFile struct {
	StorageConditions []struct {
		Code           string   `yaml:"code"`
		Name           string   `yaml:"name"`
		MinTempCelsius *float64 `yaml:"min_temp_celsius"`
		MaxTempCelsius *float64 `yaml:"max_temp_celsius"`
		Description    string   `yaml:"description"`
		IsActive       *bool    `yaml:"is_active"`
	} `yaml:"storage_conditions"`
}

type categoriesFile struct {
	Categories []struct {
		Code        string `yaml:"code"`
		Name        string `yaml:"name"`
		Parent      string `yaml:"parent"`
		Description string `yaml:"description"`
		SortOrder   int    `yaml:"sort_order"`
		IsActive    *bool  `yaml:"is_active"`
	} `yaml:"categories"`
}

type taxRatesFile struct {
	TaxRates []struct {
		Code        string   `yaml:"code"`
		Name        string   `yaml:"name"`
		RatePercent *float64 `yaml:"rate_percent"`
		IsDefault   *bool    `yaml:"is_default"`
		IsActive    *bool    `yaml:"is_active"`
	} `yaml:"tax_rates"`
}

// --- Seed steps --------------------------------------------------------------

func (s *Seeder) seedRoutes(ctx context.Context, q db.Querier) (map[string]uuid.UUID, error) {
	var f routesFile
	if err := s.load("routes.yaml", &f); err != nil {
		return nil, err
	}
	ids := make(map[string]uuid.UUID, len(f.Routes))
	for _, r := range f.Routes {
		code := strings.TrimSpace(r.Code)
		if code == "" {
			continue
		}
		var id uuid.UUID
		if err := q.QueryRow(ctx, `
			INSERT INTO routes (code, name, description, is_active)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (code) WHERE deleted_at IS NULL DO UPDATE
			SET name = EXCLUDED.name,
			    description = EXCLUDED.description,
			    is_active = EXCLUDED.is_active,
			    updated_at = now()
			RETURNING id`,
			code, strings.TrimSpace(r.Name), nullString(r.Description), activeOrTrue(r.IsActive),
		).Scan(&id); err != nil {
			return nil, fmt.Errorf("seed route %s: %w", code, err)
		}
		ids[code] = id
	}
	return ids, nil
}

// seedDosageForms links each form to its route by code. A route that is not in
// routes.yaml (or was left blank) stores NULL rather than failing the seed.
func (s *Seeder) seedDosageForms(ctx context.Context, q db.Querier, routeIDs map[string]uuid.UUID) (int, error) {
	var f dosageFormsFile
	if err := s.load("dosage_forms.yaml", &f); err != nil {
		return 0, err
	}
	for _, d := range f.DosageForms {
		if strings.TrimSpace(d.Code) == "" {
			continue
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO dosage_forms (code, name, route_id, description, is_active)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (code) WHERE deleted_at IS NULL DO UPDATE
			SET name = EXCLUDED.name,
			    route_id = EXCLUDED.route_id,
			    description = EXCLUDED.description,
			    is_active = EXCLUDED.is_active,
			    updated_at = now()`,
			strings.TrimSpace(d.Code), strings.TrimSpace(d.Name), routeID(routeIDs, d.Route),
			nullString(d.Description), activeOrTrue(d.IsActive),
		); err != nil {
			return 0, fmt.Errorf("seed dosage form %s: %w", d.Code, err)
		}
	}
	return len(f.DosageForms), nil
}

func (s *Seeder) seedPackageTypes(ctx context.Context, q db.Querier) (int, error) {
	var f packageTypesFile
	if err := s.load("package_types.yaml", &f); err != nil {
		return 0, err
	}
	for _, p := range f.PackageTypes {
		if strings.TrimSpace(p.Code) == "" {
			continue
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO package_types (code, name, description, is_active)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (code) WHERE deleted_at IS NULL DO UPDATE
			SET name = EXCLUDED.name,
			    description = EXCLUDED.description,
			    is_active = EXCLUDED.is_active,
			    updated_at = now()`,
			strings.TrimSpace(p.Code), strings.TrimSpace(p.Name), nullString(p.Description), activeOrTrue(p.IsActive),
		); err != nil {
			return 0, fmt.Errorf("seed package type %s: %w", p.Code, err)
		}
	}
	return len(f.PackageTypes), nil
}

// seedUnitTypes runs two passes so `base_unit` may point at a unit declared
// later in the file. Pass one inserts every row with a NULL base_unit_id, pass
// two points the derived units at their base.
func (s *Seeder) seedUnitTypes(ctx context.Context, q db.Querier) (int, error) {
	var f unitTypesFile
	if err := s.load("unit_types.yaml", &f); err != nil {
		return 0, err
	}

	ids := make(map[string]uuid.UUID, len(f.UnitTypes))
	for _, u := range f.UnitTypes {
		code := strings.TrimSpace(u.Code)
		if code == "" {
			continue
		}
		var id uuid.UUID
		if err := q.QueryRow(ctx, `
			INSERT INTO unit_types (code, name, symbol, base_unit_id, conversion_factor, is_active)
			VALUES ($1, $2, $3, NULL, $4, $5)
			ON CONFLICT (code) WHERE deleted_at IS NULL DO UPDATE
			SET name = EXCLUDED.name,
			    symbol = EXCLUDED.symbol,
			    conversion_factor = EXCLUDED.conversion_factor,
			    is_active = EXCLUDED.is_active,
			    updated_at = now()
			RETURNING id`,
			code, strings.TrimSpace(u.Name), nullString(u.Symbol), u.ConversionFactor, activeOrTrue(u.IsActive),
		).Scan(&id); err != nil {
			return 0, fmt.Errorf("seed unit type %s: %w", code, err)
		}
		ids[code] = id
	}

	for _, u := range f.UnitTypes {
		code := strings.TrimSpace(u.Code)
		base := strings.TrimSpace(u.BaseUnit)
		if code == "" || base == "" {
			continue
		}
		baseID, ok := ids[base]
		if !ok {
			s.log.Warn("masterdata seed: unit base not found, leaving NULL",
				zap.String("unit", code), zap.String("base_unit", base))
			continue
		}
		if _, err := q.Exec(ctx,
			`UPDATE unit_types SET base_unit_id = $2, updated_at = now() WHERE id = $1`,
			ids[code], baseID,
		); err != nil {
			return 0, fmt.Errorf("link unit %s to base %s: %w", code, base, err)
		}
	}

	return len(f.UnitTypes), nil
}

func (s *Seeder) seedStorageConditions(ctx context.Context, q db.Querier) (int, error) {
	var f storageConditionsFile
	if err := s.load("storage_conditions.yaml", &f); err != nil {
		return 0, err
	}
	for _, c := range f.StorageConditions {
		if strings.TrimSpace(c.Code) == "" {
			continue
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO storage_conditions (code, name, min_temp_celsius, max_temp_celsius, description, is_active)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (code) WHERE deleted_at IS NULL DO UPDATE
			SET name = EXCLUDED.name,
			    min_temp_celsius = EXCLUDED.min_temp_celsius,
			    max_temp_celsius = EXCLUDED.max_temp_celsius,
			    description = EXCLUDED.description,
			    is_active = EXCLUDED.is_active,
			    updated_at = now()`,
			strings.TrimSpace(c.Code), strings.TrimSpace(c.Name), c.MinTempCelsius, c.MaxTempCelsius,
			nullString(c.Description), activeOrTrue(c.IsActive),
		); err != nil {
			return 0, fmt.Errorf("seed storage condition %s: %w", c.Code, err)
		}
	}
	return len(f.StorageConditions), nil
}

// seedProductCategories runs two passes so a sub-category may be declared before
// its parent. Pass one inserts every row with a NULL parent_id, pass two links
// the children to their parents.
func (s *Seeder) seedProductCategories(ctx context.Context, q db.Querier) (int, error) {
	var f categoriesFile
	if err := s.load("categories.yaml", &f); err != nil {
		return 0, err
	}

	ids := make(map[string]uuid.UUID, len(f.Categories))
	for _, c := range f.Categories {
		code := strings.TrimSpace(c.Code)
		if code == "" {
			continue
		}
		var id uuid.UUID
		if err := q.QueryRow(ctx, `
			INSERT INTO product_categories (code, name, parent_id, description, sort_order, is_active)
			VALUES ($1, $2, NULL, $3, $4, $5)
			ON CONFLICT (code) WHERE deleted_at IS NULL DO UPDATE
			SET name = EXCLUDED.name,
			    description = EXCLUDED.description,
			    sort_order = EXCLUDED.sort_order,
			    is_active = EXCLUDED.is_active,
			    updated_at = now()
			RETURNING id`,
			code, strings.TrimSpace(c.Name), nullString(c.Description), c.SortOrder, activeOrTrue(c.IsActive),
		).Scan(&id); err != nil {
			return 0, fmt.Errorf("seed product category %s: %w", code, err)
		}
		ids[code] = id
	}

	for _, c := range f.Categories {
		code := strings.TrimSpace(c.Code)
		parent := strings.TrimSpace(c.Parent)
		if code == "" || parent == "" {
			continue
		}
		parentID, ok := ids[parent]
		if !ok {
			s.log.Warn("masterdata seed: category parent not found, leaving NULL",
				zap.String("category", code), zap.String("parent", parent))
			continue
		}
		if _, err := q.Exec(ctx,
			`UPDATE product_categories SET parent_id = $2, updated_at = now() WHERE id = $1`,
			ids[code], parentID,
		); err != nil {
			return 0, fmt.Errorf("link category %s to parent %s: %w", code, parent, err)
		}
	}

	return len(f.Categories), nil
}

func (s *Seeder) seedTaxRates(ctx context.Context, q db.Querier) (int, error) {
	var f taxRatesFile
	if err := s.load("tax_rates.yaml", &f); err != nil {
		return 0, err
	}
	for _, t := range f.TaxRates {
		if strings.TrimSpace(t.Code) == "" {
			continue
		}
		rate := 0.0
		if t.RatePercent != nil {
			rate = *t.RatePercent
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO tax_rates (code, name, rate_percent, is_default, is_active)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (code) WHERE deleted_at IS NULL DO UPDATE
			SET name = EXCLUDED.name,
			    rate_percent = EXCLUDED.rate_percent,
			    is_default = EXCLUDED.is_default,
			    is_active = EXCLUDED.is_active,
			    updated_at = now()`,
			strings.TrimSpace(t.Code), strings.TrimSpace(t.Name), rate, boolOr(t.IsDefault, false), activeOrTrue(t.IsActive),
		); err != nil {
			return 0, fmt.Errorf("seed tax rate %s: %w", t.Code, err)
		}
	}
	return len(f.TaxRates), nil
}

// --- Helpers -----------------------------------------------------------------

// load reads and parses one YAML file from the seed directory. A missing file is
// not an error: the corresponding catalogue is simply left untouched, which lets
// a deployment ship only the catalogues it needs.
func (s *Seeder) load(file string, out any) error {
	dir := s.seedDir
	if dir == "" {
		dir = FindSeedDir()
	}
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		if os.IsNotExist(err) {
			s.log.Debug("masterdata seed file absent, skipping", zap.String("file", file))
			return nil
		}
		return fmt.Errorf("read seed file %s: %w", file, err)
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		return fmt.Errorf("parse seed file %s: %w", file, err)
	}
	return nil
}

// FindSeedDir locates the checked-in seed directory from a working directory,
// walking up a couple of levels so it works from backend/, backend/cmd/seed and
// the repository root alike.
func FindSeedDir() string {
	candidates := []string{
		filepath.Join("seed"),
		filepath.Join("..", "seed"),
		filepath.Join("..", "..", "seed"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return filepath.Join("seed")
}

// routeID resolves a YAML `route` code to its seeded id, or nil when the code is
// blank or not present in the catalogue.
func routeID(ids map[string]uuid.UUID, code string) *uuid.UUID {
	id, ok := ids[strings.TrimSpace(code)]
	if !ok {
		return nil
	}
	return &id
}

// nullString stores "" as SQL NULL so an omitted description stays NULL rather
// than an empty string.
func nullString(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// activeOrTrue defaults is_active to true when the YAML omits it.
func activeOrTrue(v *bool) bool {
	return boolOr(v, true)
}

func boolOr(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}
