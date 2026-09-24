package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MediaRole is where a photograph appears on the site.
//
// A role rather than a position in a list. The site used to take its home
// page cover from photos[0] and its about-section image from photos[4],
// which meant "the cover" was not a thing anybody could set — it was
// whichever photograph happened to sort first, and changing it meant
// reordering an album until the right one landed at the top.
type MediaRole string

const (
	// MediaRoleHero is the home page cover. One per tenant.
	MediaRoleHero MediaRole = "hero"
	// MediaRoleAbout is the photograph beside the about text. One per tenant.
	MediaRoleAbout MediaRole = "about"
	// MediaRoleGallery is the album: many, ordered, tagged.
	MediaRoleGallery MediaRole = "gallery"
)

func (r MediaRole) Valid() bool {
	switch r {
	case MediaRoleHero, MediaRoleAbout, MediaRoleGallery:
		return true
	}
	return false
}

// Singular reports whether a role admits at most one image per tenant.
func (r MediaRole) Singular() bool {
	return r == MediaRoleHero || r == MediaRoleAbout
}

// GalleryTags is the closed set of filters the gallery page offers. Stored as
// a plain string on Media rather than an enum type, because it is a label the
// site groups by and not something the server branches on.
var GalleryTags = []string{"exterior", "interior", "detailing", "beforeAfter"}

func ValidGalleryTag(tag string) bool {
	for _, t := range GalleryTags {
		if t == tag {
			return true
		}
	}
	return false
}

// Media is one uploaded photograph.
//
// The bytes live at an image host; this is the record of them. URL is what a
// page renders, PublicID is what lets the host be told to delete it — keeping
// both means removing a row can also remove the file, instead of leaving an
// orphan nobody will ever find again.
type Media struct {
	ID       primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	TenantID primitive.ObjectID `bson:"tenant_id" json:"-"`

	Role MediaRole `bson:"role" json:"role"`

	URL      string `bson:"url" json:"url"`
	PublicID string `bson:"public_id" json:"-"`

	// Width and Height are the intrinsic pixel size, taken from the upload
	// response rather than asked of whoever uploaded it.
	//
	// next/image needs both to reserve the right space before the file
	// arrives. Without them every photograph on the page causes a layout
	// shift as it loads, and asking a person to type them is asking for the
	// one field nobody checks.
	Width  int `bson:"width" json:"width"`
	Height int `bson:"height" json:"height"`

	// Alt describes the photograph for someone who cannot see it, per locale.
	//
	// Never decorative here: these are photographs of work, and the caption
	// is the only description of what the picture shows. Stored as a locale
	// map for the same reason the rest of the site's copy is.
	Alt map[string]string `bson:"alt" json:"alt"`

	// Tag groups a gallery photograph under one of GalleryTags. Empty for
	// hero and about, which are not filtered.
	Tag string `bson:"tag,omitempty" json:"tag,omitempty"`

	// Feature marks a tile that takes more room in the grid.
	Feature bool `bson:"feature" json:"feature"`

	SortOrder int  `bson:"sort_order" json:"sort_order"`
	Active    bool `bson:"active" json:"active"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}
