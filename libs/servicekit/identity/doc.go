// Package identity provides neutral runtime identity values and a pure
// development constructor and an opt-in deployment validator. It does not read
// process state or environment or generate random IDs. Services choose their
// policy and retain responsibility for loading and startup validation.
package identity
