package pagination

// Pagination represents pagination parameters
type Pagination struct {
	Page     int   `json:"page" form:"page" binding:"omitempty,min=1"`
	PageSize int   `json:"page_size" form:"page_size" binding:"omitempty,min=1,max=100"`
	Total    int64 `json:"total"`
	Pages    int   `json:"pages"`
}

// NewPagination creates a new pagination instance
func NewPagination(page, pageSize int) *Pagination {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return &Pagination{
		Page:     page,
		PageSize: pageSize,
	}
}

// GetOffset returns the offset for database queries
func (p *Pagination) GetOffset() int {
	return (p.Page - 1) * p.PageSize
}

// GetLimit returns the limit for database queries
func (p *Pagination) GetLimit() int {
	return p.PageSize
}

// SetTotal sets the total records and calculates pages
func (p *Pagination) SetTotal(total int64) {
	p.Total = total
	if p.PageSize > 0 {
		p.Pages = int((total + int64(p.PageSize) - 1) / int64(p.PageSize))
	}
}

// HasNextPage returns true if there is a next page
func (p *Pagination) HasNextPage() bool {
	return p.Page < p.Pages
}

// HasPrevPage returns true if there is a previous page
func (p *Pagination) HasPrevPage() bool {
	return p.Page > 1
}
