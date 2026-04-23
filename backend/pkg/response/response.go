package response

// APIResponse represents the standard API response wrapper
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorInfo  `json:"error,omitempty"`
	Meta    *MetaInfo   `json:"meta,omitempty"`
}

// ErrorInfo represents error details in response
type ErrorInfo struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// MetaInfo represents metadata in response
type MetaInfo struct {
	Page     int   `json:"page,omitempty"`
	PageSize int   `json:"page_size,omitempty"`
	Total    int64 `json:"total,omitempty"`
	Pages    int   `json:"pages,omitempty"`
}

// NewSuccessResponse creates a successful API response
func NewSuccessResponse(data interface{}) *APIResponse {
	return &APIResponse{
		Success: true,
		Data:    data,
	}
}

// NewErrorResponse creates an error API response
func NewErrorResponse(code, message string) *APIResponse {
	return &APIResponse{
		Success: false,
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
		},
	}
}

// NewPaginatedResponse creates a paginated API response
func NewPaginatedResponse(data interface{}, page, pageSize int, total int64) *APIResponse {
	pages := 0
	if pageSize > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}

	return &APIResponse{
		Success: true,
		Data:    data,
		Meta: &MetaInfo{
			Page:     page,
			PageSize: pageSize,
			Total:    total,
			Pages:    pages,
		},
	}
}

// AddError adds error details to response
func (r *APIResponse) AddError(key string, value interface{}) *APIResponse {
	if r.Error == nil {
		r.Error = &ErrorInfo{
			Details: make(map[string]interface{}),
		}
	}
	if r.Error.Details == nil {
		r.Error.Details = make(map[string]interface{})
	}
	r.Error.Details[key] = value
	return r
}
