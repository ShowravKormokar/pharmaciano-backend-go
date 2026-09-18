//go:build oapigen

package server

import "context"

// Reference implementation for the oapi-codegen strict-server adapter.
//
// Generated code exposes `v1.StrictServerInterface`, one method per operationId,
// each returning a typed response object:
//
//	AuthLogin(ctx context.Context, req AuthLoginRequestObject) (AuthLoginResponseObject, error)
//
// The response objects carry their status (`StatusCode()`) and body; oapi-codegen's
// generated gin `RegisterHandlersWithOptions` converts them to wire responses.
//
// The adapter below does two things:
//
//  1. routes the typed request to the real service layer, and
//  2. wraps the service result in the response Envelope before returning it, so
//     generated responses keep the `{success,data,meta}` contract that the legacy
//     handlers already write via pkg/response.
//
// Concrete response-type names (AuthLogin200JSONResponse, …) are what oapi-codegen
// emits for our operationIds/statuses. Run `make oapi-generate`, then reconcile any
// names below with the real output. Add one method per operationId you want served
// by the typed path; the rest keep the legacy handlers (which also write the
// envelope). The validator middleware (validator.go) applies on both paths.
type StrictHandler struct {
	// AuthSvc is the slice of the auth service needed by the typed login path.
	// Bind it to the REAL service interface in the wiring code.
	AuthSvc AuthService
	// Env wraps service results in the response Envelope.
	Env EnvelopeWrap
}

// AuthService is the subset of the auth service contract the typed login uses.
// Tighten to the actual interface when wiring.
type AuthService interface {
	Login(ctx context.Context, email, password, device string) (TokenPair, error)
}

// TokenPair mirrors auth's token response (bind the real type when wiring).
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// EnvelopeWrap constructs Envelope responses without importing gin into handlers.
type EnvelopeWrap interface {
	Success(data any) any
}

// Reference wiring for auth.login. Commented because it symbol-references the
// generated v1 types; uncomment + fix names after the first `make oapi-generate`.
//
//	func (s *StrictHandler) AuthLogin(ctx context.Context, req v1.AuthLoginRequestObject) (v1.AuthLoginResponseObject, error) {
//		pair, err := s.AuthSvc.Login(ctx, req.Body.Email, req.Body.Password, deref(req.Body.DeviceName))
//		if err != nil {
//			return nil, err // maps to a 4xx via the error catalogue
//		}
//		return v1.AuthLogin200JSONResponse{
//			Body: v1.TokenResponse{
//				Success:        true,
//				AccessToken:    pair.AccessToken,
//				RefreshToken:   pair.RefreshToken,
//				TokenType:      pair.TokenType,
//				ExpiresIn:      pair.ExpiresIn,
//			},
//		}, nil
//	}