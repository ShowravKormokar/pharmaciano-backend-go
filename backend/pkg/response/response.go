package response

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/common/constants"
	"backend/pkg/pagination"
)

// Envelope is the single response shape used by every REST endpoint.
type Envelope struct {
	Success    bool             `json:"success"`
	Data       any              `json:"data,omitempty"`
	Pagination *pagination.Meta `json:"pagination,omitempty"`
	Error      *ErrorBody       `json:"error,omitempty"`
	Meta       Meta             `json:"meta"`
}

// Meta is inline metadata every response carries for tracing and clock skew.
type Meta struct {
	RequestID string    `json:"request_id"`
	Timestamp time.Time `json:"timestamp"`
	Version   string    `json:"version,omitempty"`
}

// Writer abstracts a Gin/net-http compatible response writer so pkg/response does not depend on any framework. Handlers pass an adapter.
type Writer interface {
	Header() http.Header
	WriteHeader(int)
	Write([]byte) (int, error)
}

func newMeta(requestID string) Meta {
	if requestID == "" {
		requestID = uuid.NewString()
	}
	return Meta{RequestID: requestID, Timestamp: time.Now().UTC().Truncate(time.Second)}
}

// writeJSON is the single place that actually writes a response.
func writeJSON(w Writer, requestID string, status int, body Envelope) error {
	w.Header().Set(constants.HeaderRequestID, requestID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}

// OK writes 200.
func OK(w Writer, requestID string, data any) error {
	return writeJSON(w, requestID, http.StatusOK, Envelope{Success: true, Data: data, Meta: newMeta(requestID)})
}

// Created writes 201.
func Created(w Writer, requestID string, data any) error {
	return writeJSON(w, requestID, http.StatusCreated, Envelope{Success: true, Data: data, Meta: newMeta(requestID)})
}

// Accepted writes 202
func Accepted(w Writer, requestID, location string, data any) error {
	if location != "" {
		w.Header().Set("Location", location)
	}
	return writeJSON(w, requestID, http.StatusAccepted, Envelope{Success: true, Data: data, Meta: newMeta(requestID)})
}

// NoContent writes 204 (no body).
func NoContent(w Writer, requestID string) error {
	w.Header().Set(constants.HeaderRequestID, requestID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ListOptions configures List's optional Link header.
type ListOptions struct {
	SelfURL string
}

func List(w Writer, requestID string, data any, meta pagination.Meta, opts ...ListOptions) error {
	h := w.Header()
	if meta.Total > 0 {
		h.Set(constants.HeaderTotalCount, strconv.FormatInt(meta.Total, 10))
	}
	if meta.Page > 0 {
		h.Set(constants.HeaderPage, strconv.Itoa(meta.Page))
	}
	if meta.Limit > 0 {
		h.Set(constants.HeaderLimit, strconv.Itoa(meta.Limit))
	}
	if meta.NextCursor != "" {
		h.Set(constants.HeaderNextCursor, meta.NextCursor)
	}
	if len(opts) > 0 && opts[0].SelfURL != "" {
		if link := buildLinkHeader(opts[0].SelfURL, meta); link != "" {
			h.Set(constants.HeaderLink, link)
		}
	}
	metaCopy := meta
	return writeJSON(w, requestID, http.StatusOK, Envelope{
		Success: true, Data: data, Pagination: &metaCopy, Meta: newMeta(requestID),
	})
}

func buildLinkHeader(selfURL string, meta pagination.Meta) string {
	u, err := url.Parse(selfURL)
	if err != nil {
		return ""
	}
	var links []string
	switch {
	case meta.NextCursor != "":
		q := u.Query()
		q.Set("cursor", meta.NextCursor)
		u.RawQuery = q.Encode()
		links = append(links, fmt.Sprintf(`<%s>; rel="next"`, u.String()))
	case meta.Page > 0 && meta.TotalPages > 0:
		if meta.Page < meta.TotalPages {
			q := u.Query()
			q.Set("page", strconv.Itoa(meta.Page+1))
			u.RawQuery = q.Encode()
			links = append(links, fmt.Sprintf(`<%s>; rel="next"`, u.String()))
		}
		if meta.Page > 1 {
			q := u.Query()
			q.Set("page", strconv.Itoa(meta.Page-1))
			u.RawQuery = q.Encode()
			links = append(links, fmt.Sprintf(`<%s>; rel="prev"`, u.String()))
		}
	}
	return strings.Join(links, ", ")
}

func SetDeprecated(w Writer, sunset time.Time, migrationLink string) {
	h := w.Header()
	h.Set(constants.HeaderDeprecation, "true")
	h.Set(constants.HeaderSunset, sunset.UTC().Format(http.TimeFormat))
	if migrationLink != "" {
		h.Set(constants.HeaderLink, fmt.Sprintf(`<%s>; rel="deprecation"`, migrationLink))
	}
}