// Package xcodebuildmcp owns the Simurgh ↔ XcodeBuildMCP session adapter.
//
// Simurgh allocates leases, isolation paths, and process lifecycle. XcodeBuildMCP
// remains the build/test/UI engine. This package never invents UDIDs or DerivedData
// paths: bindings are derived only from an ACTIVE lease plus isolator lease-root
// layout (ARCHITECTURE §7 / report §11).
package xcodebuildmcp
