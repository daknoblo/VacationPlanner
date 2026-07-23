package server

import (
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/version"
)

// Project links shown on the About page.
const (
	aboutRepoURL = "https://github.com/daknoblo/vacationplanner"
	aboutDocsURL = "https://github.com/daknoblo/vacationplanner#readme"
)

// aboutView is the data envelope for the About page.
type aboutView struct {
	Version string
	RepoURL string
	DocsURL string
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	s.page(w, r, "about", loc.T("about.title"), aboutView{
		Version: version.String(),
		RepoURL: aboutRepoURL,
		DocsURL: aboutDocsURL,
	})
}
