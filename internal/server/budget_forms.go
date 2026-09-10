package server

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

// Invalid payer input must not silently clear an existing payment reference.
func parseBudgetPayer(r *http.Request) (*uuid.UUID, error) {
	raw := formStr(r, "paid_by")
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return nil, errValidation(i18n.FromContext(r.Context()).T("error.paid_by_invalid"))
	}
	return &id, nil
}
