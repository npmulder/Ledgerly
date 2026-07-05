// Package testdb provisions isolated PostgreSQL databases for integration
// suites.
//
// Tests should share the process-wide Postgres runtime through TestMain:
//
//	func TestMain(m *testing.M) {
//		os.Exit(testdb.Main(m))
//	}
//
// New creates one database per suite from a migrated template database. Raw
// returns the suite's superuser pool, and AsModule returns a pool pinned to a
// Ledgerly module role and schema.
package testdb
