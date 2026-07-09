package placeholder

import "testing"

func TestValidateRequiresCanonicalPath(t *testing.T) {
	err := validate(Metadata{
		SchemaVersion: Schema,
		Kind:          Kind,
		Project:       "alpha",
		Identity:      "https://example.invalid/bram/alpha",
	})
	if err == nil {
		t.Fatal("validate error = nil, want incomplete metadata error")
	}
}
