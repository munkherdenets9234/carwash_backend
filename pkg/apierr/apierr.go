// Package apierr is the closed set of errors this API is allowed to return.
//
// Every error a client sees is built by one of the constructors below, so the
// wire format cannot drift between endpoints and a client can branch on
// Code — a stable machine-readable identifier — rather than on Message, which
// is prose and may be reworded or translated at any time.
//
// Three things travel with every error:
//
//   - Domain, the subsystem it came from (AUTH, TENANT, DB, UPLOAD, ...).
//     Useful for alerting: "every 5xx today is DOMAIN=UPLOAD" is a far more
//     actionable page than "the API is throwing 500s".
//   - Code, what went wrong, from the fixed list below.
//   - Err, the underlying cause. It is logged, never serialised — the cause
//     of a database failure is for us, not for the caller.
//
// Stack is captured at construction and rendered only in development (see
// response.Err), so a production client never learns our package layout.
package apierr

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
)

// Domain constants group errors by the subsystem they originate in.
const (
	DomainGeneral     = "GENERAL"
	DomainAuth        = "AUTH"
	DomainDB          = "DB"
	DomainCatalog     = "CATALOG"     // services, locations
	DomainSchedule    = "SCHEDULE"    // shifts and availability
	DomainReservation = "RESERVATION" // bookings made against a slot
	DomainAttendance  = "ATTENDANCE"  // clock-in / clock-out
)

// Code constants are the machine-readable error identifiers. Clients branch on
// these. Adding one is a deliberate act; reusing an existing one is preferred.
const (
	CodeInternal         = "INTERNAL_ERROR"
	CodeBadRequest       = "BAD_REQUEST"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeConflict         = "CONFLICT"
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeUpstream         = "UPSTREAM_ERROR"

	// CodeRateLimited accompanies 429. The response carries a Retry-After
	// header alongside it (see response.Err).
	CodeRateLimited = "RATE_LIMITED"

	// CodeFeatureUnavailable accompanies 503 on a route whose optional
	// dependency was not configured at startup. It exists so a client can
	// tell "this deployment has no image uploads" from "your request was
	// wrong" — the two used to be indistinguishable, which is how a feature
	// stayed silently unmounted in production for a week.
	CodeFeatureUnavailable = "FEATURE_UNAVAILABLE"

	// CodeOutsideGeofence accompanies 422 when an employee tries to clock in
	// from outside their location's accepted radius. It is distinct from a
	// plain validation failure because the client acts on it differently:
	// the right response is "move closer", not "fix your input", and the app
	// shows the measured distance rather than a form error.
	CodeOutsideGeofence = "OUTSIDE_GEOFENCE"

	// CodeSlotUnavailable accompanies 409 when the requested employee is not
	// free for the whole of the requested window — off shift, already booked,
	// or the service would run past the end of their shift.
	CodeSlotUnavailable = "SLOT_UNAVAILABLE"
)

// APIError is a structured application error carrying everything needed to
// render one consistent API response and one detailed server-side log entry.
type APIError struct {
	HTTPStatus int
	Domain     string
	Code       string
	Message    string
	Err        error  // underlying cause; logged, never serialised
	Stack      string // captured at construction; rendered only in dev
	RetryAfter int    // seconds, for CodeRateLimited; 0 otherwise
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s/%s] %s: %v", e.Domain, e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s/%s] %s", e.Domain, e.Code, e.Message)
}

// Unwrap exposes the cause to errors.Is / errors.As.
func (e *APIError) Unwrap() error { return e.Err }

// In returns a copy of e attributed to a different domain. Use it when a
// generic constructor is right but the subsystem is not, e.g.
// apierr.NotFound("tenant").In(apierr.DomainTenant).
func (e *APIError) In(domain string) *APIError {
	e.Domain = domain
	return e
}

func captureStack(skip int) string {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	lines := strings.Split(string(buf[:n]), "\n")
	// Each frame is two lines. Drop the goroutine header plus captureStack,
	// New/Wrap and the convenience constructor that called them.
	start := 1 + skip*2
	if start < len(lines) {
		lines = lines[start:]
	}
	return strings.Join(lines, "\n")
}

// New builds an APIError with no underlying cause.
func New(httpStatus int, domain, code, message string) *APIError {
	return &APIError{
		HTTPStatus: httpStatus,
		Domain:     domain,
		Code:       code,
		Message:    message,
		Stack:      captureStack(2),
	}
}

// Wrap builds an APIError carrying err as its cause.
func Wrap(err error, httpStatus int, domain, code, message string) *APIError {
	return &APIError{
		HTTPStatus: httpStatus,
		Domain:     domain,
		Code:       code,
		Message:    message,
		Err:        err,
		Stack:      captureStack(2),
	}
}

// ── Convenience constructors — use these in handlers, services and middleware ──

// Internal reports a fault on our side. The caller always sees the same
// message; err is for the log. Pass the cause whenever one is in scope —
// "internal server error" with no cause attached is how a 500 becomes
// undiagnosable after the fact.
func Internal(err error) *APIError {
	return Wrap(err, http.StatusInternalServerError, DomainGeneral, CodeInternal, "internal server error")
}

// BadRequest reports a malformed request — a bad id, an unparseable body.
func BadRequest(msg string) *APIError {
	return New(http.StatusBadRequest, DomainGeneral, CodeBadRequest, msg)
}

// Unauthorized reports missing or invalid credentials.
func Unauthorized(msg string) *APIError {
	if msg == "" {
		msg = "unauthorized"
	}
	return New(http.StatusUnauthorized, DomainAuth, CodeUnauthorized, msg)
}

// Forbidden reports valid credentials that do not permit this action.
func Forbidden(msg string) *APIError {
	if msg == "" {
		msg = "forbidden"
	}
	return New(http.StatusForbidden, DomainAuth, CodeForbidden, msg)
}

// NotFound reports a missing resource. Pass the resource name ("blog"), not a
// sentence — the constructor writes the sentence, so every 404 reads alike.
func NotFound(resource string) *APIError {
	return New(http.StatusNotFound, DomainGeneral, CodeNotFound, fmt.Sprintf("%s not found", resource))
}

// Conflict reports a request that collides with existing state — a duplicate
// slug, an email already registered.
func Conflict(msg string) *APIError {
	return New(http.StatusConflict, DomainGeneral, CodeConflict, msg)
}

// ValidationFailed reports a well-formed request whose contents are not
// acceptable. Distinct from BadRequest: the body parsed, the values are wrong.
func ValidationFailed(msg string) *APIError {
	return New(http.StatusUnprocessableEntity, DomainGeneral, CodeValidationFailed, msg)
}

// Upstream wraps a failure from an external service as 502. Use it in
// integration code so a third party's outage is never reported as our bug.
func Upstream(domain string, err error) *APIError {
	return Wrap(err, http.StatusBadGateway, domain, CodeUpstream, "upstream service unavailable")
}

// RateLimited reports that the caller has exceeded a limiter's allowance.
// retryAfterSeconds is echoed in the Retry-After response header.
func RateLimited(retryAfterSeconds int) *APIError {
	e := New(http.StatusTooManyRequests, DomainGeneral, CodeRateLimited, "too many requests")
	e.RetryAfter = retryAfterSeconds
	return e
}

// FeatureUnavailable reports a route whose optional dependency was not
// configured at startup. Returning this rather than a 404 or a generic 500 is
// what makes a degraded deployment visible to the client instead of looking
// like a bug in their request.
func FeatureUnavailable(feature string) *APIError {
	return New(http.StatusServiceUnavailable, DomainGeneral, CodeFeatureUnavailable,
		feature+" is not configured on this deployment")
}

// OutsideGeofence reports a clock-in attempt made too far from the location.
//
// The measured and permitted distances are both in the message on purpose. An
// employee standing in the right car park who is refused needs to know
// whether they are 5 metres over or 500 — the first is a GPS drift they can
// wait out, the second means they are at the wrong branch.
func OutsideGeofence(distanceM, radiusM float64) *APIError {
	return New(http.StatusUnprocessableEntity, DomainAttendance, CodeOutsideGeofence,
		fmt.Sprintf("you are %.0f m from the site; clock-in is allowed within %.0f m", distanceM, radiusM))
}

// SlotUnavailable reports a reservation request the schedule cannot satisfy.
func SlotUnavailable(msg string) *APIError {
	if msg == "" {
		msg = "the selected employee is not available for that time"
	}
	return New(http.StatusConflict, DomainSchedule, CodeSlotUnavailable, msg)
}
