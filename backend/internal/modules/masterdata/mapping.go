// Explicit model → response mapping for the eleven MasterData resources.
//
// The boundary exists so the public API contract is decided here rather than
// inherited from the row struct. Today every model field happens to be
// publishable, but a response DTO built by embedding common.BaseModel would
// silently start emitting a new column the moment one is added, and would carry
// `db:` tags into the transport layer. A converter cannot do either by accident.
//
// Every function here is pure and deterministic: no database access, no clock
// reads, no error paths. A nil pointer in means a nil pointer out.

package masterdata

// NewProductCategoryResponse converts a ProductCategory row to its API form.
func NewProductCategoryResponse(m ProductCategory) ProductCategoryResponse {
	return ProductCategoryResponse{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		ParentID:    m.ParentID,
		Description: m.Description,
		SortOrder:   m.SortOrder,
		IsActive:    m.IsActive,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// NewRouteResponse converts a Route row to its API form.
func NewRouteResponse(m Route) RouteResponse {
	return RouteResponse{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		Description: m.Description,
		IsActive:    m.IsActive,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// NewDosageFormResponse converts a DosageForm row to its API form.
func NewDosageFormResponse(m DosageForm) DosageFormResponse {
	return DosageFormResponse{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		RouteID:     m.RouteID,
		Description: m.Description,
		IsActive:    m.IsActive,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// NewPackageTypeResponse converts a PackageType row to its API form.
func NewPackageTypeResponse(m PackageType) PackageTypeResponse {
	return PackageTypeResponse{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		Description: m.Description,
		IsActive:    m.IsActive,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// NewUnitTypeResponse converts a UnitType row to its API form.
func NewUnitTypeResponse(m UnitType) UnitTypeResponse {
	return UnitTypeResponse{
		ID:               m.ID,
		Code:             m.Code,
		Name:             m.Name,
		Symbol:           m.Symbol,
		BaseUnitID:       m.BaseUnitID,
		ConversionFactor: m.ConversionFactor,
		IsActive:         m.IsActive,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

// NewStorageConditionResponse converts a StorageCondition row to its API form.
func NewStorageConditionResponse(m StorageCondition) StorageConditionResponse {
	return StorageConditionResponse{
		ID:             m.ID,
		Code:           m.Code,
		Name:           m.Name,
		MinTempCelsius: m.MinTempCelsius,
		MaxTempCelsius: m.MaxTempCelsius,
		Description:    m.Description,
		IsActive:       m.IsActive,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

// NewTaxRateResponse converts a TaxRate row to its API form.
func NewTaxRateResponse(m TaxRate) TaxRateResponse {
	return TaxRateResponse{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		RatePercent: m.RatePercent,
		IsDefault:   m.IsDefault,
		IsActive:    m.IsActive,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// NewDrugGroupResponse converts a DrugGroup row to its API form.
func NewDrugGroupResponse(m DrugGroup) DrugGroupResponse {
	return DrugGroupResponse{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		Description: m.Description,
		IsActive:    m.IsActive,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// NewDrugClassResponse converts a DrugClass row to its API form.
func NewDrugClassResponse(m DrugClass) DrugClassResponse {
	return DrugClassResponse{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		DrugGroupID: m.DrugGroupID,
		Description: m.Description,
		IsActive:    m.IsActive,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// NewTherapeuticClassResponse converts a TherapeuticClass row to its API form.
func NewTherapeuticClassResponse(m TherapeuticClass) TherapeuticClassResponse {
	return TherapeuticClassResponse{
		ID:          m.ID,
		Code:        m.Code,
		Name:        m.Name,
		DrugClassID: m.DrugClassID,
		Description: m.Description,
		IsActive:    m.IsActive,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// NewGenericMedicineResponse converts a GenericMedicine row to its API form.
func NewGenericMedicineResponse(m GenericMedicine) GenericMedicineResponse {
	return GenericMedicineResponse{
		ID:                 m.ID,
		Code:               m.Code,
		Name:               m.Name,
		Description:        m.Description,
		TherapeuticClassID: m.TherapeuticClassID,
		IsControlled:       m.IsControlled,
		IsActive:           m.IsActive,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
}

// ProductCategoryResponses maps a page of rows. Phase 04 uses it for the list
// endpoint; the per-resource converters above stay the single definition of the
// field mapping.
func ProductCategoryResponses(in []ProductCategory) []ProductCategoryResponse {
	if in == nil {
		return nil
	}
	out := make([]ProductCategoryResponse, len(in))
	for i, m := range in {
		out[i] = NewProductCategoryResponse(m)
	}
	return out
}

// RouteResponses maps a page of rows.
func RouteResponses(in []Route) []RouteResponse {
	if in == nil {
		return nil
	}
	out := make([]RouteResponse, len(in))
	for i, m := range in {
		out[i] = NewRouteResponse(m)
	}
	return out
}

// DosageFormResponses maps a page of rows.
func DosageFormResponses(in []DosageForm) []DosageFormResponse {
	if in == nil {
		return nil
	}
	out := make([]DosageFormResponse, len(in))
	for i, m := range in {
		out[i] = NewDosageFormResponse(m)
	}
	return out
}

// PackageTypeResponses maps a page of rows.
func PackageTypeResponses(in []PackageType) []PackageTypeResponse {
	if in == nil {
		return nil
	}
	out := make([]PackageTypeResponse, len(in))
	for i, m := range in {
		out[i] = NewPackageTypeResponse(m)
	}
	return out
}

// UnitTypeResponses maps a page of rows.
func UnitTypeResponses(in []UnitType) []UnitTypeResponse {
	if in == nil {
		return nil
	}
	out := make([]UnitTypeResponse, len(in))
	for i, m := range in {
		out[i] = NewUnitTypeResponse(m)
	}
	return out
}

// StorageConditionResponses maps a page of rows.
func StorageConditionResponses(in []StorageCondition) []StorageConditionResponse {
	if in == nil {
		return nil
	}
	out := make([]StorageConditionResponse, len(in))
	for i, m := range in {
		out[i] = NewStorageConditionResponse(m)
	}
	return out
}

// TaxRateResponses maps a page of rows.
func TaxRateResponses(in []TaxRate) []TaxRateResponse {
	if in == nil {
		return nil
	}
	out := make([]TaxRateResponse, len(in))
	for i, m := range in {
		out[i] = NewTaxRateResponse(m)
	}
	return out
}

// DrugGroupResponses maps a page of rows.
func DrugGroupResponses(in []DrugGroup) []DrugGroupResponse {
	if in == nil {
		return nil
	}
	out := make([]DrugGroupResponse, len(in))
	for i, m := range in {
		out[i] = NewDrugGroupResponse(m)
	}
	return out
}

// DrugClassResponses maps a page of rows.
func DrugClassResponses(in []DrugClass) []DrugClassResponse {
	if in == nil {
		return nil
	}
	out := make([]DrugClassResponse, len(in))
	for i, m := range in {
		out[i] = NewDrugClassResponse(m)
	}
	return out
}

// TherapeuticClassResponses maps a page of rows.
func TherapeuticClassResponses(in []TherapeuticClass) []TherapeuticClassResponse {
	if in == nil {
		return nil
	}
	out := make([]TherapeuticClassResponse, len(in))
	for i, m := range in {
		out[i] = NewTherapeuticClassResponse(m)
	}
	return out
}

// GenericMedicineResponses maps a page of rows.
func GenericMedicineResponses(in []GenericMedicine) []GenericMedicineResponse {
	if in == nil {
		return nil
	}
	out := make([]GenericMedicineResponse, len(in))
	for i, m := range in {
		out[i] = NewGenericMedicineResponse(m)
	}
	return out
}
