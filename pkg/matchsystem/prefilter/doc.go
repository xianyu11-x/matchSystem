// Package prefilter implements strict index-backed candidate prefiltering.
//
// Prefilter owns the closed bitmap language, its compiler, compile-time
// physical requirements, strict prefilter/v3 envelope and Roaring Bitmap
// indexes. Scalar operands are compiled by the shared expression package and
// retained as opaque ScalarPrograms. Tick-scoped sessions execute the private
// bitmap topology without exposing an intermediate IR.
// IndexStore ownership is single-goroutine by contract: Add, Remove and session
// execution are serialized by the owner, and this package adds no locks or
// concurrent snapshot abstraction. It never scans candidate Tickets as a
// fallback. Callers remain responsible for final group correctness and
// candidate scoring.
package prefilter
