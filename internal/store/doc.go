// Package store persists daemon state in an atomic state.json journal.
//
// M1.6 added an opaque Leases field (json.RawMessage) that the broker
// marshals/unmarshals. This package never interprets lease contents.
package store
