// Package domain contains Document lookup values independent of storage and transport types.
package domain

// StoredDocument is one metadata row returned by the Document-owned schema.
type StoredDocument struct {
	PolicyNumber string
	Bytes        []byte
}

// Document is one retrievable policy document returned by the lookup use case.
type Document struct {
	PolicyNumber string
	Content      []byte
}
