package server

import (
	"net/http"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

// peoplePalette are the accent colors auto-assigned to new people, cycled by
// creation order so participants stay visually distinct in the budget view.
var peoplePalette = []string{
	"#2563eb", "#db2777", "#059669", "#d97706", "#7c3aed",
	"#0891b2", "#dc2626", "#65a30d", "#c026d3", "#0d9488",
}

// handleCreatePerson adds a new person that trip costs can be attributed to.
func (s *Server) handleCreatePerson(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	name := strings.TrimSpace(formStr(r, "name"))
	if name == "" || !maxLen(name, 60) {
		s.formError(w, r, "#person-error", loc.T("error.person_name_required"))
		return
	}

	existing, err := s.store.ListPeople(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	maxSort := 0
	for _, p := range existing {
		if strings.EqualFold(p.Name, name) {
			s.formError(w, r, "#person-error", loc.T("error.person_exists"))
			return
		}
		if p.SortOrder > maxSort {
			maxSort = p.SortOrder
		}
	}

	p := &models.Person{
		Name:      name,
		Color:     peoplePalette[len(existing)%len(peoplePalette)],
		SortOrder: maxSort + 1,
	}
	if err := s.store.CreatePerson(r.Context(), p); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.fragment(w, r, "person_item", p)
}

// handleDeletePerson removes a person. Expenses they paid keep their amount but
// become unassigned (paid_by is cleared), so deleting a person never orphans data.
func (s *Server) handleDeletePerson(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "personID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeletePerson(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
