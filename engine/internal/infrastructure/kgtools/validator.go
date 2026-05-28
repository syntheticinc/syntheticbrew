package kgtools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SchemaValidator validates entity data against a JSON Schema (Draft 2020-12)
// using santhosh-tekuri/jsonschema/v6. The validator caches compiled schemas
// keyed by schema bytes so repeat validations of the same schema avoid
// re-parsing.
//
// External refs ($ref to https://... URLs) are forbidden: the loader is
// replaced with a no-op so any attempt to fetch a remote schema fails
// closed with a "remote ref disabled" error. This blocks KG-SEC-04 (schema
// injection via attacker-controlled URL).
type SchemaValidator struct {
	mu    sync.Mutex
	cache map[string]*jsonschema.Schema
}

// NewSchemaValidator returns a SchemaValidator with an empty cache.
func NewSchemaValidator() *SchemaValidator {
	return &SchemaValidator{cache: make(map[string]*jsonschema.Schema)}
}

// Validate validates entityData against schemaJSON. Returns a wrapped error
// describing the first violation, or nil on success. The error message is
// safe to surface to callers (does not leak internal paths).
func (v *SchemaValidator) Validate(ctx context.Context, schemaJSON, entityData []byte) error {
	if len(schemaJSON) == 0 {
		return errors.New("schema is empty")
	}
	if len(entityData) == 0 {
		return errors.New("entity data is empty")
	}

	sch, err := v.compile(schemaJSON)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}

	dataVal, err := jsonschema.UnmarshalJSON(bytes.NewReader(entityData))
	if err != nil {
		return fmt.Errorf("parse entity data: %w", err)
	}

	if err := sch.Validate(dataVal); err != nil {
		// Strip noisy compiler-internal paths; keep the human-readable summary.
		return errors.New(simplifyValidationError(err))
	}
	return nil
}

func (v *SchemaValidator) compile(schemaJSON []byte) (*jsonschema.Schema, error) {
	key := string(schemaJSON)

	v.mu.Lock()
	defer v.mu.Unlock()
	if sch, ok := v.cache[key]; ok {
		return sch, nil
	}

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	// Block external $refs to avoid KG-SEC-04 (schema injection via
	// attacker-controlled URL). The library defaults to a non-fetching loader
	// already; we make the policy explicit by registering a refusing loader.
	c.UseLoader(refusingLoader{})

	const url = "kg-entity-schema://compile"
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("add resource: %w", err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	v.cache[key] = sch
	return sch, nil
}

// refusingLoader implements jsonschema.URLLoader by always refusing.
// External $refs in customer schemas are blocked outright.
type refusingLoader struct{}

func (refusingLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external $ref disabled (KG-SEC-04): %s", url)
}

// simplifyValidationError converts a jsonschema validation error into a
// short, customer-friendly message. The full structured error is logged
// at the caller; this string is what surfaces in HTTP 400 responses.
func simplifyValidationError(err error) string {
	msg := err.Error()
	// Strip leading "jsonschema validation failed with " and trailing
	// compiler URL noise that leaks the internal "kg-entity-schema://" URL.
	msg = strings.ReplaceAll(msg, "kg-entity-schema://compile#", "")
	msg = strings.ReplaceAll(msg, "kg-entity-schema://compile", "")
	return msg
}
