// Package identity owns the single-row company profile read by every module.
//
// No copies rule: consumers must call Profile at render time and never store
// copies of the company name or logo. Already-rendered immutable documents keep
// the identity they were generated with; new renders read the current profile.
package identity
