package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bert386/soundnet-go/internal/eventrecord"
)

func TestHourlyEnginesFoldsTypesIntoClasses(t *testing.T) {
	t.Parallel()
	got := hourlyEngines([]eventrecord.HourlyType{
		{Hour: 11, TypeCode: "B738", Detections: 30},
		{Hour: 11, TypeCode: "A320", Detections: 5},
		{Hour: 11, TypeCode: "C208", Detections: 2},
		{Hour: 11, TypeCode: "", Detections: 4},
		{Hour: 12, TypeCode: "A139", Detections: 1},
	})
	assert.Equal(t, []soundNetHourlyEngine{
		{Hour: 11, Engine: "jet", Detections: 35},
		{Hour: 11, Engine: "other", Detections: 4},
		{Hour: 11, Engine: "prop", Detections: 2},
		{Hour: 12, Engine: "helicopter", Detections: 1},
	}, got)
}
