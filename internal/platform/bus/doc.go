// Package bus provides synchronous in-process domain event dispatch.
//
// Events are dispatched inside the caller's database transaction. Handlers
// receive the same db.Tx as the publisher, so a handler error can make the
// caller roll back all source and derived writes together.
package bus
