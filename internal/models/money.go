// Package models holds the persisted documents. Every one of them is a plain
// struct with bson and json tags: no behaviour, no database access, so a
// model can be constructed in a test without a Mongo connection.
package models

// MNT is an amount of Mongolian tugrik, held as a whole number in int64 and
// never as a float.
//
// A wash priced at 25000 with a 3000 bonus has no sub-unit to represent, and
// the daily report sums hundreds of these per branch. Floats would introduce
// rounding exactly where it is least welcome: in the line an employee reads
// to check their own bonus. The currency has no minor unit in practice, so
// the integer IS the amount and there is no scaling factor to remember.
type MNT = int64
