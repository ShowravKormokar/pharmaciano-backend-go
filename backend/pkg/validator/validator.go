package validator

import (
	"regexp"

	"github.com/go-playground/validator/v10"
)

// CustomValidator wraps the standard validator with custom validation rules
type CustomValidator struct {
	validator *validator.Validate
}

// NewValidator creates a new custom validator
func NewValidator() *CustomValidator {
	v := validator.New()

	// Register custom validation functions
	_ = v.RegisterValidation("phone", validatePhone)
	_ = v.RegisterValidation("zipcode", validateZipcode)

	return &CustomValidator{
		validator: v,
	}
}

// Validate validates a struct
func (cv *CustomValidator) Validate(i interface{}) error {
	return cv.validator.Struct(i)
}

// ValidatePhone validates a phone number format
func validatePhone(fl validator.FieldLevel) bool {
	phone := fl.Field().String()
	phoneRegex := regexp.MustCompile(`^\+?[1-9]\d{1,14}$`)
	return phoneRegex.MatchString(phone)
}

// ValidateZipcode validates a zipcode format
func validateZipcode(fl validator.FieldLevel) bool {
	zipcode := fl.Field().String()
	zipcodeRegex := regexp.MustCompile(`^\d{5}(-\d{4})?$`)
	return zipcodeRegex.MatchString(zipcode)
}
