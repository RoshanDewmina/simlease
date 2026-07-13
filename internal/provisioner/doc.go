// Package provisioner implements broker.Provisioner against public simctl
// interfaces: clone a Simurgh-owned base device, rename to
// simurgh-clone[<lease-id>:<model>:<runtime>], boot, and wait for Booted.
//
// This package performs no destructive simulator operations (no delete, erase,
// or shutdown of devices it did not just create in the same Provision call).
// Base devices use the simurgh-base[...] name so they never match the
// ^simurgh-(clone|warm)\[.*\]$ deletion-invariant regex used by M1.3/M1.4.
package provisioner
