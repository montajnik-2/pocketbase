package validators_test

import (
	"testing"

	"github.com/montajnik-2/pocketbase/forms/validators"
	"github.com/montajnik-2/pocketbase/tests"
)

func TestUniqueId(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	scenarios := []struct {
		id          string
		tableName   string
		expectError bool
	}{
		{"", "", false},
		{"test", "", true},
		{"wsmn24bux7wo113", "_collections", true},
		{"test_unique_id", "unknown_table", true},
		{"test_unique_id", "_collections", false},
	}

	for i, s := range scenarios {
		err := validators.UniqueId(app.Dao(), s.tableName)(s.id)

		hasErr := err != nil
		if hasErr != s.expectError {
			t.Errorf("(%d) Expected hasErr to be %v, got %v (%v)", i, s.expectError, hasErr, err)
		}
	}
}
