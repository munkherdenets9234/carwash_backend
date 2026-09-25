package public

import (
	"github.com/eandstravel/carwash/internal/api/apictx"
	"github.com/eandstravel/carwash/pkg/response"
	"github.com/gin-gonic/gin"
)

// tenantController answers "whose car wash is this".
//
// The storefront needs a trading name before it can render a header, and it
// has no token at that point — a visitor reading the price list has not
// signed in and never will. So this sits on the public surface, behind the
// tenant gate like everything else: the API key already identifies the
// business, and this returns what that business is called.
//
// It costs nothing. The tenant middleware has already fetched and cached the
// entitlement for this request, and the name came with it; this reads it off
// the context rather than making a call of its own.
type tenantController struct{}

// businessView is what the storefront gets. Deliberately not the whole
// entitlement: a public page has no business knowing which modules the
// subscription includes, what the plan's limits are, or when the billing
// period ends. Those are the operator's commercial details, and the fact
// that they happen to be in the same struct on the server is not a reason to
// put them on an anonymous page.
type businessView struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Show returns the tenant's display identity.
func (h *tenantController) Show(c *gin.Context) error {
	ent := apictx.Entitlement(c)

	response.OK(c, businessView{
		Name: ent.Name,
		Slug: ent.Slug,
	})
	return nil
}
