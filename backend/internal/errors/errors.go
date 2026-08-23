// =============================================================================
//  Pharmaciano ERP — internal/errors + internal/common
//  errors/    — canonical AppError type, code taxonomy, HTTP mapper.
//  Design notes:
//    * AppError is the only error type that leaves the service layer. Handlers
//      map it to HTTP responses; nothing else has to know about status codes.
//    * Enums are string-typed so they encode cleanly to JSON and Postgres.
//    * constants.go carries "role name" style values that must never drift;
//      enums.go carries string types with validation helpers.
// =============================================================================

package errors
