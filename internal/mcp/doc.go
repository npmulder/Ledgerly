// Package mcp implements Ledgerly's stdio Model Context Protocol server.
//
// The server intentionally uses a small JSON-RPC 2.0 implementation instead of
// an SDK dependency. Ledgerly only needs the initialize, tools/list, tools/call,
// initialized notification, and ping methods for MCP-1, and keeping those
// methods local makes the CLI a thin, auditable wrapper over the generated HTTP
// client.
package mcp
