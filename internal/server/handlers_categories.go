package server

import (
	"net/http"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

// defaultCategoryIcons are the emoji offered in the Settings category picker.
var defaultCategoryIcons = []string{
	"📍", "🎯", "🍽️", "☕", "🍷", "🏛️", "🖼️", "🎭", "🏰", "⛪",
	"🏖️", "⛰️", "🥾", "📷", "🛒", "🎡", "🎢", "🏊", "🏨", "🚗",
	"🚆", "✈️", "⛴️", "🎉", "⭐",
}

// handleCreateCategory adds a new user-managed item category.
func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	name := strings.TrimSpace(formStr(r, "name"))
	if name == "" || !maxLen(name, 60) {
		s.formError(w, r, "#category-error", loc.T("error.category_name_required"))
		return
	}
	icon := strings.TrimSpace(formStr(r, "icon"))
	if !maxLen(icon, 16) {
		s.formError(w, r, "#category-error", loc.T("error.input_toolong"))
		return
	}

	existing, err := s.store.ListCategories(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	maxSort := 0
	for _, c := range existing {
		if strings.EqualFold(c.Name, name) {
			s.formError(w, r, "#category-error", loc.T("error.category_exists"))
			return
		}
		if c.SortOrder > maxSort {
			maxSort = c.SortOrder
		}
	}

	c := &models.Category{Name: name, Icon: icon, SortOrder: maxSort + 1}
	if err := s.store.CreateCategory(r.Context(), c); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.fragment(w, r, "category_item", c)
}

// handleDeleteCategory removes a category. Existing items keep their (denormalized)
// category label, so deleting a category never orphans data.
func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "categoryID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteCategory(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
