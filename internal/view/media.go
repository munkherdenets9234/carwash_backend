package view

import (
	"github.com/eandstravel/carwash/internal/models"
)

// Media is a photograph as any caller sees it.
//
// One response type for both audiences here, unlike reservations, and that is
// a deliberate exception rather than an oversight: a photograph on a public
// page has no private half. The manager sees the same URL, size and
// description a visitor does — what differs is only WHICH rows each is shown,
// and that is a filter, not a shape.
//
// PublicID is absent and unrepresentable. It is the handle that lets the
// image host be told to delete a file, it is of no use to any client, and a
// field that does not exist cannot be added to a response by accident.
type Media struct {
	ID   string `json:"id"`
	Role string `json:"role"`

	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`

	// Alt is the locale map, sent whole rather than resolved to one
	// language. The site renders in two and switches without a round trip,
	// so picking one here would mean re-fetching every photograph to change
	// language.
	Alt map[string]string `json:"alt"`

	Tag     string `json:"tag,omitempty"`
	Feature bool   `json:"feature"`

	SortOrder int  `json:"sort_order"`
	Active    bool `json:"active"`
}

func MediaOf(m *models.Media) Media {
	alt := m.Alt
	if alt == nil {
		// Rendered as {} rather than null: a client that has to handle both
		// is a client with a bug waiting in it.
		alt = map[string]string{}
	}
	return Media{
		ID:        m.ID.Hex(),
		Role:      string(m.Role),
		URL:       m.URL,
		Width:     m.Width,
		Height:    m.Height,
		Alt:       alt,
		Tag:       m.Tag,
		Feature:   m.Feature,
		SortOrder: m.SortOrder,
		Active:    m.Active,
	}
}

func MediaListOf(ms []*models.Media) []Media {
	out := make([]Media, 0, len(ms))
	for _, m := range ms {
		out = append(out, MediaOf(m))
	}
	return out
}
