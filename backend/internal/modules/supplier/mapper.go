package supplier

// Mapper converts between the Supplier persistence model and the HTTP DTOs.
// It is pure: no DB access, no business policy, no validation beyond what the
// DTO struct tags already enforce. The service layer is responsible for
// tenant scope, credit-limit null semantics and redaction policy; the mapper
// only moves data between two shapes.
type Mapper struct{}

// NewMapper returns a Supplier mapper.
func NewMapper() *Mapper { return &Mapper{} }

// ToResponse projects a Supplier onto the redacted single-resource response.
//
// TIN is passed through here unchanged; the service layer may choose to
// redact it further for specific callers, but the mapper has no opinion on
// which principal may see a sensitive field — that is authorization, not
// mapping.
func (m *Mapper) ToResponse(s *Supplier) *SupplierResponse {
	if s == nil {
		return nil
	}
	return &SupplierResponse{
		ID:                s.ID,
		Code:              s.Code,
		Name:              s.Name,
		ContactPerson:    s.ContactPerson,
		Phone:            s.Phone,
		Email:            s.Email,
		Address:          s.Address,
		City:             s.City,
		Country:          s.Country,
		DrugLicenseNo:    s.DrugLicenseNo,
		TIN:              s.TIN,
		CreditLimit:      s.CreditLimit,
		CreditPeriodDays: s.CreditPeriodDays,
		IsActive:         s.IsActive,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
	}
}

// ToListItem projects a Supplier onto the compact list row. Deliberately
// narrow: contact/address/phone/email/TIN are omitted so a list call never
// pulls the wide supplier row or joins purchase/ledger data.
func (m *Mapper) ToListItem(s *Supplier) ListItem {
	return ListItem{
		ID:               s.ID,
		Code:             s.Code,
		Name:             s.Name,
		City:             s.City,
		Country:          s.Country,
		CreditLimit:      s.CreditLimit,
		CreditPeriodDays: s.CreditPeriodDays,
		IsActive:         s.IsActive,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
	}
}

// ToModel materializes a Supplier from a Create request. organization_id is
// supplied by the service from the trusted authenticated context; the mapper
// does not read it from the payload.
func (m *Mapper) ToModel(req *CreateSupplierRequest) *Supplier {
	return &Supplier{
		Code:             req.Code,
		Name:             req.Name,
		ContactPerson:    req.ContactPerson,
		Phone:            req.Phone,
		Email:            req.Email,
		Address:          req.Address,
		City:             req.City,
		Country:          req.Country,
		DrugLicenseNo:    req.DrugLicenseNo,
		TIN:              req.TIN,
		CreditLimit:      req.CreditLimit,
		CreditPeriodDays: req.CreditPeriodDays,
		IsActive:         req.IsActive != nil && *req.IsActive,
	}
}

// ApplyPatch mutates s in place from the PATCH request. Nil pointers mean
// "leave unchanged"; a non-nil pointer to a zero value means "set to zero".
// Code is immutable and therefore absent from UpdateSupplierRequest, so it is
// never touched here.
func (m *Mapper) ApplyPatch(s *Supplier, req *UpdateSupplierRequest) {
	if req.Name != nil {
		s.Name = *req.Name
	}
	if req.ContactPerson != nil {
		s.ContactPerson = req.ContactPerson
	}
	if req.Phone != nil {
		s.Phone = req.Phone
	}
	if req.Email != nil {
		s.Email = req.Email
	}
	if req.Address != nil {
		s.Address = req.Address
	}
	if req.City != nil {
		s.City = req.City
	}
	if req.Country != nil {
		s.Country = req.Country
	}
	if req.DrugLicenseNo != nil {
		s.DrugLicenseNo = req.DrugLicenseNo
	}
	if req.TIN != nil {
		s.TIN = req.TIN
	}
	if req.CreditLimit != nil {
		s.CreditLimit = req.CreditLimit
	}
	if req.CreditPeriodDays != nil {
		s.CreditPeriodDays = req.CreditPeriodDays
	}
	if req.IsActive != nil {
		s.IsActive = *req.IsActive
	}
}