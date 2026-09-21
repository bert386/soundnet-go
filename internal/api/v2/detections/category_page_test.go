package detections

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Paging runs after the category filter, so pages must be full and the last
// one short - never the ragged "1 to 14, then 26 to 38" the first version gave.
func TestPageOfSlicesTheFilteredList(t *testing.T) {
	t.Parallel()

	all := make([]DetectionResponse, 60)
	for i := range all {
		all[i].ID = uint(i + 1)
	}

	first := pageOf(all, 0, 25)
	assert.Len(t, first, 25)
	assert.Equal(t, uint(1), first[0].ID)

	second := pageOf(all, 25, 25)
	assert.Len(t, second, 25)
	assert.Equal(t, uint(26), second[0].ID)

	last := pageOf(all, 50, 25)
	assert.Len(t, last, 10, "the last page is short, not padded or empty")

	assert.Empty(t, pageOf(all, 60, 25), "a page past the end is empty, not a panic")
	assert.Empty(t, pageOf(all, 500, 25))
	assert.Len(t, pageOf(all, -5, 25), 25, "a negative offset reads from the start")
	assert.Len(t, pageOf(all, 0, 0), 60, "no limit returns everything")
}
