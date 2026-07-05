// Package httpserver owns Ledgerly's process-level HTTP infrastructure.
//
// Module route convention:
//
// Domain modules expose a function with this shape:
//
//	func RegisterRoutes(r chi.Router)
//
// Wiring code passes that function as Module.RegisterRoutes with a stable
// Module.Name. The platform mounts it under /api/<module>, so module packages
// declare routes relative to their namespace, for example /invoices rather
// than /api/invoicing/invoices.
//
// OpenAPI fragment convention:
//
// Domain modules expose an OpenAPIFragment beside their route registration.
// A fragment contains only the module-owned path entries, using fully
// namespaced paths such as /api/invoicing/invoices, plus any component
// definitions the module owns. Platform wiring passes all fragments to
// NewRouter, which serves the assembled OpenAPI 3 document at
// /api/openapi.json.
package httpserver
