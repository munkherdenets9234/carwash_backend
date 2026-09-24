package models

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// The derived keys that back this service's unique indexes.
//
// Both live here, as functions, for one reason: each is written in one place
// and read in another, and a unique index whose key is built differently by
// the writer and the reader is an index that silently stops constraining
// anything. Before, the reservation key was assembled in the service and the
// time-entry key in the repository; now neither can drift from its reader
// without changing this file.
//
// Both begin with the tenant. Neither strictly needs to — an employee belongs
// to exactly one business, so an employee id already implies a tenant — but
// the INDEXES are global, and a reader asking whether cross-tenant collision
// is possible would otherwise have to go and confirm that employee ids are
// never shared. With the prefix the answer is visible in the key itself.

const keySep = "|"

// SlotKey identifies one employee's booking at one instant.
//
// Present only while a booking is live; cancelling unsets it, which is what
// makes the sparse unique index allow any number of cancelled bookings at the
// same time while forbidding two live ones.
//
// The instant is formatted RFC3339 in UTC, so the same moment always produces
// the same string regardless of the offset it arrived in. A key built from a
// local-time rendering would let 09:00+08:00 and 01:00Z — the same instant —
// occupy two different slots.
func SlotKey(tenantID, employeeID primitive.ObjectID, start time.Time) string {
	return tenantID.Hex() + keySep + employeeID.Hex() + keySep + start.UTC().Format(time.RFC3339)
}

// OpenKey identifies an employee's running time entry.
//
// Present only while the entry is open and unset at clock-out, so a sparse
// unique index on it forbids a second clock-in while allowing an employee any
// number of closed entries.
func OpenKey(tenantID, employeeID primitive.ObjectID) string {
	return tenantID.Hex() + keySep + employeeID.Hex()
}

// LoginKey identifies an account that can sign in.
//
// Present only when the user has an email address, which is what makes the
// sparse unique index on it work: a business has at most one login per email
// and any number of GUEST customers, who have no email at all.
//
// This replaced a plain unique index on (tenant_id, email). That index was
// correct while every customer registered, and became wrong the moment
// booking stopped requiring an account: a missing field indexes as null, and
// Email carries no omitempty, so every guest would have stored email "" and
// the SECOND guest of a business would have been refused at the database
// with a duplicate-key error that says nothing about what actually happened.
//
// Lowercased, because an email address is not case sensitive in the part
// that matters here and "Bat@x.mn" and "bat@x.mn" must not be two accounts.
func LoginKey(tenantID primitive.ObjectID, email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	return tenantID.Hex() + keySep + email
}

// ContactKey identifies a customer by the phone number they book with.
//
// A guest has no account, so the phone number is the only durable thing
// about them. Keying on it means the same person booking a second time is
// recognised rather than duplicated, so a business sees one customer with
// two visits instead of two customers with one each — which is the whole
// point of asking for the number.
//
// Sparse-unique like the others, because staff and older customers may have
// no phone recorded and must not all collide on one empty key.
//
// It deliberately does NOT prove anything. Anyone can type anyone's number.
// It is a way of grouping bookings, never a way of authorising access to
// them: reading a booking back needs the reference code as well.
func ContactKey(tenantID primitive.ObjectID, phone string) string {
	phone = NormalizePhone(phone)
	if phone == "" {
		return ""
	}
	return tenantID.Hex() + keySep + phone
}

// CountryCallingCode is the dialling code this service assumes when a
// customer does not write one.
//
// Hard-coded rather than configured, like the currency: this is a car wash
// in Ulaanbaatar, the prices are MNT, and a deployment that needed a
// different country would need more than this constant changed. When that
// day comes it moves to config, and this comment is the note saying so.
const CountryCallingCode = "976"

// subscriberDigits is the length of a local number once the code is off.
// Mongolian mobile and landline numbers are eight digits.
const subscriberDigits = 8

// NormalizePhone reduces a typed phone number to one canonical form.
//
// "99112233", "9911-2233", "9911 2233", "+976 9911 2233" and "976 9911 2233"
// are one person to everybody except a string comparison. Without this the
// same customer books five times and the business sees five customers — and
// worse, somebody looking their booking up by typing the number the other
// way is told it does not exist.
//
// The country code is REMOVED rather than preserved. An earlier version kept
// a leading + on the reasoning that it distinguishes an international number
// from a local one that happens to share digits. That reasoning does not
// survive contact with the thing: +976 9911 2233 and 9911 2233 are not two
// numbers that happen to look alike, they are the same telephone, and
// treating them as two customers is the bug the normalisation exists to
// prevent.
//
// Numbers that are not the local shape are left as their digits, so a
// genuinely foreign number still works as a key — it simply keeps its code,
// which is correct, because without it it would not be dialable.
func NormalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()

	// Only strip the code when what is left is a plausible local number.
	// Blindly removing a leading "976" would mangle a foreign number that
	// happens to begin with those digits.
	if len(digits) == len(CountryCallingCode)+subscriberDigits &&
		strings.HasPrefix(digits, CountryCallingCode) {
		return digits[len(CountryCallingCode):]
	}
	return digits
}

// ReferenceKey identifies one booking by the code its customer holds.
//
// A derived single-field key rather than a compound index on (tenant_id,
// reference), and that distinction is not cosmetic — the compound version
// was written first and would not build.
//
// A compound SPARSE index skips a document only when EVERY indexed field is
// missing. tenant_id is never missing, so every booking made before
// references existed was indexed with reference: null, and the second one
// collided with the first. The index could not be created at all, and the
// service refused to start.
//
// A single-field sparse index has no such subtlety: a booking with no
// reference has no reference_key, so it is simply not in the index. This is
// the same shape as SlotKey and OpenKey above, for the same reason, which is
// why they look the way they do.
func ReferenceKey(tenantID primitive.ObjectID, reference string) string {
	reference = NormalizeReference(reference)
	if reference == "" {
		return ""
	}
	return tenantID.Hex() + keySep + reference
}
