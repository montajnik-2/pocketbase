package models_test

import (
	"testing"

	"github.com/montajnik-2/pocketbase/models"
)

func TestParamTableName(t *testing.T) {
	m := models.Param{}
	if m.TableName() != "_params" {
		t.Fatalf("Unexpected table name, got %q", m.TableName())
	}
}
