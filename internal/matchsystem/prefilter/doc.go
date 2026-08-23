// Package prefilter implements strict index-backed candidate prefiltering.
//
// It contains closed declarative expressions, compile-time requirements
// validation, Roaring Bitmap indexes and Tick-scoped evaluation sessions.
// IndexStore ownership is single-goroutine by contract: Add, Remove and session
// execution are serialized by the owner, and this package adds no locks or
// concurrent snapshot abstraction. It never scans candidate Documents as a
// fallback. Callers remain responsible for final group correctness and
// candidate scoring.
package prefilter
