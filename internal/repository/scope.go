package repository

import (
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// scoped builds a query filter that is always tenant-scoped.
//
// Every read and every write in this package goes through it. The point is
// not brevity — `bson.M{"tenant_id": t, "_id": id}` is barely longer — it is
// that there is one place where the scoping can be seen to happen, and a
// filter built any other way stands out in review as the thing to question.
//
// The scoping is in the FILTER, never a comparison after the read. The
// difference is the whole security model: a filter that is missing its tenant
// returns another business's rows and looks like success, while a check that
// is missing returns them too and looks like success. Only the filter makes
// the unsafe version awkward to write.
//
// Cross-tenant reads do not exist in this service. There is no admin surface
// here that spans businesses; the platform holds that, and it holds no car
// wash data. So there is no legitimate caller for an unscoped query, and none
// is provided.
func scoped(tenantID primitive.ObjectID, filter bson.M) bson.M {
	out := make(bson.M, len(filter)+1)
	for k, v := range filter {
		// A caller passing tenant_id itself is either duplicating the scope
		// or trying to override it. Neither is something to silently honour.
		if k == "tenant_id" {
			panic("repository: do not set tenant_id in a filter; scoped() owns it")
		}
		out[k] = v
	}
	out["tenant_id"] = tenantID
	return out
}

// scopedID is the common case: one document by id, within a tenant.
func scopedID(tenantID, id primitive.ObjectID) bson.M {
	return bson.M{"_id": id, "tenant_id": tenantID}
}
