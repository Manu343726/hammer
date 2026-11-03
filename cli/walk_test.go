package cli

import (
	"testing"

	api "github.com/Manu343726/hammer"
	"github.com/stretchr/testify/assert"
)

// The flag walk follows a schema's members, so a schema that refers back to itself
// is the one shape that would not terminate. The walk stops at an object already on
// its path; the depth bound is the second line of defence, for a description that is
// merely deep rather than circular.
func TestASchemaThatContainsItselfIsNotFlattened(t *testing.T) {
	cyclic := &api.Schema{Type: api.TypeObject}
	cyclic.Properties = []api.Property{{Name: "child", Schema: cyclic}}

	ordinary := &api.Schema{Type: api.TypeObject, Properties: []api.Property{
		{Name: "child", Schema: api.StringSchema()},
	}}

	assert.True(t, flattenable(ordinary, 0, nil), "an object with members is flattened")
	assert.False(t, flattenable(cyclic, 0, []*api.Schema{cyclic}), "an object already on the path is not")
	assert.False(t, flattenable(ordinary, maxFlagDepth, nil), "and neither is one past the bound")
}
