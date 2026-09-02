// Package legacy holds the API surface that is on its way out.
//
// It exists so the deprecation lint has real markers to check - and it is
// written the way a real package looks mid-migration: one deprecation done
// properly, and three done the way they usually are.
package legacy

import (
	"errors"
	"strings"
)

// FindProduct looks a product up by SKU.
//
// Deprecated: use catalog.Store.Get instead, which returns a typed error
// rather than a bare nil. This will be removed after 2026-12-01; see
// https://docs.example.com/migrations/products for the two-line change.
func FindProduct(sku string) (string, error) {
	if sku == "" {
		return "", errors.New("empty sku")
	}

	return strings.ToUpper(sku), nil
}

// LookupProduct is the older spelling of FindProduct.
//
// Deprecated: this one is missing its removal date, which is the most common
// way a deprecation stalls: with no date there is no reason to migrate today,
// so nobody does, and it is still here three years later.
func LookupProduct(sku string) (string, error) {
	return FindProduct(sku)
}

// ProductName returns a display name.
//
// Deprecated: scheduled for removal after 2020-01-01.
//
// The date has passed, which means one of two things and both need a decision:
// either it should have been removed, or it is not actually going away and the
// marker is lying to every caller who reads it.
func ProductName(sku string) string {
	name, _ := FindProduct(sku)

	return name
}

// MaxResults caps the legacy search endpoint.
//
// Deprecated: no longer used.
//
// This is the least useful form of deprecation: it names no replacement, so a
// caller reading it learns that they have a problem and nothing about how to
// solve it.
const MaxResults = 100
